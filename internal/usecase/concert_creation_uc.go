package usecase

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/google/uuid"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/messaging"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-logging/logging"
)

// ConcertCreationUseCase defines the interface for processing discovered concert
// batches. It resolves venues, persists concerts, and publishes downstream events.
type ConcertCreationUseCase interface {
	// CreateFromDiscovered processes a batch of scraped concerts for a single artist.
	// For each concert it resolves or creates a venue, builds concert entities,
	// bulk-inserts them, and publishes concert.created.v1 and venue.created.v1 events.
	CreateFromDiscovered(ctx context.Context, data entity.ConcertDiscoveredData) error
}

// concertCreationUseCase implements ConcertCreationUseCase.
type concertCreationUseCase struct {
	venueRepo     entity.VenueRepository
	concertRepo   entity.ConcertRepository
	placeSearcher entity.VenuePlaceSearcher
	publisher     message.Publisher
	logger        *logging.Logger
}

// Compile-time interface compliance check.
var _ ConcertCreationUseCase = (*concertCreationUseCase)(nil)

// NewConcertCreationUseCase creates a new ConcertCreationUseCase.
// placeSearcher may be nil when Google Places API is not configured (e.g., local dev).
func NewConcertCreationUseCase(
	venueRepo entity.VenueRepository,
	concertRepo entity.ConcertRepository,
	placeSearcher entity.VenuePlaceSearcher,
	publisher message.Publisher,
	logger *logging.Logger,
) ConcertCreationUseCase {
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
		venueID, venue, err := uc.resolveVenue(ctx, sc.ListedVenueName, sc.AdminArea, newVenues)
		if err != nil {
			return fmt.Errorf("resolve venue %q: %w", sc.ListedVenueName, err)
		}

		if venue != nil {
			// Cache by place_id if available, otherwise by listed name.
			cacheKey := sc.ListedVenueName
			if venue.GooglePlaceID != nil {
				cacheKey = *venue.GooglePlaceID
			}
			newVenues[cacheKey] = venue
		}

		id, err := uuid.NewV7()
		if err != nil {
			return fmt.Errorf("generate concert ID: %w", err)
		}

		listedName := sc.ListedVenueName
		concerts = append(concerts, &entity.Concert{
			Event: entity.Event{
				ID:              id.String(),
				VenueID:         venueID,
				Title:           sc.Title,
				ListedVenueName: &listedName,
				LocalDate:       sc.LocalDate,
				StartTime:       sc.StartTime,
				OpenTime:        sc.OpenTime,
				SourceURL:       sc.SourceURL,
			},
			ArtistID: data.ArtistID,
		})
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

	// Publish venue.created.v1 for each newly created venue that needs enrichment.
	for _, v := range newVenues {
		if v.EnrichmentStatus == entity.EnrichmentStatusEnriched {
			continue // Already enriched via Google Places at creation time.
		}
		venueData := entity.VenueCreatedData{
			VenueID:   v.ID,
			Name:      v.Name,
			AdminArea: v.AdminArea,
		}
		if err := uc.publishEvent(ctx, entity.SubjectVenueCreated, venueData); err != nil {
			uc.logger.Error(ctx, "failed to publish venue.created event", err,
				slog.String("venue_id", v.ID),
			)
			// Non-fatal: venue enrichment will pick up pending venues on next batch.
		}
	}

	return nil
}

