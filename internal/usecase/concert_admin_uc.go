package usecase

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
)

// PendingConcertReview pairs a staged concert with its resolved performer
// for presentation in the admin review queue.
type PendingConcertReview struct {
	// Staged is the staged concert row awaiting approval.
	Staged *entity.StagedConcert
	// Performer is the artist who will perform at the concert.
	Performer *entity.Artist
}

// ApproveResolution selects how Approve reconciles a staged concert that
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

// DuplicateConflict describes a staged concert that maps onto an already-published
// event, carrying both records so the reviewer can choose a resolution.
type DuplicateConflict struct {
	// Existing is the already-published event's display-field preview.
	Existing *ExistingEventDisplay
	// Staged is the staged concert the reviewer proposed to publish.
	Staged *entity.StagedConcert
	// StagedPerformer is the artist the staged concert was discovered for.
	StagedPerformer *entity.Artist
}

// ExistingEventDisplay is the display-field preview of an already-published event
// that a staged concert collides with. Venue identity is intentionally omitted —
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

// AdminConcertUseCase defines the admin-console operations over concerts: the
// approval gate over AI-discovered concerts plus management of the published
// catalog. It is the admin-facing counterpart to ConcertUseCase; both are
// implemented by the same concertUseCase, so a consumer handler depending on
// ConcertUseCase never gains the approve/reject/delete capabilities.
type AdminConcertUseCase interface {
	// List returns every published concert (no audience filter), each carrying
	// its event id and performers, for catalog review and management.
	//
	// # Possible errors
	//
	//  - Internal: database query failure.
	List(ctx context.Context) ([]*entity.Concert, error)

	// ListPending returns all staged concerts currently awaiting review, each
	// paired with the performing artist resolved per row. Results are ordered
	// oldest-first (review-queue order).
	//
	// # Possible errors
	//
	//  - Internal: If the repository query or a per-row artist lookup fails.
	ListPending(ctx context.Context) ([]*PendingConcertReview, error)

	// Approve promotes a pending staged concert to a published event. It resolves
	// or creates the venues row, and — when the staged concert does not duplicate
	// an existing event — builds the Concert/Series/Event entities, inserts them,
	// publishes CONCERT.created, and deletes the staged row. The operation is
	// idempotent: if the staged row is already gone (e.g. double-click) the method
	// returns a non-conflict success without duplicating.
	//
	// When the staged concert maps onto an already-published event at the resolved
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
	Approve(ctx context.Context, stagedID string, resolution ApproveResolution, reviewerID string) (*ApproveResult, error)

	// Reject records the staged concert in the rejection log and deletes the
	// staged row. It is idempotent: if the staged row is already gone, the
	// method returns success without creating a duplicate log entry.
	//
	// # Possible errors
	//
	//  - NotFound: If the staged concert does not exist (idempotent — treated as
	//    success internally).
	//  - Internal: If the log append or delete fails.
	Reject(ctx context.Context, stagedID string, reason string, reviewedBy string) error

	// Delete permanently removes a published concert by its event id. The delete
	// cascades through the database's foreign keys to every referencing row. It
	// is idempotent: deleting an id that no longer exists succeeds.
	//
	// # Possible errors
	//
	//  - InvalidArgument: If the event id is empty or malformed.
	//  - Internal: If the delete fails.
	Delete(ctx context.Context, eventID string) error
}

// List returns every published concert for admin catalog management. Read logic
// is shared with the consumer path through ConcertRepository; this method only
// strips the audience filter.
func (uc *concertUseCase) List(ctx context.Context) ([]*entity.Concert, error) {
	concerts, err := uc.concertRepo.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list published concerts: %w", err)
	}
	return concerts, nil
}

// Delete permanently removes a published concert by its event id, cascading to
// all referencing rows, and records a suppression entry from the deleted event's
// natural key so a later discovery run does not re-create it. An empty id is
// rejected; deleting an absent id succeeds and records no suppression (the
// repository writes suppression only for a row that actually existed).
func (uc *concertUseCase) Delete(ctx context.Context, eventID string) error {
	if eventID == "" {
		return apperr.New(codes.InvalidArgument, "event id must not be empty")
	}
	if err := uc.concertRepo.DeleteAndSuppress(ctx, eventID); err != nil {
		return fmt.Errorf("delete concert %q: %w", eventID, err)
	}
	return nil
}

