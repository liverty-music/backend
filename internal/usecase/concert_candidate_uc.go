package usecase

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-logging/logging"
)

// ConcertCandidateReview pairs a concert candidate with its resolved performer
// for presentation in the admin review queue.
type ConcertCandidateReview struct {
	// ConcertCandidate is the concert candidate row awaiting approval.
	ConcertCandidate *entity.ConcertCandidate
	// Performer is the artist who will perform at the concert.
	Performer *entity.Artist
}

// ApproveResolution selects how Approve reconciles a concert candidate that
// duplicates an already-published event. It mirrors the admin Approve RPC's
// Resolution enum but is defined in the usecase layer so the business logic does
// not depend on the generated proto types.
type ApproveResolution int

const (
	// ApproveResolutionUnspecified makes Approve return a DuplicateConflict
	// (without mutating state) when it detects a duplicate; otherwise it publishes.
	ApproveResolutionUnspecified ApproveResolution = iota
	// ApproveResolutionKeepExisting logs the staged row to the rejection log with a
	// duplicate reason and clears it, leaving the existing event unchanged.
	ApproveResolutionKeepExisting
	// ApproveResolutionAdoptStaged overwrites the existing event's display fields
	// from the staged row (listed venue name, and NULL-only start/open fill) and
	// clears the staged row.
	ApproveResolutionAdoptStaged
)

// ApproveResult is the outcome of an approval attempt. A nil Conflict means the
// concert was published (or reconciled) and the staged row cleared. A non-nil
// Conflict means a duplicate was detected with no resolution chosen: no state was
// mutated and the caller must re-invoke Approve with a resolution.
type ApproveResult struct {
	// Conflict is set only on an unresolved duplicate; nil otherwise.
	Conflict *DuplicateConflict
}

// DuplicateConflict describes a concert candidate that maps onto an already-published
// event, carrying both records so the reviewer can choose a resolution.
type DuplicateConflict struct {
	// Existing is the already-published event's display-field preview.
	Existing *ExistingEventDisplay
	// ConcertCandidate is the concert candidate the reviewer proposed to publish.
	ConcertCandidate *entity.ConcertCandidate
	// ConcertCandidatePerformer is the artist the concert candidate was discovered for.
	ConcertCandidatePerformer *entity.Artist
}

// ExistingEventDisplay is the display-field preview of an already-published event
// that a concert candidate collides with. Venue identity is intentionally omitted —
// reconciliation never changes it.
type ExistingEventDisplay struct {
	// EventID is the unique identifier of the already-published event.
	EventID string
	// Title is the descriptive title of the existing event (from its series).
	Title string
	// ListedVenueName is the venue name currently stored on the existing event.
	ListedVenueName string
	// LocalDate is the local calendar date of the existing event.
	LocalDate time.Time
	// StartTime is the existing event's start time; nil when unknown.
	StartTime *time.Time
	// OpenTime is the existing event's door-open time; nil when unknown.
	OpenTime *time.Time
}

// ConcertCandidateUseCase is the admin-console approval gate over
// AI-discovered concerts: the review queue of ConcertCandidates, approving one
// into a published event, or rejecting it into the RejectedConcert record.
type ConcertCandidateUseCase interface {
	// List returns all concert candidates currently awaiting review, each
	// paired with the performing artist resolved per row. Results are ordered
	// oldest-first (review-queue order).
	//
	// # Possible errors
	//
	//  - Internal: If the repository query or a per-row artist lookup fails.
	List(ctx context.Context) ([]*ConcertCandidateReview, error)

	// Approve promotes a pending concert candidate to a published event. It resolves
	// or creates the venues row, and — when the concert candidate does not duplicate
	// an existing event — builds the Concert/Series/Event entities, inserts them,
	// publishes CONCERT.created, and deletes the staged row. The operation is
	// idempotent: if the staged row is already gone (e.g. double-click) the method
	// returns a non-conflict success without duplicating.
	//
	// When the concert candidate maps onto an already-published event at the resolved
	// (venue, date, start time), the behavior depends on resolution:
	//   - ApproveResolutionUnspecified: returns an ApproveResult whose Conflict is
	//     set to the existing event's display fields plus the staged preview; NO
	//     state is mutated.
	//   - ApproveResolutionKeepExisting: records the staged row in the rejection log
	//     (attributed to reviewerID) with a duplicate reason and clears it, leaving
	//     the existing event unchanged.
	//   - ApproveResolutionAdoptStaged: overwrites the existing event's listed venue
	//     name and fills its start/open time only where currently NULL, leaving
	//     venue identity and the shared series title unchanged, then clears the
	//     staged row.
	//
	// reviewerID is the calling reviewer's identity (used only by KeepExisting).
	//
	// # Possible errors
	//
	//  - Internal: If the venue, series, or event mutation fails.
	Approve(ctx context.Context, concertCandidateID string, resolution ApproveResolution, reviewerID string) (*ApproveResult, error)

	// Reject records the concert candidate in the rejection log and deletes the
	// staged row. It is idempotent: if the staged row is already gone, the
	// method returns success without creating a duplicate log entry.
	//
	// # Possible errors
	//
	//  - NotFound: If the concert candidate does not exist (idempotent — treated as
	//    success internally).
	//  - Internal: If the log append or delete fails.
	Reject(ctx context.Context, concertCandidateID string, reason string, reviewedBy string) error
}

