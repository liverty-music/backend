package usecase

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
)

// DraftEventInput is the intermediate representation of an event being authored,
// carrying the unresolved venue name and optional place ID alongside the timing
// fields. The usecase resolves or creates the venue row and produces a
// DraftEvent before persisting.
type DraftEventInput struct {
	// VenueName is the raw venue name as entered by the organizer.
	VenueName string
	// PlaceID is an optional Google Places place ID for precise venue resolution.
	PlaceID *string
	// LocalDate is the calendar date of the performance (midnight UTC).
	LocalDate time.Time
	StartTime *time.Time
	OpenTime  *time.Time
	AdminArea *string
}

// ConcertAuthoringUseCase defines the organizer-facing authoring surface for
// first-party concerts. All methods are scoped to a single calling organizer;
// authorization (ownership) is enforced before delegating to the repository.
type ConcertAuthoringUseCase interface {
	// CreateDraft authors a new first-party series in the DRAFT state. The
	// venues for each draft event are resolved via get-or-create before the
	// draft is stored. Returns the full authored concert.
	//
	// # Possible errors
	//
	//  - InvalidArgument: A required field is missing, the event list is empty,
	//    or a time is inconsistent (open > start, date in the past).
	//  - PermissionDenied: A performer is not among the caller's represented
	//    artists. Non-revealing.
	CreateDraft(ctx context.Context, callerOrgID string, draft *entity.Series, eventInputs []*DraftEventInput, performerArtistIDs []string) (*entity.Series, []*entity.Event, []*entity.Artist, error)

	// UpdateDraft replaces the draft content of an existing series. While
	// DRAFT, any field may change. While PUBLISHED, only title, description,
	// cover image URL, and event times are revised in place; followers are NOT
	// re-notified. CANCELLED series are rejected with FailedPrecondition.
	//
	// # Possible errors
	//
	//  - NotFound: The series does not exist.
	//  - PermissionDenied: The series is not owned by the caller. Non-revealing.
	//  - FailedPrecondition: The series is CANCELLED (terminal).
	UpdateDraft(ctx context.Context, callerOrgID, seriesID string, draft *entity.Series, eventInputs []*DraftEventInput, performerArtistIDs []string) (*entity.Series, []*entity.Event, []*entity.Artist, error)

	// Publish transitions a DRAFT series to PUBLISHED. For PUBLIC series this
	// emits exactly ONE CONCERT.created carrying the newly-published event IDs.
	// UNLISTED series publish without emitting. Returns the published concert.
	//
	// # Possible errors
	//
	//  - NotFound: The series does not exist.
	//  - PermissionDenied: The series is not owned by the caller. Non-revealing.
	//  - FailedPrecondition: A draft event slot is suppressed, or is owned by a
	//    different organizer.
	Publish(ctx context.Context, callerOrgID, seriesID string) (*entity.Series, []*entity.Event, []*entity.Artist, error)

	// Cancel marks a series CANCELLED. This is terminal: the series is dropped
	// from every fan surface and cannot be re-published. Emits CONCERT.cancelled.
	//
	// # Possible errors
	//
	//  - NotFound: The series does not exist.
	//  - PermissionDenied: The series is not owned by the caller. Non-revealing.
	//  - FailedPrecondition: The series is already CANCELLED.
	Cancel(ctx context.Context, callerOrgID, seriesID string) error

	// RegenerateToken issues a fresh share token for an UNLISTED series,
	// invalidating the previous URL. Returns the new share URL.
	//
	// # Possible errors
	//
	//  - NotFound: The series does not exist.
	//  - PermissionDenied: The series is not owned by the caller. Non-revealing.
	//  - FailedPrecondition: The series is not UNLISTED.
	RegenerateToken(ctx context.Context, callerOrgID, seriesID string) (string, error)

	// ListOwn returns all first-party series owned by the caller's organizer,
	// ordered newest first. Each returned series carries its events and artists.
	//
	// # Possible errors
	//
	//  - InvalidArgument: The organizerID is empty.
	ListOwn(ctx context.Context, callerOrgID string) ([]*entity.Series, []*[]*entity.Event, []*[]*entity.Artist, error)
}

