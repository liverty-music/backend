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

// ConcertCreationUseCase defines the interface for processing discovered concert
// batches. It resolves venues, persists concerts, and publishes downstream events.
type ConcertCreationUseCase interface {
	// CreateFromDiscovered processes a batch of scraped concerts for a single artist.
	// For each concert it resolves a venue via Google Places API, builds concert
	// entities, bulk-inserts them, and publishes a concert.created.v1 event.
	// Concerts whose venues cannot be resolved are skipped with a structured log.
	CreateFromDiscovered(ctx context.Context, data entity.ConcertDiscoveredData) error
}

// concertCreationUseCase implements ConcertCreationUseCase.
type concertCreationUseCase struct {
	venueRepo     entity.VenueRepository
	concertRepo   entity.ConcertRepository
	placeSearcher entity.VenuePlaceSearcher
	publisher     EventPublisher
	logger        *logging.Logger
}

// Compile-time interface compliance check.
var _ ConcertCreationUseCase = (*concertCreationUseCase)(nil)

// NewConcertCreationUseCase creates a new ConcertCreationUseCase.
// placeSearcher must not be nil; panics if not provided.
func NewConcertCreationUseCase(
	venueRepo entity.VenueRepository,
	concertRepo entity.ConcertRepository,
	placeSearcher entity.VenuePlaceSearcher,
	publisher EventPublisher,
	logger *logging.Logger,
) ConcertCreationUseCase {
	if placeSearcher == nil {
		panic("placeSearcher is required")
	}
	return &concertCreationUseCase{
		venueRepo:     venueRepo,
		concertRepo:   concertRepo,
		placeSearcher: placeSearcher,
		publisher:     publisher,
		logger:        logger,
	}
}

// CreateFromDiscovered processes a discovered concert batch: resolves venues,
// persists concerts, and publishes downstream events.
func (uc *concertCreationUseCase) CreateFromDiscovered(ctx context.Context, data entity.ConcertDiscoveredData) error {
	// Resolve or create venues for each scraped concert, then build Concert entities.
	var concerts []*entity.Concert
	newVenues := make(map[string]*entity.Venue) // track newly created venues by cache key

	for _, sc := range data.Concerts {
		venueID, venue, skip, err := uc.resolveVenue(ctx, sc.ListedVenueName, sc.AdminArea, newVenues)
		if err != nil {
			return fmt.Errorf("resolve venue %q: %w", sc.ListedVenueName, err)
		}
		if skip {
			uc.logger.Warn(ctx, "skipping concert: venue not found in Google Places",
				slog.String("artist_id", data.ArtistID),
				slog.String("title", sc.Title),
				slog.String("listed_venue_name", sc.ListedVenueName),
				slog.Any("admin_area", sc.AdminArea),
				slog.String("local_date", sc.LocalDate.Format("2006-01-02")),
				slog.Any("start_time", sc.StartTime),
				slog.Any("open_time", sc.OpenTime),
				slog.String("source_url", sc.SourceURL),
			)
			continue
		}

		if venue != nil {
			newVenues[*venue.GooglePlaceID] = venue
		}

		id, err := uuid.NewV7()
		if err != nil {
			return fmt.Errorf("generate concert ID: %w", err)
		}

		concerts = append(concerts, sc.ToConcert(data.ArtistID, id.String(), venueID))
	}

	// Bulk insert concerts (ON CONFLICT DO NOTHING handles duplicates).
	if len(concerts) > 0 {
		if err := uc.concertRepo.Create(ctx, concerts...); err != nil {
			return fmt.Errorf("create concerts: %w", err)
		}
	}

	uc.logger.Info(ctx, "concerts persisted",
		slog.String("artist_id", data.ArtistID),
		slog.Int("count", len(concerts)),
	)

	// Publish concert.created.v1 for downstream notification handler.
	createdData := entity.ConcertCreatedData{
		ArtistID:     data.ArtistID,
		ArtistName:   data.ArtistName,
		ConcertCount: len(concerts),
	}
	if err := uc.publishEvent(ctx, entity.SubjectConcertCreated, createdData); err != nil {
		uc.logger.Error(ctx, "failed to publish concert.created event", err,
			slog.String("artist_id", data.ArtistID),
		)
		// Non-fatal: concerts are already persisted.
	}

	return nil
}

// resolveVenue resolves a venue for a scraped concert via Google Places API.
//
// Resolution strategy:
//  1. Call Google Places API to get canonical place_id.
//  2. Check batch-local cache by place_id.
//  3. Look up existing venue by google_place_id in the database.
//  4. If not found, create a new venue from the Places result.
//
// Returns skip=true when Places API returns NotFound, signalling the caller
// to skip the concert. Non-nil *Venue is returned only when a new venue was created.
func (uc *concertCreationUseCase) resolveVenue(
	ctx context.Context,
	name string,
	adminArea *string,
	newVenues map[string]*entity.Venue,
) (string, *entity.Venue, bool, error) {
	area := ""
	if adminArea != nil {
		area = *adminArea
	}

	place, err := uc.placeSearcher.SearchPlace(ctx, name, area)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return "", nil, true, nil
		}
		return "", nil, false, fmt.Errorf("search place %q: %w", name, err)
	}

	// Check batch-local cache by place_id.
	if v, ok := newVenues[place.ExternalID]; ok {
		return v.ID, nil, false, nil
	}

	// Look up existing venue by google_place_id.
	existing, err := uc.venueRepo.GetByPlaceID(ctx, place.ExternalID)
	if err == nil {
		return existing.ID, nil, false, nil
	}
	if !errors.Is(err, apperr.ErrNotFound) {
		return "", nil, false, fmt.Errorf("get venue by place ID: %w", err)
	}

	// Create new venue from Places API result.
	id, venue, err := uc.createVenueFromPlace(ctx, name, adminArea, place)
	if err != nil {
		return "", nil, false, err
	}
	return id, venue, false, nil
}

// createVenueFromPlace creates a new venue using canonical data from the Google Places API.
func (uc *concertCreationUseCase) createVenueFromPlace(
	ctx context.Context,
	listedName string,
	adminArea *string,
	place *entity.VenuePlace,
) (string, *entity.Venue, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", nil, fmt.Errorf("generate venue ID: %w", err)
	}

	venue := &entity.Venue{
		ID:            id.String(),
		Name:          place.Name,
		AdminArea:     adminArea,
		GooglePlaceID: &place.ExternalID,
		Coordinates:   place.Coordinates,
	}

	if err := uc.venueRepo.Create(ctx, venue); err != nil {
		return "", nil, fmt.Errorf("create venue: %w", err)
	}

	uc.logger.Info(ctx, "created new venue from Google Places",
		slog.String("venue_id", venue.ID),
		slog.String("venue_name", place.Name),
		slog.String("raw_name", listedName),
		slog.String("place_id", place.ExternalID),
	)

	return venue.ID, venue, nil
}

// publishEvent publishes data as a CloudEvent to the given subject.
func (uc *concertCreationUseCase) publishEvent(ctx context.Context, subject string, data any) error {
	return uc.publisher.PublishEvent(ctx, subject, data)
}