// concertCandidateUseCase implements [ConcertCandidateUseCase] over the
// concert use case's repositories and venue/duplicate helpers.
type concertCandidateUseCase struct {
	*concertUseCase
}

var _ ConcertCandidateUseCase = (*concertCandidateUseCase)(nil)

// NewConcertCandidateUseCase creates the ConcertCandidate review use case on
// the dependencies of concerts.
func NewConcertCandidateUseCase(concerts *concertUseCase) ConcertCandidateUseCase {
	return &concertCandidateUseCase{concertUseCase: concerts}
}

// List returns all concert candidates awaiting review, each paired with
// the resolved performing artist.
func (uc *concertCandidateUseCase) List(ctx context.Context) ([]*ConcertCandidateReview, error) {
	staged, err := uc.concertCandidateRepo.ListPending(ctx)
	if err != nil {
		return nil, fmt.Errorf("list pending staged concerts: %w", err)
	}

	reviews := make([]*ConcertCandidateReview, 0, len(staged))
	for _, sc := range staged {
		artist, err := uc.artistRepo.Get(ctx, sc.ArtistID)
		if err != nil {
			return nil, fmt.Errorf("get artist %q for staged concert %q: %w", sc.ArtistID, sc.ID, err)
		}
		reviews = append(reviews, &ConcertCandidateReview{
			ConcertCandidate: sc,
			Performer:        artist,
		})
	}

	return reviews, nil
}