// ListPending returns all staged concerts awaiting review, each paired with
// the resolved performing artist.
func (uc *concertUseCase) ListPending(ctx context.Context) ([]*PendingConcertReview, error) {
	staged, err := uc.stagedConcertRepo.ListPending(ctx)
	if err != nil {
		return nil, fmt.Errorf("list pending staged concerts: %w", err)
	}

	reviews := make([]*PendingConcertReview, 0, len(staged))
	for _, sc := range staged {
		artist, err := uc.artistRepo.Get(ctx, sc.ArtistID)
		if err != nil {
			return nil, fmt.Errorf("get artist %q for staged concert %q: %w", sc.ArtistID, sc.ID, err)
		}
		reviews = append(reviews, &PendingConcertReview{
			Staged:    sc,
			Performer: artist,
		})
	}

	return reviews, nil
}

// Approve promotes a pending staged concert to a published event, reconciling a
// duplicate existing event per the resolution when one is detected.
func (uc *concertUseCase) Approve(ctx context.Context, stagedID string, resolution ApproveResolution, reviewerID string) (*ApproveResult, error) {
	sc, err := uc.stagedConcertRepo.GetByID(ctx, stagedID)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			// Idempotent: staged row already gone (already approved or rejected).
			// This also covers the second phase of a two-call reconcile where the
			// first call already cleared the row.
			uc.logger.Info(ctx, "approve: staged concert already gone — treating as success",
				slog.String("staged_concert_id", stagedID),
			)
			return &ApproveResult{}, nil
		}
		return nil, fmt.Errorf("get staged concert: %w", err)
	}

	// Resolve or create the venues row from the staged resolved fields.
	venueID, err := resolveOrCreateVenue(ctx, sc, uc.venueRepo, uc.logger)
	if err != nil {
		return nil, fmt.Errorf("resolve or create venue for staged concert %q: %w", stagedID, err)
	}

	// Fetch the events at the resolved (venue, date) ONCE and reuse the slice for
	// both duplicate detection and the fill-vs-insert decision.
	existing, err := uc.concertRepo.FindEventsByVenueAndDate(ctx, []string{venueID}, []time.Time{sc.LocalDate})
	if err != nil {
		return nil, fmt.Errorf("find existing events for staged concert %q: %w", stagedID, err)
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
			return nil, fmt.Errorf("fill event start times for staged concert %q: %w", stagedID, err)
		}
	} else {
		insertedIDs, err = insertEventUnderSeries(ctx, sc.ArtistID, sc.SeriesID, ev, venueID, uc.concertRepo)
		if err != nil {
			return nil, fmt.Errorf("insert event under series for staged concert %q: %w", stagedID, err)
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
				slog.String("staged_concert_id", stagedID),
			)
			// Non-fatal: event is persisted; notification will retry or be missed.
		}
	}

	// Delete the staged row only after a successful publish/fill.
	if err := uc.stagedConcertRepo.Delete(ctx, stagedID); err != nil {
		return nil, fmt.Errorf("delete staged concert after approval: %w", err)
	}

	uc.logger.Info(ctx, "staged concert approved and published",
		slog.String("artist_id", sc.ArtistID),
		slog.String("series_id", sc.SeriesID),
		slog.String("staged_concert_id", stagedID),
		slog.Int("inserted", len(insertedIDs)),
	)

	return &ApproveResult{}, nil
}

// detectDuplicateEvent returns the already-published event a staged concert
// duplicates at the resolved (venue, date, start time), or nil when the concert
// is genuinely new or merely fills an existing unknown-start row. It mirrors the
// two zero-insert paths the creation path would otherwise hit:
//   - an exact-start match (equal StartKey, including both-unknown), and
//   - an unknown-start staged row when a known-start row already covers the slot.
//
// The fill case (known-start staged onto an unknown-start existing row) is NOT a
// duplicate: it proceeds to the creation path, which fills the existing row.
func detectDuplicateEvent(ctx context.Context, sc *entity.StagedConcert, venueID string, concertRepo entity.ConcertRepository) (*entity.Event, error) {
	events, err := concertRepo.FindEventsByVenueAndDate(ctx, []string{venueID}, []time.Time{sc.LocalDate})
	if err != nil {
		return nil, fmt.Errorf("find existing events: %w", err)
	}
	return duplicateEventAmong(events, sc), nil
}

