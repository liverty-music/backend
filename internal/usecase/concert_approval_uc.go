package usecase

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-logging/logging"
)

// ConcertApprovalUseCase defines the interface for the admin-console approval
// gate over AI-discovered concerts.
type ConcertApprovalUseCase interface {
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
}

// concertApprovalUseCase implements ConcertApprovalUseCase.
type concertApprovalUseCase struct {
	stagedConcertRepo   entity.StagedConcertRepository
	rejectedConcertRepo entity.RejectedConcertLogRepository
	venueRepo           entity.VenueRepository
	seriesRepo          entity.SeriesRepository
	concertRepo         entity.ConcertRepository
	artistRepo          entity.ArtistRepository
	publisher           EventPublisher
	logger              *logging.Logger
}

// Compile-time interface compliance check.
var _ ConcertApprovalUseCase = (*concertApprovalUseCase)(nil)

// NewConcertApprovalUseCase creates a new ConcertApprovalUseCase.
func NewConcertApprovalUseCase(
	stagedConcertRepo entity.StagedConcertRepository,
	rejectedConcertRepo entity.RejectedConcertLogRepository,
	venueRepo entity.VenueRepository,
	seriesRepo entity.SeriesRepository,
	concertRepo entity.ConcertRepository,
	artistRepo entity.ArtistRepository,
	publisher EventPublisher,
	logger *logging.Logger,
) ConcertApprovalUseCase {
	return &concertApprovalUseCase{
		stagedConcertRepo:   stagedConcertRepo,
		rejectedConcertRepo: rejectedConcertRepo,
		venueRepo:           venueRepo,
		seriesRepo:          seriesRepo,
		concertRepo:         concertRepo,
		artistRepo:          artistRepo,
		publisher:           publisher,
		logger:              logger,
	}
}

// Approve promotes a pending staged concert to a published event.
func (uc *concertApprovalUseCase) Approve(ctx context.Context, stagedID string) error {
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

	uc.logger.Info(ctx, "staged concert approved and published",
		slog.String("artist_id", sc.ArtistID),
		slog.String("staged_concert_id", stagedID),
		slog.Int("inserted", len(insertedIDs)),
	)

	// Publish CONCERT.created only when at least one event was genuinely inserted.
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

	// Delete the staged row.
	if err := uc.stagedConcertRepo.Delete(ctx, stagedID); err != nil {
		return fmt.Errorf("delete staged concert after approval: %w", err)
	}

	return nil
}

// Reject records the staged concert in the rejection log and deletes the
// staged row.
func (uc *concertApprovalUseCase) Reject(ctx context.Context, stagedID string, reason string, reviewedBy string) error {
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

// resolveOrCreateVenue finds an existing venues row by place_id (or listed
// name when unresolved) or creates a new one from the staged resolved fields.
// Returns the venues.id to use for event insertion.
func (uc *concertApprovalUseCase) resolveOrCreateVenue(ctx context.Context, sc *entity.StagedConcert) (string, error) {
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
		// Not found — create a new venues row from the staged resolved fields.
		return uc.createVenueFromStaged(ctx, sc)
	}

	// Step 2: unresolved venue — try to find by listed name.
	existing, err := uc.venueRepo.GetByListedName(ctx, sc.ListedVenueName, sc.AdminArea)
	if err == nil {
		return existing.ID, nil
	}
	if !errors.Is(err, apperr.ErrNotFound) {
		return "", fmt.Errorf("get venue by listed name: %w", err)
	}

	// No existing venue found and no resolved place ID — create a minimal
	// venues row with only the raw listed name.
	return uc.createVenueFromStaged(ctx, sc)
}

// createVenueFromStaged creates a new venues row from the denormalised fields
// on the staged concert row.
func (uc *concertApprovalUseCase) createVenueFromStaged(ctx context.Context, sc *entity.StagedConcert) (string, error) {
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
		AdminArea:       sc.ResolvedAdminArea,
		GooglePlaceID:   sc.ResolvedPlaceID,
		ListedVenueName: &sc.ListedVenueName,
	}
	if sc.ResolvedLatitude != nil && sc.ResolvedLongitude != nil {
		venue.Coordinates = &entity.Coordinates{
			Latitude:  *sc.ResolvedLatitude,
			Longitude: *sc.ResolvedLongitude,
		}
	}

	if err := uc.venueRepo.Create(ctx, venue); err != nil {
		return "", fmt.Errorf("create venue from staged concert: %w", err)
	}

	uc.logger.Info(ctx, "created venue from staged concert",
		slog.String("venue_id", venue.ID),
		slog.String("venue_name", name),
	)

	return venue.ID, nil
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