// Approve promotes a pending concert candidate to a published event, reconciling a
// duplicate existing event per the resolution when one is detected.
func (uc *concertCandidateUseCase) Approve(ctx context.Context, concertCandidateID string, resolution ApproveResolution, reviewerID string) (*ApproveResult, error) {
	sc, err := uc.concertCandidateRepo.GetByID(ctx, concertCandidateID)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			// Idempotent: staged row already gone (already approved or rejected).
			// This also covers the second phase of a two-call reconcile where the
			// first call already cleared the row.
			uc.logger.Info(ctx, "approve: staged concert already gone — treating as success",
				slog.String("concert_candidate_id", concertCandidateID),
			)
			return &ApproveResult{}, nil
		}
		return nil, fmt.Errorf("get staged concert: %w", err)
	}

	// Resolve or create the venues row from the staged resolved fields.
	venueID, err := resolveOrCreateVenue(ctx, sc, uc.venueRepo, uc.logger)
	if err != nil {
		return nil, fmt.Errorf("resolve or create venue for staged concert %q: %w", concertCandidateID, err)
	}

	// Fetch the events at the resolved (venue, date) ONCE and reuse the slice for
	// both duplicate detection and the fill-vs-insert decision.
	existing, err := uc.concertRepo.FindEventsByVenueAndDate(ctx, []string{venueID}, []time.Time{sc.LocalDate})
	if err != nil {
		return nil, fmt.Errorf("find existing events for staged concert %q: %w", concertCandidateID, err)
	}

	// Detect a duplicate existing event at the resolved (venue, date, start time)
	// BEFORE any event mutation, so an unresolved conflict returns without side
	// effects. Re-checked on every call, so a two-phase reconcile re-validates.
	if conflictEvent := duplicateEventAmong(existing, sc); conflictEvent != nil {
		return uc.reconcileDuplicate(ctx, sc, conflictEvent, resolution, reviewerID)
	}

	// No duplicate — insert the event under the series_id carried on the staged
	// row (the series row already exists, created at discovery when the group's
	// series_id was resolved). Approval never mints a series, adopts identity, or
	// re-derives grouping. A known-start staged row that fills an existing
	// unknown-start row legitimately inserts nothing; that is a success, not a
	// conflict.
	ev := stagedToEvent(sc)
	var insertedIDs []string
	if match, isFill := resolveExistingEvent(existing, ev, map[string]bool{}); isFill {
		if err := uc.concertRepo.FillEventStartTimes(ctx,
			[]string{match.ID},
			[]*time.Time{sc.StartTime},
			[]*time.Time{sc.OpenTime},
		); err != nil {
			return nil, fmt.Errorf("fill event start times for staged concert %q: %w", concertCandidateID, err)
		}
	} else {
		insertedIDs, err = insertEventUnderSeries(ctx, sc.ArtistID, sc.SeriesID, ev, venueID, uc.concertRepo)
		if err != nil {
			return nil, fmt.Errorf("insert event under series for staged concert %q: %w", concertCandidateID, err)
		}
	}

	// Publish CONCERT.created only for genuinely inserted events (a fill inserts
	// nothing but still updates an existing row, which needs no new-concert event).
	if len(insertedIDs) > 0 {
		created := ConcertCreatedData{
			ArtistID:   sc.ArtistID,
			ConcertIDs: insertedIDs,
		}
		if err := uc.publisher.PublishEvent(ctx, entity.SubjectConcertCreated, created); err != nil {
			uc.logger.Error(ctx, "failed to publish CONCERT.created after approval", err,
				slog.String("concert_candidate_id", concertCandidateID),
			)
			// Non-fatal: event is persisted; notification will retry or be missed.
		}
	}

	// Delete the staged row only after a successful publish/fill.
	if err := uc.concertCandidateRepo.Delete(ctx, concertCandidateID); err != nil {
		return nil, fmt.Errorf("delete staged concert after approval: %w", err)
	}

	// When the approval only filled an existing row (no event inserted under
	// sc.SeriesID) and this staged row was the last reference to a discovery-minted
	// series, that series is now orphaned. Sweep it, mirroring Reject — scoped to
	// this series so it never races another run's eager series row. A series that
	// gained an inserted event is not orphaned, so the sweep is a no-op there.
	if len(insertedIDs) == 0 {
		if _, err := uc.seriesRepo.DeleteOrphaned(ctx, []string{sc.SeriesID}); err != nil {
			uc.logger.Error(ctx, "failed to clean up orphaned series after fill approval", err,
				slog.String("concert_candidate_id", concertCandidateID),
			)
			// Non-fatal: an orphaned series row is harmless and swept on the next run.
		}
	}

	uc.logger.Info(ctx, "staged concert approved and published",
		slog.String("artist_id", sc.ArtistID),
		slog.String("series_id", sc.SeriesID),
		slog.String("concert_candidate_id", concertCandidateID),
		slog.Int("inserted", len(insertedIDs)),
	)

	return &ApproveResult{}, nil
}

// detectDuplicateEvent returns the already-published event a concert candidate
// duplicates at the resolved (venue, date, start time), or nil when the concert
// is genuinely new or merely fills an existing unknown-start row. It mirrors the
// two zero-insert paths the creation path would otherwise hit:
//   - an exact-start match (equal StartKey, including both-unknown), and
//   - an unknown-start staged row when a known-start row already covers the slot.
//
// The fill case (known-start staged onto an unknown-start existing row) is NOT a
// duplicate: it proceeds to the creation path, which fills the existing row.
func detectDuplicateEvent(ctx context.Context, sc *entity.ConcertCandidate, venueID string, concertRepo entity.ConcertRepository) (*entity.Event, error) {
	events, err := concertRepo.FindEventsByVenueAndDate(ctx, []string{venueID}, []time.Time{sc.LocalDate})
	if err != nil {
		return nil, fmt.Errorf("find existing events: %w", err)
	}
	return duplicateEventAmong(events, sc), nil
}