// duplicateEventAmong is the pure duplicate-detection core of detectDuplicateEvent,
// operating on already-fetched events so callers that already hold the (venue,
// date) slice (e.g. Approve) do not re-query.
func duplicateEventAmong(events []*entity.Event, sc *entity.StagedConcert) *entity.Event {
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
func (uc *concertUseCase) reconcileDuplicate(ctx context.Context, sc *entity.StagedConcert, existing *entity.Event, resolution ApproveResolution, reviewerID string) (*ApproveResult, error) {
	switch resolution {
	case ApproveResolutionKeepExisting:
		reason := fmt.Sprintf("duplicate of existing event %s", existing.ID)
		if err := uc.appendRejectionLog(ctx, sc, reason, reviewerID); err != nil {
			return nil, fmt.Errorf("keep-existing: append rejection log: %w", err)
		}
		if err := uc.stagedConcertRepo.Delete(ctx, sc.ID); err != nil {
			return nil, fmt.Errorf("keep-existing: delete staged concert: %w", err)
		}
		uc.logger.Info(ctx, "duplicate reconciled: kept existing event, staged row logged and cleared",
			slog.String("staged_concert_id", sc.ID),
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
		if err := uc.stagedConcertRepo.Delete(ctx, sc.ID); err != nil {
			return nil, fmt.Errorf("adopt-staged: delete staged concert: %w", err)
		}
		uc.logger.Info(ctx, "duplicate reconciled: adopted staged display fields onto existing event",
			slog.String("staged_concert_id", sc.ID),
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
			slog.String("staged_concert_id", sc.ID),
			slog.String("existing_event_id", existing.ID),
		)
		return &ApproveResult{Conflict: conflict}, nil
	}
}

// buildDuplicateConflict assembles the conflict DTO: the existing event's display
// fields (its title resolved from the shared series) plus the staged preview and
// its performer.
func (uc *concertUseCase) buildDuplicateConflict(ctx context.Context, sc *entity.StagedConcert, existing *entity.Event) (*DuplicateConflict, error) {
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
		Staged:          sc,
		StagedPerformer: performer,
	}, nil
}

// Reject records the staged concert in the rejection log and deletes the
// staged row.
func (uc *concertUseCase) Reject(ctx context.Context, stagedID string, reason string, reviewedBy string) error {
	sc, err := uc.stagedConcertRepo.GetByID(ctx, stagedID)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			// Idempotent: staged row already gone.
			uc.logger.Info(ctx, "reject: staged concert already gone — treating as success",
				slog.String("staged_concert_id", stagedID),
			)
			return nil
		}
		return fmt.Errorf("get staged concert: %w", err)
	}

	if err := uc.appendRejectionLog(ctx, sc, reason, reviewedBy); err != nil {
		return err
	}

	if err := uc.stagedConcertRepo.Delete(ctx, stagedID); err != nil {
		return fmt.Errorf("delete staged concert after rejection: %w", err)
	}

	// Sweep the parent series if this rejection left it with no events and no
	// other pending staged rows (a discovered series whose every event was staged
	// and then rejected). Scoped to just this staged row's series.
	if _, err := uc.seriesRepo.DeleteOrphaned(ctx, []string{sc.SeriesID}); err != nil {
		uc.logger.Error(ctx, "failed to clean up orphaned series after rejection", err,
			slog.String("staged_concert_id", stagedID),
		)
		// Non-fatal: an orphaned series row is harmless and swept on the next run.
	}

	uc.logger.Info(ctx, "staged concert rejected and logged",
		slog.String("artist_id", sc.ArtistID),
		slog.String("staged_concert_id", stagedID),
		slog.String("reason", reason),
	)

	return nil
}

// appendRejectionLog captures a staged concert in the rejection log with the
// given reason and reviewer identity. The artist name is snapshotted at write
// time for readability even if the artist is later deleted. Shared by Reject and
// the approval keep-existing reconciliation.
func (uc *concertUseCase) appendRejectionLog(ctx context.Context, sc *entity.StagedConcert, reason, reviewedBy string) error {
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

	logEntry := &entity.RejectedConcertLog{
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
func resolveVenueAdminArea(sc *entity.StagedConcert) *string {
	if sc.ResolvedAdminArea != nil {
		return sc.ResolvedAdminArea
	}
	return sc.AdminArea
}

// resolveOrCreateVenue is an idempotent get-or-create over the venues row,
// place_id-authoritative: resolve by place_id, then by (listed_venue_name,
// admin_area), and only create when neither matches. Returns the venues.id to
// use for event insertion.
func resolveOrCreateVenue(ctx context.Context, sc *entity.StagedConcert, venueRepo entity.VenueRepository, logger *logging.Logger) (string, error) {
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
// on the staged concert row.
func createVenueFromStaged(ctx context.Context, sc *entity.StagedConcert, venueRepo entity.VenueRepository, logger *logging.Logger) (string, error) {
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

// stagedToEvent converts a StagedConcert into a DiscoveredEvent so the shared
// resolveExistingEvent / insertEventUnderSeries helpers can process an approved
// staged row without duplicating the fill-detection and insert logic. The
// series identity is NOT reconstructed here — it is carried separately via
// StagedConcert.SeriesID.
func stagedToEvent(sc *entity.StagedConcert) *entity.DiscoveredEvent {
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
