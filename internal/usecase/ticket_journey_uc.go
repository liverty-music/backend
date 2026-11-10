package usecase

import (
	"context"
	"log/slog"

	"github.com/pannpers/go-logging/logging"

	"github.com/liverty-music/backend/internal/entity"
)

// TicketJourneyUseCase defines the interface for ticket journey business logic.
type TicketJourneyUseCase interface {
	// SetStatus records the status the fan set for the event, set by the fan.
	// After Applied, Unpaid or Paid it raises the fan's hype level for the
	// concert's artist to match the concert (see [raiseHypeToMatch]); a failed
	// raise does not fail SetStatus.
	//
	// # Possible errors:
	//
	//   - The upsert's failure, unchanged.
	SetStatus(ctx context.Context, userID, eventID string, status entity.TicketJourneyStatus) error

	// InferTracking starts tracking an event whose concert the fan added to
	// their calendar: it records Tracking, set by inference, only when the fan
	// has no journey for the event, and reports whether it did. It never
	// raises a hype level.
	//
	// # Possible errors:
	//
	//   - FailedPrecondition: the event or the fan does not exist.
	//   - The guarded upsert's other failures, unchanged.
	InferTracking(ctx context.Context, userID, eventID string) (bool, error)

	// Delete removes a ticket journey for the authenticated user and event.
	// Idempotent — deleting a non-existent journey succeeds silently.
	//
	// # Possible errors:
	//
	//   - Internal: unexpected failure.
	Delete(ctx context.Context, userID, eventID string) error

	// List retrieves all ticket journeys for the authenticated user.
	//
	// # Possible errors:
	//
	//   - Internal: query failure.
	List(ctx context.Context, userID string) ([]*entity.TicketJourney, error)

	// MarkPaid sets the fan's ticket journey for the event to Paid, set by the
	// platform, for a paid Order, from a checkout or a won lottery, replacing
	// whatever status the fan set, and then raises the fan's hype level to
	// match the concert. Running it again for the same Order leaves the
	// journey Paid. A failed raise does not fail MarkPaid.
	//
	// # Possible errors:
	//
	//   - The upsert's failure, unchanged; the announcement is redelivered.
	MarkPaid(ctx context.Context, paid entity.OrderPaidData) error

	// MarkApplied sets the fan's ticket journey for the event to Applied, set
	// by the platform, for each lottery entry announced as entered
	// (LotteryEntry.Entered, LOTTERY_ENTRY.entered), unless the entry is no longer
	// Entered or the journey is not replaceable by it (Unpaid or Paid). When it
	// recorded Applied, it raises the fan's hype level to match the concert. A
	// failed raise does not fail MarkApplied.
	//
	// # Possible errors:
	//
	//   - The entry read's or the guarded upsert's failure, unchanged; the
	//     announcement is redelivered.
	MarkApplied(ctx context.Context, entered entity.LotteryEntryEnteredData) error
}

// ticketJourneyUseCase implements the TicketJourneyUseCase interface.
type ticketJourneyUseCase struct {
	repo     entity.TicketJourneyRepository
	users    entity.UserRepository
	concerts entity.ConcertRepository
	follows  entity.FollowRepository
	entries  entity.LotteryEntryRepository
	logger   *logging.Logger
}

// Compile-time interface compliance check.
var _ TicketJourneyUseCase = (*ticketJourneyUseCase)(nil)

// NewTicketJourneyUseCase creates a new ticket journey use case.
func NewTicketJourneyUseCase(
	repo entity.TicketJourneyRepository,
	users entity.UserRepository,
	concerts entity.ConcertRepository,
	follows entity.FollowRepository,
	entries entity.LotteryEntryRepository,
	logger *logging.Logger,
) TicketJourneyUseCase {
	return &ticketJourneyUseCase{
		repo:     repo,
		users:    users,
		concerts: concerts,
		follows:  follows,
		entries:  entries,
		logger:   logger,
	}
}

// SetStatus records the status through TicketJourney.Upsert, which announces a
// created journey or a changed status (TICKET_JOURNEY.status_changed)
// together with the change, and then raises the hype level.
func (uc *ticketJourneyUseCase) SetStatus(ctx context.Context, userID, eventID string, status entity.TicketJourneyStatus) error {
	err := uc.repo.Upsert(ctx, &entity.TicketJourney{
		UserID:      userID,
		EventID:     eventID,
		Status:      status,
		StatusSetBy: entity.TicketJourneySetByFan,
	})
	if err != nil {
		return err
	}
	uc.raiseHypeToMatch(ctx, userID, eventID, status)
	return nil
}

// InferTracking implements [TicketJourneyUseCase].
func (uc *ticketJourneyUseCase) InferTracking(ctx context.Context, userID, eventID string) (bool, error) {
	return uc.repo.UpsertIfReplaceable(ctx, &entity.TicketJourney{
		UserID:      userID,
		EventID:     eventID,
		Status:      entity.TicketJourneyStatusTracking,
		StatusSetBy: entity.TicketJourneySetByInference,
	})
}