// duplicateEventAmong is the pure duplicate-detection core of detectDuplicateEvent,
// operating on already-fetched events so callers that already hold the (venue,
// date) slice (e.g. Approve) do not re-query.
func duplicateEventAmong(events []*entity.Event, sc *entity.ConcertCandidate) *entity.Event {
	ev := stagedToEvent(sc)
	match, isFill := resolveExistingEvent(events, ev, map[string]bool{})
	if match != nil && !isFill {
		// Exact-start duplicate (or both-unknown at the same venue/date).
		return match
	}
	if ev.StartTime.IsZero() {
		// Unknown-start staged row: a duplicate iff any known-start row exists here.
		for _, e := range events {
			if e.StartTime != nil && !e.StartTime.IsZero() {
				return e
			}
		}
	}
	return nil
}

// reconcileDuplicate applies the reviewer's resolution to a detected duplicate.
func (uc *concertCandidateUseCase) reconcileDuplicate(ctx context.Context, sc *entity.ConcertCandidate, existing *entity.Event, resolution ApproveResolution, reviewerID string) (*ApproveResult, error) {
	switch resolution {
	case ApproveResolutionKeepExisting:
		reason := fmt.Sprintf("duplicate of existing event %s", existing.ID)
		if err := uc.appendRejectionLog(ctx, sc, reason, reviewerID); err != nil {
			return nil, fmt.Errorf("keep-existing: append rejection log: %w", err)
		}
		if err := uc.concertCandidateRepo.Delete(ctx, sc.ID); err != nil {
			return nil, fmt.Errorf("keep-existing: delete staged concert: %w", err)
		}
		uc.logger.Info(ctx, "duplicate reconciled: kept existing event, staged row logged and cleared",
			slog.String("concert_candidate_id", sc.ID),
			slog.String("existing_event_id", existing.ID),
		)
		return &ApproveResult{}, nil

	case ApproveResolutionAdoptStaged:
		// Overwrite the display venue name and fill start/open only where NULL
		// (COALESCE), leaving venue identity and the shared series title untouched.
		if err := uc.concertRepo.UpdateEventListedVenueName(ctx, existing.ID, sc.ListedVenueName); err != nil {
			return nil, fmt.Errorf("adopt-staged: update listed venue name: %w", err)
		}
		if err := uc.concertRepo.FillEventStartTimes(ctx,
			[]string{existing.ID},
			[]*time.Time{sc.StartTime},
			[]*time.Time{sc.OpenTime},
		); err != nil {
			return nil, fmt.Errorf("adopt-staged: fill start/open times: %w", err)
		}
		if err := uc.concertCandidateRepo.Delete(ctx, sc.ID); err != nil {
			return nil, fmt.Errorf("adopt-staged: delete staged concert: %w", err)
		}
		uc.logger.Info(ctx, "duplicate reconciled: adopted staged display fields onto existing event",
			slog.String("concert_candidate_id", sc.ID),
			slog.String("existing_event_id", existing.ID),
		)
		return &ApproveResult{}, nil

	default:
		// Unspecified: surface the conflict for reviewer choice; do not mutate.
		conflict, err := uc.buildDuplicateConflict(ctx, sc, existing)
		if err != nil {
			return nil, err
		}
		uc.logger.Info(ctx, "approve: duplicate event detected — returning conflict for reviewer choice",
			slog.String("concert_candidate_id", sc.ID),
			slog.String("existing_event_id", existing.ID),
		)
		return &ApproveResult{Conflict: conflict}, nil
	}
}

// buildDuplicateConflict assembles the conflict DTO: the existing event's display
// fields (its title resolved from the shared series) plus the staged preview and
// its performer.
func (uc *concertCandidateUseCase) buildDuplicateConflict(ctx context.Context, sc *entity.ConcertCandidate, existing *entity.Event) (*DuplicateConflict, error) {
	series, err := uc.seriesRepo.Get(ctx, existing.SeriesID)
	if err != nil {
		return nil, fmt.Errorf("get series for duplicate conflict: %w", err)
	}
	performer, err := uc.artistRepo.Get(ctx, sc.ArtistID)
	if err != nil {
		return nil, fmt.Errorf("get performer for duplicate conflict: %w", err)
	}

	listedName := ""
	if existing.ListedVenueName != nil {
		listedName = *existing.ListedVenueName
	}

	return &DuplicateConflict{
		Existing: &ExistingEventDisplay{
			EventID:         existing.ID,
			Title:           series.Title,
			ListedVenueName: listedName,
			LocalDate:       existing.LocalDate,
			StartTime:       existing.StartTime,
			OpenTime:        existing.OpenTime,
		},
		ConcertCandidate:          sc,
		ConcertCandidatePerformer: performer,
	}, nil
}