// concertAuthoringUseCase implements ConcertAuthoringUseCase.
type concertAuthoringUseCase struct {
	seriesRepo  entity.SeriesRepository
	venueRepo   entity.VenueRepository
	organizerUC OrganizerUseCase
	publisher   EventPublisher
	logger      *logging.Logger
}

// Compile-time interface check.
var _ ConcertAuthoringUseCase = (*concertAuthoringUseCase)(nil)

// NewConcertAuthoringUseCase creates a new ConcertAuthoringUseCase. All fields
// are required. Media upload/attach lives in MediaUseCase, not here.
func NewConcertAuthoringUseCase(
	seriesRepo entity.SeriesRepository,
	venueRepo entity.VenueRepository,
	organizerUC OrganizerUseCase,
	publisher EventPublisher,
	logger *logging.Logger,
) ConcertAuthoringUseCase {
	return &concertAuthoringUseCase{
		seriesRepo:  seriesRepo,
		venueRepo:   venueRepo,
		organizerUC: organizerUC,
		publisher:   publisher,
		logger:      logger,
	}
}

// assertOwnsSeries verifies that the given series is owned by callerOrgID.
// It returns a non-revealing PermissionDenied error when the check fails.
func (uc *concertAuthoringUseCase) assertOwnsSeries(series *entity.Series, callerOrgID string) error {
	if series.OrganizerID == nil || *series.OrganizerID != callerOrgID {
		return apperr.New(codes.PermissionDenied, "permission denied")
	}
	return nil
}

// checkPerformerOwnership returns PermissionDenied (non-revealing) when any of
// the supplied artist IDs is not represented by the caller's organizer.
func (uc *concertAuthoringUseCase) checkPerformerOwnership(ctx context.Context, callerOrgID string, artistIDs []string) error {
	ownedArtists, err := uc.organizerUC.ListArtists(ctx, callerOrgID)
	if err != nil {
		return fmt.Errorf("list organizer artists: %w", err)
	}
	ownedSet := make(map[string]bool, len(ownedArtists))
	for _, a := range ownedArtists {
		ownedSet[a.ID] = true
	}
	for _, id := range artistIDs {
		if !ownedSet[id] {
			return apperr.New(codes.PermissionDenied, "permission denied")
		}
	}
	return nil
}

// generateUnlistedToken generates a cryptographically random 32-byte base64url
// token suitable as an unlisted share token.
func generateUnlistedToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate random bytes: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// resolveVenueForInput resolves or creates a venue row for one DraftEventInput.
// It prefers place_id lookup, falls back to listed-name lookup, then creates.
func (uc *concertAuthoringUseCase) resolveVenueForInput(ctx context.Context, inp *DraftEventInput) (string, error) {
	if inp.PlaceID != nil && *inp.PlaceID != "" {
		v, err := uc.venueRepo.GetByPlaceID(ctx, *inp.PlaceID)
		if err == nil {
			return v.ID, nil
		}
		if !errors.Is(err, apperr.ErrNotFound) {
			return "", fmt.Errorf("get venue by place id: %w", err)
		}
	}

	listed := inp.VenueName
	v, err := uc.venueRepo.GetByListedName(ctx, listed, inp.AdminArea)
	if err == nil {
		return v.ID, nil
	}
	if !errors.Is(err, apperr.ErrNotFound) {
		return "", fmt.Errorf("get venue by listed name: %w", err)
	}

	// Create a minimal venue row from the raw name. Coordinates and canonical
	// name are left blank here; a subsequent organizer-triggered resolution or
	// admin review can enrich the row. The listed_venue_name index guarantees
	// idempotency for concurrent creates at the same listed name.
	newVenue := &entity.Venue{
		ID:              entity.NewID(),
		Name:            listed,
		AdminArea:       inp.AdminArea,
		GooglePlaceID:   inp.PlaceID,
		ListedVenueName: &listed,
	}
	venueID, err := uc.venueRepo.Create(ctx, newVenue)
	if err != nil {
		return "", fmt.Errorf("create venue: %w", err)
	}
	return venueID, nil
}

