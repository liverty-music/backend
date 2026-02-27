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
	CreateFromDiscovered(ctx context.Context, data messaging.ConcertDiscoveredData) error
}

// concertCreationUseCase implements ConcertCreationUseCase.
type concertCreationUseCase struct {
	venueRepo   entity.VenueRepository
	concertRepo entity.ConcertRepository
	publisher   message.Publisher
	logger      *logging.Logger
}

// Compile-time interface compliance check.
var _ ConcertCreationUseCase = (*concertCreationUseCase)(nil)

// NewConcertCreationUseCase creates a new ConcertCreationUseCase.
func NewConcertCreationUseCase(
	venueRepo entity.VenueRepository,
	concertRepo entity.ConcertRepository,
	publisher message.Publisher,
	logger *logging.Logger,
) ConcertCreationUseCase {
	return &concertCreationUseCase{
		venueRepo:   venueRepo,
		concertRepo: concertRepo,
		publisher:   publisher,
		logger:      logger,
	}
}

// CreateFromDiscovered processes a discovered concert batch: resolves venues,
// persists concerts, and publishes downstream events.
func (uc *concertCreationUseCase) CreateFromDiscovered(ctx context.Context, data messaging.ConcertDiscoveredData) error {
	// Resolve or create venues for each scraped concert, then build Concert entities.
	var concerts []*entity.Concert
	newVenues := make(map[string]*entity.Venue) // track newly created venues by name

	for _, sc := range data.Concerts {
		venueID, venue, err := uc.resolveVenue(ctx, sc.ListedVenueName, sc.AdminArea, newVenues)
		if err != nil {
			return fmt.Errorf("resolve venue %q: %w", sc.ListedVenueName, err)
		}

		if venue != nil {
			newVenues[sc.ListedVenueName] = venue
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
	createdData := messaging.ConcertCreatedData{
		ArtistID:     data.ArtistID,
		ArtistName:   data.ArtistName,
		ConcertCount: len(concerts),
	}
	if err := uc.publishEvent(messaging.EventTypeConcertCreated, createdData); err != nil {
		uc.logger.Error(ctx, "failed to publish concert.created event", err,
			slog.String("artist_id", data.ArtistID),
		)
		// Non-fatal: concerts are already persisted.
	}

	// Publish venue.created.v1 for each newly created venue.
	for _, v := range newVenues {
		venueData := messaging.VenueCreatedData{
			VenueID:   v.ID,
			Name:      v.Name,
			AdminArea: v.AdminArea,
		}
		if err := uc.publishEvent(messaging.EventTypeVenueCreated, venueData); err != nil {
			uc.logger.Error(ctx, "failed to publish venue.created event", err,
				slog.String("venue_id", v.ID),
			)
			// Non-fatal: venue enrichment will pick up pending venues on next batch.
		}
	}

	return nil
}

// resolveVenue looks up an existing venue by name or creates a new one.
// It returns the venue ID and a non-nil *Venue only when a new venue was created.
// The newVenues map prevents creating duplicates within the same batch.
func (uc *concertCreationUseCase) resolveVenue(
	ctx context.Context,
	name string,
	adminArea *string,
	newVenues map[string]*entity.Venue,
) (string, *entity.Venue, error) {
	// Check batch-local cache first.
	if v, ok := newVenues[name]; ok {
		return v.ID, nil, nil
	}

	// Look up existing venue by name.
	existing, err := uc.venueRepo.GetByName(ctx, name)
	if err == nil {
		return existing.ID, nil, nil
	}
	if !errors.Is(err, apperr.ErrNotFound) {
		return "", nil, fmt.Errorf("get venue by name: %w", err)
	}

	// Create a new venue with pending enrichment status.
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

	uc.logger.Info(ctx, "created new venue",
		slog.String("venue_id", venue.ID),
		slog.String("venue_name", name),
	)

	return venue.ID, venue, nil
}

// publishEvent creates a CloudEvent message and publishes it to the given topic.
func (uc *concertCreationUseCase) publishEvent(eventType string, data any) error {
	msg, err := messaging.NewCloudEvent(eventType, data)
	if err != nil {
		return fmt.Errorf("create %s event: %w", eventType, err)
	}
	return uc.publisher.Publish(eventType, msg)
}