// Delete removes a ticket journey.
func (uc *ticketJourneyUseCase) Delete(ctx context.Context, userID, eventID string) error {
	return uc.repo.Delete(ctx, userID, eventID)
}

// List retrieves all ticket journeys for a user.
func (uc *ticketJourneyUseCase) List(ctx context.Context, userID string) ([]*entity.TicketJourney, error) {
	return uc.repo.ListByUser(ctx, userID)
}

// MarkPaid implements [TicketJourneyUseCase].
func (uc *ticketJourneyUseCase) MarkPaid(ctx context.Context, paid entity.OrderPaidData) error {
	err := uc.repo.Upsert(ctx, &entity.TicketJourney{
		UserID:      paid.UserID,
		EventID:     paid.EventID,
		Status:      entity.TicketJourneyStatusPaid,
		StatusSetBy: entity.TicketJourneySetByPlatform,
	})
	if err != nil {
		return err
	}
	uc.raiseHypeToMatch(ctx, paid.UserID, paid.EventID, entity.TicketJourneyStatusPaid)
	return nil
}

// MarkApplied implements [TicketJourneyUseCase]. It reads the entry first, so
// an announcement the forwarder delivered after the draw records nothing.
func (uc *ticketJourneyUseCase) MarkApplied(ctx context.Context, entered entity.LotteryEntryEnteredData) error {
	entry, err := uc.entries.Get(ctx, entered.LotteryEntryID)
	if err != nil {
		return err
	}
	if entry.State != entity.LotteryEntryStateEntered {
		uc.logger.Info(ctx, "lottery entry no longer entered; journey left as it is",
			slog.String("lottery_entry_id", string(entered.LotteryEntryID)))
		return nil
	}
	stored, err := uc.repo.UpsertIfReplaceable(ctx, &entity.TicketJourney{
		UserID:      entered.UserID,
		EventID:     entered.EventID,
		Status:      entity.TicketJourneyStatusApplied,
		StatusSetBy: entity.TicketJourneySetByPlatform,
	})
	if err != nil {
		return err
	}
	if stored {
		uc.raiseHypeToMatch(ctx, entered.UserID, entered.EventID, entity.TicketJourneyStatusApplied)
	}
	return nil
}

// raiseHypeToMatch raises the fan's hype level for the artist of the event's
// concert to the lowest level that matches the concert
// ([entity.LowestHypeMatching]), when the journey's status is Applied, Unpaid
// or Paid, the fan has a home, the event is a concert with a lowest matching
// level (its distance is known when it is outside the home), exactly one of its
// artists is followed by the fan, and that Follow is raisable to the level.
// The journey write has already succeeded and is what the caller asked for,
// so every failure here is logged and swallowed.
func (uc *ticketJourneyUseCase) raiseHypeToMatch(ctx context.Context, userID, eventID string, status entity.TicketJourneyStatus) {
	switch status {
	case entity.TicketJourneyStatusApplied, entity.TicketJourneyStatusUnpaid, entity.TicketJourneyStatusPaid:
	default:
		return
	}
	attrs := []slog.Attr{slog.String("user_id", userID), slog.String("event_id", eventID)}
	warn := func(msg string, err error) {
		uc.logger.Warn(ctx, msg, append(attrs, slog.Any("error", err))...)
	}

	user, err := uc.users.Get(ctx, userID)
	if err != nil {
		warn("hype raise skipped: failed to read the user", err)
		return
	}
	if user.Home == nil {
		return
	}
	concerts, err := uc.concerts.ListByIDs(ctx, []string{eventID})
	if err != nil {
		warn("hype raise skipped: failed to read the concert", err)
		return
	}
	if len(concerts) == 0 {
		return
	}
	concert := concerts[0]
	followed, err := uc.follows.ListByUser(ctx, userID)
	if err != nil {
		warn("hype raise skipped: failed to read the follows", err)
		return
	}
	follow := onlyFollowedPerformer(concert, followed)
	if follow == nil {
		return
	}
	target, ok := entity.LowestHypeMatching(user.Home, concert)
	if !ok || !follow.RaisableTo(target) {
		return
	}
	raised, err := uc.follows.RaiseHype(ctx, userID, follow.ArtistID, target)
	if err != nil {
		warn("hype raise failed", err)
		return
	}
	if raised {
		uc.logger.Info(ctx, "hype raised to match a concert",
			append(attrs, slog.String("artist_id", follow.ArtistID), slog.String("hype", string(target)))...)
	}
}

// onlyFollowedPerformer returns the fan's Follow of the concert's one followed
// artist, or nil when the fan follows none or several of its artists.
func onlyFollowedPerformer(concert *entity.Concert, followed []*entity.FollowedArtist) *entity.Follow {
	performing := make(map[string]bool, len(concert.Artists))
	for _, a := range concert.Artists {
		performing[a.ID] = true
	}
	var match *entity.Follow
	for _, fa := range followed {
		if fa.Artist == nil || !performing[fa.Artist.ID] {
			continue
		}
		if match != nil {
			return nil
		}
		match = &entity.Follow{UserID: fa.UserID, ArtistID: fa.Artist.ID, Hype: fa.Hype, HypeSetBy: fa.HypeSetBy}
	}
	return match
}