// resolveDraftEvents converts DraftEventInputs into persisted-ready DraftEvents
// by resolving each venue and assigning new IDs.
func (uc *concertAuthoringUseCase) resolveDraftEvents(ctx context.Context, seriesID string, inputs []*DraftEventInput) ([]*entity.DraftEvent, error) {
	out := make([]*entity.DraftEvent, 0, len(inputs))
	for _, inp := range inputs {
		venueID, err := uc.resolveVenueForInput(ctx, inp)
		if err != nil {
			return nil, fmt.Errorf("resolve venue for event on %s: %w", inp.LocalDate.Format("2006-01-02"), err)
		}
		lvn := inp.VenueName
		de := &entity.DraftEvent{
			ID:              entity.NewID(),
			SeriesID:        seriesID,
			VenueID:         venueID,
			ListedVenueName: &lvn,
			LocalDate:       inp.LocalDate,
			StartTime:       inp.StartTime,
			OpenTime:        inp.OpenTime,
		}
		out = append(out, de)
	}
	return out, nil
}

// CreateDraft authors a new first-party series in the DRAFT state.
func (uc *concertAuthoringUseCase) CreateDraft(
	ctx context.Context,
	callerOrgID string,
	draft *entity.Series,
	eventInputs []*DraftEventInput,
	performerArtistIDs []string,
) (*entity.Series, []*entity.Event, []*entity.Artist, error) {
	if draft.Title == "" {
		return nil, nil, nil, apperr.New(codes.InvalidArgument, "title is required")
	}
	if len(eventInputs) == 0 {
		return nil, nil, nil, apperr.New(codes.InvalidArgument, "at least one event is required")
	}

	// Performer ownership check.
	if err := uc.checkPerformerOwnership(ctx, callerOrgID, performerArtistIDs); err != nil {
		return nil, nil, nil, err
	}

	// Validate event timing.
	if err := validateDraftEventInputs(eventInputs); err != nil {
		return nil, nil, nil, err
	}

	// Set authoring fields.
	draft.ID = entity.NewID()
	orgID := callerOrgID
	draft.OrganizerID = &orgID
	ps := entity.SeriesPublishStateDraft
	draft.PublishState = &ps

	// Resolve venues and build DraftEvents.
	draftEvents, err := uc.resolveDraftEvents(ctx, draft.ID, eventInputs)
	if err != nil {
		return nil, nil, nil, err
	}

	if err := uc.seriesRepo.CreateDraft(ctx, draft, draftEvents, performerArtistIDs); err != nil {
		return nil, nil, nil, fmt.Errorf("create draft: %w", err)
	}

	series, events, artists, err := uc.seriesRepo.GetAuthored(ctx, draft.ID)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("get authored after create: %w", err)
	}
	return series, events, artists, nil
}

// UpdateDraft replaces the draft content of an existing series.
func (uc *concertAuthoringUseCase) UpdateDraft(
	ctx context.Context,
	callerOrgID, seriesID string,
	draft *entity.Series,
	eventInputs []*DraftEventInput,
	performerArtistIDs []string,
) (*entity.Series, []*entity.Event, []*entity.Artist, error) {
	existing, err := uc.seriesRepo.Get(ctx, seriesID)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return nil, nil, nil, apperr.New(codes.PermissionDenied, "permission denied")
		}
		return nil, nil, nil, err
	}
	if err := uc.assertOwnsSeries(existing, callerOrgID); err != nil {
		return nil, nil, nil, err
	}
	if existing.PublishState != nil && *existing.PublishState == entity.SeriesPublishStateCancelled {
		return nil, nil, nil, apperr.New(codes.FailedPrecondition, "series is cancelled and cannot be updated")
	}

	// Performer ownership check.
	if err := uc.checkPerformerOwnership(ctx, callerOrgID, performerArtistIDs); err != nil {
		return nil, nil, nil, err
	}

	// Basic validation.
	if draft.Title == "" {
		return nil, nil, nil, apperr.New(codes.InvalidArgument, "title is required")
	}
	if len(eventInputs) == 0 {
		return nil, nil, nil, apperr.New(codes.InvalidArgument, "at least one event is required")
	}
	if err := validateDraftEventInputs(eventInputs); err != nil {
		return nil, nil, nil, err
	}

	// Preserve the series ID and organizer fields.
	draft.ID = seriesID
	draft.OrganizerID = existing.OrganizerID

	// Resolve venues and build DraftEvents.
	draftEvents, err := uc.resolveDraftEvents(ctx, seriesID, eventInputs)
	if err != nil {
		return nil, nil, nil, err
	}

	if err := uc.seriesRepo.UpdateDraft(ctx, draft, draftEvents, performerArtistIDs); err != nil {
		return nil, nil, nil, fmt.Errorf("update draft: %w", err)
	}

	series, events, artists, err := uc.seriesRepo.GetAuthored(ctx, seriesID)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("get authored after update: %w", err)
	}
	return series, events, artists, nil
}