// resolveVenue resolves a venue for a scraped concert.
//
// Resolution strategy:
//  1. Check batch-local cache (by place_id or name).
//  2. Call Google Places API to get canonical place_id (if configured).
//  3. If Places API returns a result, look up venue by google_place_id.
//  4. If not found by place_id, create a new enriched venue from the Places result.
//  5. On Places API failure/not-found, fall back to GetByName lookup.
//  6. If no venue found at all, create a new venue with pending enrichment.
//
// Returns the venue ID and a non-nil *Venue only when a new venue was created.
func (uc *concertCreationUseCase) resolveVenue(
	ctx context.Context,
	name string,
	adminArea *string,
	newVenues map[string]*entity.Venue,
) (string, *entity.Venue, error) {
	// Try Google Places API first (if configured).
	if uc.placeSearcher != nil {
		area := ""
		if adminArea != nil {
			area = *adminArea
		}

		place, err := uc.placeSearcher.SearchPlace(ctx, name, area)
		if err == nil {
			// Check batch-local cache by place_id.
			if v, ok := newVenues[place.ExternalID]; ok {
				return v.ID, nil, nil
			}

			// Look up existing venue by google_place_id.
			existing, err := uc.venueRepo.GetByPlaceID(ctx, place.ExternalID)
			if err == nil {
				return existing.ID, nil, nil
			}
			if !errors.Is(err, apperr.ErrNotFound) {
				return "", nil, fmt.Errorf("get venue by place ID: %w", err)
			}

			// Create new venue from Places API result (already enriched).
			return uc.createVenueFromPlace(ctx, name, adminArea, place)
		}

		// Log and fall through to name-based lookup on failure.
		if !errors.Is(err, apperr.ErrNotFound) {
			uc.logger.Warn(ctx, "Google Places API error, falling back to name lookup",
				slog.String("venue_name", name),
				slog.Any("error", err),
			)
		}
	}

	// Fallback: check batch-local cache by name.
	if v, ok := newVenues[name]; ok {
		return v.ID, nil, nil
	}

	// Fallback: look up existing venue by name.
	existing, err := uc.venueRepo.GetByName(ctx, name)
	if err == nil {
		return existing.ID, nil, nil
	}
	if !errors.Is(err, apperr.ErrNotFound) {
		return "", nil, fmt.Errorf("get venue by name: %w", err)
	}

	// Create a new venue with pending enrichment status.
	return uc.createVenuePending(ctx, name, adminArea)
}

// createVenueFromPlace creates a new venue using canonical data from the Google Places API.
func (uc *concertCreationUseCase) createVenueFromPlace(
	ctx context.Context,
	rawName string,
	adminArea *string,
	place *entity.VenuePlace,
) (string, *entity.Venue, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", nil, fmt.Errorf("generate venue ID: %w", err)
	}

	venue := &entity.Venue{
		ID:               id.String(),
		Name:             place.Name,
		AdminArea:        adminArea,
		GooglePlaceID:    &place.ExternalID,
		Coordinates:      place.Coordinates,
		EnrichmentStatus: entity.EnrichmentStatusEnriched,
		RawName:          rawName,
	}

	if err := uc.venueRepo.Create(ctx, venue); err != nil {
		return "", nil, fmt.Errorf("create venue: %w", err)
	}

	uc.logger.Info(ctx, "created new venue from Google Places",
		slog.String("venue_id", venue.ID),
		slog.String("venue_name", place.Name),
		slog.String("raw_name", rawName),
		slog.String("place_id", place.ExternalID),
	)

	return venue.ID, venue, nil
}

// createVenuePending creates a new venue with pending enrichment status.
func (uc *concertCreationUseCase) createVenuePending(
	ctx context.Context,
	name string,
	adminArea *string,
) (string, *entity.Venue, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", nil, fmt.Errorf("generate venue ID: %w", err)
	}

	venue := &entity.Venue{
		ID:               id.String(),
		Name:             name,
		AdminArea:        adminArea,
		EnrichmentStatus: entity.EnrichmentStatusPending,
		RawName:          name,
	}

	if err := uc.venueRepo.Create(ctx, venue); err != nil {
		return "", nil, fmt.Errorf("create venue: %w", err)
	}

	uc.logger.Info(ctx, "created new venue (pending enrichment)",
		slog.String("venue_id", venue.ID),
		slog.String("venue_name", name),
	)

	return venue.ID, venue, nil
}

// publishEvent creates an event message and publishes it to the given subject.
func (uc *concertCreationUseCase) publishEvent(ctx context.Context, subject string, data any) error {
	msg, err := messaging.NewEvent(ctx, data)
	if err != nil {
		return fmt.Errorf("create %s event: %w", subject, err)
	}
	return uc.publisher.Publish(subject, msg)
}