// Reject records the concert candidate in the rejection log and deletes the
// staged row.
func (uc *concertCandidateUseCase) Reject(ctx context.Context, concertCandidateID string, reason string, reviewedBy string) error {
	sc, err := uc.concertCandidateRepo.GetByID(ctx, concertCandidateID)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			// Idempotent: staged row already gone.
			uc.logger.Info(ctx, "reject: staged concert already gone — treating as success",
				slog.String("concert_candidate_id", concertCandidateID),
			)
			return nil
		}
		return fmt.Errorf("get staged concert: %w", err)
	}

	if err := uc.appendRejectionLog(ctx, sc, reason, reviewedBy); err != nil {
		return err
	}

	if err := uc.concertCandidateRepo.Delete(ctx, concertCandidateID); err != nil {
		return fmt.Errorf("delete staged concert after rejection: %w", err)
	}

	// Sweep the parent series if this rejection left it with no events and no
	// other pending staged rows (a discovered series whose every event was staged
	// and then rejected). Scoped to just this staged row's series.
	if _, err := uc.seriesRepo.DeleteOrphaned(ctx, []string{sc.SeriesID}); err != nil {
		uc.logger.Error(ctx, "failed to clean up orphaned series after rejection", err,
			slog.String("concert_candidate_id", concertCandidateID),
		)
		// Non-fatal: an orphaned series row is harmless and swept on the next run.
	}

	uc.logger.Info(ctx, "staged concert rejected and logged",
		slog.String("artist_id", sc.ArtistID),
		slog.String("concert_candidate_id", concertCandidateID),
		slog.String("reason", reason),
	)

	return nil
}

// appendRejectionLog captures a concert candidate in the rejection log with the
// given reason and reviewer identity. The artist name is snapshotted at write
// time for readability even if the artist is later deleted. Shared by Reject and
// the approval keep-existing reconciliation.
func (uc *concertCandidateUseCase) appendRejectionLog(ctx context.Context, sc *entity.ConcertCandidate, reason, reviewedBy string) error {
	artist, err := uc.artistRepo.Get(ctx, sc.ArtistID)
	if err != nil {
		return fmt.Errorf("get artist for rejection log: %w", err)
	}

	logID := entity.NewID()

	var rbPtr *string
	if reviewedBy != "" {
		rb := reviewedBy
		rbPtr = &rb
	}

	logEntry := &entity.RejectedConcert{
		ID:                logID,
		ArtistID:          sc.ArtistID,
		ArtistName:        artist.Name,
		Title:             sc.Title,
		LocalDate:         sc.LocalDate,
		StartTime:         sc.StartTime,
		OpenTime:          sc.OpenTime,
		ListedVenueName:   sc.ListedVenueName,
		AdminArea:         sc.AdminArea,
		SourceURL:         sc.SourceURL,
		ResolvedPlaceID:   sc.ResolvedPlaceID,
		ResolvedVenueName: sc.ResolvedVenueName,
		ResolvedAdminArea: sc.ResolvedAdminArea,
		Reason:            reason,
		ReviewedBy:        rbPtr,
	}

	if err := uc.rejectedConcertRepo.Append(ctx, logEntry); err != nil {
		return fmt.Errorf("append rejection log: %w", err)
	}
	return nil
}

// resolveVenueAdminArea derives the admin_area used for BOTH the
// (listed_venue_name, admin_area) lookup and the venue insert, preferring the
// Places-resolved value and falling back to the Gemini-extracted scraped value.
// The lookup and insert MUST use the same derivation, else a miss-then-collide
// asymmetry re-appears (lookup on the raw value, insert on the resolved value).
func resolveVenueAdminArea(sc *entity.ConcertCandidate) *string {
	if sc.ResolvedAdminArea != nil {
		return sc.ResolvedAdminArea
	}
	return sc.AdminArea
}