// Publish transitions a DRAFT series to PUBLISHED.
func (uc *concertAuthoringUseCase) Publish(
	ctx context.Context,
	callerOrgID, seriesID string,
) (*entity.Series, []*entity.Event, []*entity.Artist, error) {
	series, err := uc.seriesRepo.Get(ctx, seriesID)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return nil, nil, nil, apperr.New(codes.PermissionDenied, "permission denied")
		}
		return nil, nil, nil, err
	}
	if err := uc.assertOwnsSeries(series, callerOrgID); err != nil {
		return nil, nil, nil, err
	}

	now := time.Now().UTC()
	newEventIDs, err := uc.seriesRepo.PublishDraft(ctx, seriesID, now)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("publish draft: %w", err)
	}

	// For UNLISTED series, generate and store the share token.
	if series.Visibility != nil && *series.Visibility == entity.SeriesVisibilityUnlisted {
		token, err := generateUnlistedToken()
		if err != nil {
			return nil, nil, nil, fmt.Errorf("generate unlisted token: %w", err)
		}
		if err := uc.seriesRepo.SetUnlistedToken(ctx, seriesID, token); err != nil {
			return nil, nil, nil, fmt.Errorf("set unlisted token: %w", err)
		}
	}

	published, events, artists, err := uc.seriesRepo.GetAuthored(ctx, seriesID)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("get authored after publish: %w", err)
	}

	// Emit CONCERT.created ONLY for PUBLIC series with newly inserted events.
	// The notification consumer (NotifyNewConcerts) is keyed on a single
	// performing artist, so emit one event per performer carrying the same new
	// event ids — every event features every series-level performer, so each
	// passes the consumer's membership check. Followers of every performer are
	// notified, and a single-performer series (the common case) emits exactly
	// once. This mirrors the per-artist emission of the discovery pipeline.
	if series.Visibility != nil && *series.Visibility == entity.SeriesVisibilityPublic && len(newEventIDs) > 0 {
		for _, artist := range artists {
			created := entity.ConcertCreatedData{
				ArtistID:   artist.ID,
				ConcertIDs: newEventIDs,
			}
			if err := uc.publisher.PublishEvent(ctx, entity.SubjectConcertCreated, created); err != nil {
				uc.logger.Error(ctx, "failed to publish CONCERT.created after organizer publish", err,
					slog.String("series_id", seriesID),
					slog.String("artist_id", artist.ID),
					slog.Int("new_event_count", len(newEventIDs)),
				)
				// Non-fatal: events are persisted; notification retries next cycle.
			}
		}
	}

	// Always emit the organizer.concert.published analytics event.
	if err := uc.publisher.PublishEvent(ctx, entity.SubjectOrganizerConcertPublished, entity.OrganizerConcertPublishedData{
		OrganizerID:   callerOrgID,
		SeriesID:      seriesID,
		NewEventCount: len(newEventIDs),
	}); err != nil {
		uc.logger.Warn(ctx, "failed to publish ORGANIZER.concert_published event",
			slog.String("series_id", seriesID),
			slog.Any("error", err),
		)
		// Non-fatal: the series is already published.
	}

	return published, events, artists, nil
}

