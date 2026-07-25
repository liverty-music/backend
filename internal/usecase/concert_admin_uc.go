package usecase

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
)

// PendingConcertReview pairs a staged concert with its resolved performer
// for presentation in the admin review queue.
type PendingConcertReview struct {
	// Staged is the staged concert row awaiting approval.
	Staged *entity.StagedConcert
	// Performer is the artist who will perform at the concert.
	Performer *entity.Artist
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

	// Approve promotes a pending staged concert to a published event. It
	// resolves or creates the venues row from the staged resolved fields, builds
	// the Concert/Series/Event entities, inserts them, publishes CONCERT.created,
	// and deletes the staged row. The operation is idempotent: if the staged row
	// is already gone (e.g. double-click), the method returns success without
	// duplicating.
	//
	// # Possible errors
	//
	//  - NotFound: If the staged concert does not exist (idempotent — treated as
	//    success internally; callers should not distinguish this).
	//  - FailedPrecondition: If an equivalent known-start event already covers
	//    the same (venue, date) and the staged concert has no start time. The
	//    staged row is preserved so a reviewer can reject it to clear the queue.
	//  - Internal: If the venue, series, or event insert fails.
	Approve(ctx context.Context, stagedID string) error

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
// all referencing rows. An empty id is rejected; deleting an absent id succeeds
// (the repository treats a zero affected-row count as success).
func (uc *concertUseCase) Delete(ctx context.Context, eventID string) error {
	if eventID == "" {
		return apperr.New(codes.InvalidArgument, "event id must not be empty")
	}
	if err := uc.concertRepo.Delete(ctx, eventID); err != nil {
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

// Approve promotes a pending staged concert to a published event.
func (uc *concertUseCase) Approve(ctx context.Context, stagedID string) error {
	sc, err := uc.stagedConcertRepo.GetByID(ctx, stagedID)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			// Idempotent: staged row already gone (already approved or rejected).
			uc.logger.Info(ctx, "approve: staged concert already gone — treating as success",
				slog.String("staged_concert_id", stagedID),
			)
			return nil
		}
		return fmt.Errorf("get staged concert: %w", err)
	}

	// Resolve or create the venues row from the staged resolved fields.
	venueID, err := uc.resolveOrCreateVenue(ctx, sc)
	if err != nil {
		return fmt.Errorf("resolve or create venue for staged concert %q: %w", stagedID, err)
	}

	// Convert the staged row back into a ScrapedConcert so buildAndInsertConcerts
	// can run the same series-adoption + fill + bulk-insert logic.
	scraped := stagedToScraped(sc)

	insertedIDs, err := buildAndInsertConcerts(
		ctx,
		sc.ArtistID,
		scraped,
		venueID,
		uc.seriesRepo,
		uc.concertRepo,
		uc.logger,
	)
	if err != nil {
		return fmt.Errorf("build and insert concerts for staged concert %q: %w", stagedID, err)
	}

	// When zero events were inserted it means an equivalent known-start event
	// already exists for this (venue, date) and the staged concert carries no
	// start time. Deleting the staged row here would silently lose it with no
	// recovery path. Instead, return FailedPrecondition so the caller surfaces
	// the condition to the reviewer, who can then reject it to clear the queue.
	if len(insertedIDs) == 0 {
		uc.logger.Warn(ctx, "approve: equivalent event already exists — staged row preserved for manual rejection",
			slog.String("artist_id", sc.ArtistID),
			slog.String("staged_concert_id", stagedID),
		)
		return apperr.New(codes.FailedPrecondition,
			"an equivalent event already exists for this venue and date; reject this entry to remove it from the queue")
	}

	uc.logger.Info(ctx, "staged concert approved and published",
		slog.String("artist_id", sc.ArtistID),
		slog.String("staged_concert_id", stagedID),
		slog.Int("inserted", len(insertedIDs)),
	)

	// Publish CONCERT.created for every genuinely inserted event.
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

	// Delete the staged row only after a successful insertion.
	if err := uc.stagedConcertRepo.Delete(ctx, stagedID); err != nil {
		return fmt.Errorf("delete staged concert after approval: %w", err)
	}

	return nil
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

	// Fetch the artist name for the log — it is captured at rejection time for
	// readability even if the artist is later deleted.
	artist, err := uc.artistRepo.Get(ctx, sc.ArtistID)
	if err != nil {
		return fmt.Errorf("get artist for rejection log: %w", err)
	}

	logID, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("generate rejection log ID: %w", err)
	}

	var rbPtr *string
	if reviewedBy != "" {
		rb := reviewedBy
		rbPtr = &rb
	}

	logEntry := &entity.RejectedConcertLog{
		ID:                logID.String(),
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

	if err := uc.stagedConcertRepo.Delete(ctx, stagedID); err != nil {
		return fmt.Errorf("delete staged concert after rejection: %w", err)
	}

	uc.logger.Info(ctx, "staged concert rejected and logged",
		slog.String("artist_id", sc.ArtistID),
		slog.String("staged_concert_id", stagedID),
		slog.String("reason", reason),
	)

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
func (uc *concertUseCase) resolveOrCreateVenue(ctx context.Context, sc *entity.StagedConcert) (string, error) {
	adminArea := resolveVenueAdminArea(sc)

	// Step 1: if the venue was resolved via Google Places at staging time, look
	// up an existing venues row by place_id first.
	if sc.ResolvedPlaceID != nil {
		existing, err := uc.venueRepo.GetByPlaceID(ctx, *sc.ResolvedPlaceID)
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
	existing, err := uc.venueRepo.GetByListedName(ctx, sc.ListedVenueName, adminArea)
	if err == nil {
		// Backfill place_id when the found venue lacks one and the staged row
		// carries a resolved value — never overwrite a non-NULL place_id.
		if sc.ResolvedPlaceID != nil && existing.GooglePlaceID == nil {
			if err := uc.venueRepo.BackfillPlaceID(ctx, existing.ID, *sc.ResolvedPlaceID); err != nil {
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
	return uc.createVenueFromStaged(ctx, sc)
}

// createVenueFromStaged creates a new venues row from the denormalised fields
// on the staged concert row.
func (uc *concertUseCase) createVenueFromStaged(ctx context.Context, sc *entity.StagedConcert) (string, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", fmt.Errorf("generate venue ID: %w", err)
	}

	// Use the canonical resolved name when available; fall back to the raw
	// listed name for unresolved concerts.
	name := sc.ListedVenueName
	if sc.ResolvedVenueName != nil {
		name = *sc.ResolvedVenueName
	}

	venue := &entity.Venue{
		ID:              id.String(),
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
	venueID, err := uc.venueRepo.Create(ctx, venue)
	if err != nil {
		return "", fmt.Errorf("create venue from staged concert: %w", err)
	}

	uc.logger.Info(ctx, "created venue from staged concert",
		slog.String("venue_id", venueID),
		slog.String("venue_name", name),
	)

	return venueID, nil
}

// stagedToScraped converts a StagedConcert back into a ScrapedConcert so the
// shared buildAndInsertConcerts helper can process it without duplication of
// the series-adoption and fill logic.
func stagedToScraped(sc *entity.StagedConcert) *entity.ScrapedConcert {
	scraped := &entity.ScrapedConcert{
		Title:           sc.Title,
		ListedVenueName: sc.ListedVenueName,
		LocalDate:       sc.LocalDate,
	}
	if sc.StartTime != nil {
		scraped.StartTime = *sc.StartTime
	}
	if sc.OpenTime != nil {
		scraped.OpenTime = *sc.OpenTime
	}
	if sc.AdminArea != nil {
		scraped.AdminArea = sc.AdminArea
	}
	if sc.SourceURL != nil {
		scraped.SourceURL = *sc.SourceURL
	}
	return scraped
}