// resolveOrCreateVenue is an idempotent get-or-create over the venues row,
// place_id-authoritative: resolve by place_id, then by (listed_venue_name,
// admin_area), and only create when neither matches. Returns the venues.id to
// use for event insertion.
func resolveOrCreateVenue(ctx context.Context, sc *entity.ConcertCandidate, venueRepo entity.VenueRepository, logger *logging.Logger) (string, error) {
	adminArea := resolveVenueAdminArea(sc)

	// Step 1: if the venue was resolved via Google Places at staging time, look
	// up an existing venues row by place_id first.
	if sc.ResolvedPlaceID != nil {
		existing, err := venueRepo.GetByPlaceID(ctx, *sc.ResolvedPlaceID)
		if err == nil {
			return existing.ID, nil
		}
		if !errors.Is(err, apperr.ErrNotFound) {
			return "", fmt.Errorf("get venue by place ID: %w", err)
		}
		// place_id miss: fall through to the listed-name lookup. Google Places can
		// return a DIFFERENT place_id for a venue that already exists under
		// (listed_venue_name, admin_area), so creating here would collide on
		// idx_venues_listed_name_admin_area (the observed 和歌山ビッグホエール case).
	}

	// Step 2: look up by (listed_venue_name, admin_area).
	existing, err := venueRepo.GetByListedName(ctx, sc.ListedVenueName, adminArea)
	if err == nil {
		// Backfill place_id when the found venue lacks one and the staged row
		// carries a resolved value — never overwrite a non-NULL place_id.
		if sc.ResolvedPlaceID != nil && existing.GooglePlaceID == nil {
			if err := venueRepo.BackfillPlaceID(ctx, existing.ID, *sc.ResolvedPlaceID); err != nil {
				return "", fmt.Errorf("backfill venue place ID: %w", err)
			}
		}
		return existing.ID, nil
	}
	if !errors.Is(err, apperr.ErrNotFound) {
		return "", fmt.Errorf("get venue by listed name: %w", err)
	}

	// Step 3: neither key matched — create. Create absorbs a concurrent-insert
	// conflict on either index and re-SELECTs the survivor.
	return createVenueFromStaged(ctx, sc, venueRepo, logger)
}

// createVenueFromStaged creates a new venues row from the denormalised fields
// on the concert candidate row.
func createVenueFromStaged(ctx context.Context, sc *entity.ConcertCandidate, venueRepo entity.VenueRepository, logger *logging.Logger) (string, error) {
	id := entity.NewID()

	// Use the canonical resolved name when available; fall back to the raw
	// listed name for unresolved concerts.
	name := sc.ListedVenueName
	if sc.ResolvedVenueName != nil {
		name = *sc.ResolvedVenueName
	}

	venue := &entity.Venue{
		ID:              id,
		Name:            name,
		AdminArea:       resolveVenueAdminArea(sc),
		GooglePlaceID:   sc.ResolvedPlaceID,
		ListedVenueName: &sc.ListedVenueName,
	}
	if sc.ResolvedLatitude != nil && sc.ResolvedLongitude != nil {
		venue.Coordinates = &entity.Coordinates{
			Latitude:  *sc.ResolvedLatitude,
			Longitude: *sc.ResolvedLongitude,
		}
	}

	// Create returns the surviving row id: the one we generated on a real insert,
	// or an existing row's id when a concurrent create won the race.
	venueID, err := venueRepo.Create(ctx, venue)
	if err != nil {
		return "", fmt.Errorf("create venue from staged concert: %w", err)
	}

	logger.Info(ctx, "created venue from staged concert",
		slog.String("venue_id", venueID),
		slog.String("venue_name", name),
	)

	return venueID, nil
}

// stagedToEvent converts a ConcertCandidate into a DiscoveredEvent so the shared
// resolveExistingEvent / insertEventUnderSeries helpers can process an approved
// staged row without duplicating the fill-detection and insert logic. The
// series identity is NOT reconstructed here — it is carried separately via
// ConcertCandidate.SeriesID.
func stagedToEvent(sc *entity.ConcertCandidate) *entity.DiscoveredEvent {
	ev := &entity.DiscoveredEvent{
		ListedVenueName: sc.ListedVenueName,
		LocalDate:       sc.LocalDate,
	}
	if sc.StartTime != nil {
		ev.StartTime = *sc.StartTime
	}
	if sc.OpenTime != nil {
		ev.OpenTime = *sc.OpenTime
	}
	if sc.AdminArea != nil {
		ev.AdminArea = sc.AdminArea
	}
	return ev
}