// Cancel marks a series CANCELLED and emits CONCERT.cancelled.
func (uc *concertAuthoringUseCase) Cancel(ctx context.Context, callerOrgID, seriesID string) error {
	series, err := uc.seriesRepo.Get(ctx, seriesID)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return apperr.New(codes.PermissionDenied, "permission denied")
		}
		return err
	}
	if err := uc.assertOwnsSeries(series, callerOrgID); err != nil {
		return err
	}
	if series.PublishState != nil && *series.PublishState == entity.SeriesPublishStateCancelled {
		return apperr.New(codes.FailedPrecondition, "series is already cancelled")
	}

	now := time.Now().UTC()
	if err := uc.seriesRepo.MarkCancelled(ctx, seriesID, now); err != nil {
		return fmt.Errorf("mark cancelled: %w", err)
	}

	// Gather the event IDs for the CONCERT.cancelled payload.
	_, events, _, err := uc.seriesRepo.GetAuthored(ctx, seriesID)
	if err != nil {
		uc.logger.Warn(ctx, "failed to load events after cancel (payload incomplete)", slog.Any("error", err))
	}
	eventIDs := make([]string, 0, len(events))
	for _, ev := range events {
		eventIDs = append(eventIDs, ev.ID)
	}

	if err := uc.publisher.PublishEvent(ctx, entity.SubjectConcertCancelled, entity.ConcertCancelledData{
		SeriesID:   seriesID,
		ConcertIDs: eventIDs,
	}); err != nil {
		uc.logger.Error(ctx, "failed to publish CONCERT.cancelled event", err,
			slog.String("series_id", seriesID),
		)
		// Non-fatal: the series is already cancelled in the DB.
	}

	// Cancel keeps the series row (guard-hidden) and its series_media, so the DB
	// and object storage stay consistent. Media is reclaimed only when the series
	// is hard-deleted (series_media cascades; a future sweep GCs the objects).
	return nil
}

// RegenerateToken issues a fresh share token for an UNLISTED series.
func (uc *concertAuthoringUseCase) RegenerateToken(ctx context.Context, callerOrgID, seriesID string) (string, error) {
	series, err := uc.seriesRepo.Get(ctx, seriesID)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return "", apperr.New(codes.PermissionDenied, "permission denied")
		}
		return "", err
	}
	if err := uc.assertOwnsSeries(series, callerOrgID); err != nil {
		return "", err
	}
	if series.Visibility == nil || *series.Visibility != entity.SeriesVisibilityUnlisted {
		return "", apperr.New(codes.FailedPrecondition, "series is not UNLISTED; no share token applies")
	}

	token, err := generateUnlistedToken()
	if err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	if err := uc.seriesRepo.SetUnlistedToken(ctx, seriesID, token); err != nil {
		return "", fmt.Errorf("set unlisted token: %w", err)
	}

	// The share URL is the token itself — the frontend constructs the full URL.
	// Return the token directly; the handler converts to a share URL.
	return token, nil
}

// ListOwn returns all series authored by the caller's organizer.
func (uc *concertAuthoringUseCase) ListOwn(
	ctx context.Context,
	callerOrgID string,
) ([]*entity.Series, []*[]*entity.Event, []*[]*entity.Artist, error) {
	allSeries, err := uc.seriesRepo.ListByOrganizer(ctx, callerOrgID)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("list by organizer: %w", err)
	}

	allEvents := make([]*[]*entity.Event, len(allSeries))
	allArtists := make([]*[]*entity.Artist, len(allSeries))
	for i, s := range allSeries {
		_, events, artists, err := uc.seriesRepo.GetAuthored(ctx, s.ID)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("get authored for series %s: %w", s.ID, err)
		}
		ev := events
		ar := artists
		allEvents[i] = &ev
		allArtists[i] = &ar
	}
	return allSeries, allEvents, allArtists, nil
}

// validateDraftEventInputs checks that each event input carries a date, that
// open_time <= start_time when both are set, and that the date is not in the
// past relative to the current UTC day.
func validateDraftEventInputs(inputs []*DraftEventInput) error {
	today := time.Now().UTC().Truncate(24 * time.Hour)
	for _, inp := range inputs {
		if inp.LocalDate.IsZero() {
			return apperr.New(codes.InvalidArgument, "each event must have a local date")
		}
		localDateUTC := inp.LocalDate.UTC().Truncate(24 * time.Hour)
		if localDateUTC.Before(today) {
			return apperr.New(codes.InvalidArgument,
				fmt.Sprintf("event date %s is in the past", inp.LocalDate.Format("2006-01-02")),
			)
		}
		if inp.OpenTime != nil && inp.StartTime != nil {
			if inp.OpenTime.After(*inp.StartTime) {
				return apperr.New(codes.InvalidArgument, "open time must not be after start time")
			}
		}
	}
	return nil
}
