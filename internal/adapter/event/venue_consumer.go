package event

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/liverty-music/backend/internal/infrastructure/messaging"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-logging/logging"
)

// VenueConsumer handles venue.created.v1 events by triggering venue enrichment.
type VenueConsumer struct {
	venueEnrichUC usecase.VenueEnrichmentUseCase
	logger        *logging.Logger
}

// NewVenueConsumer creates a new VenueConsumer.
func NewVenueConsumer(
	venueEnrichUC usecase.VenueEnrichmentUseCase,
	logger *logging.Logger,
) *VenueConsumer {
	return &VenueConsumer{
		venueEnrichUC: venueEnrichUC,
		logger:        logger,
	}
}

// Handle processes a venue.created.v1 event by enriching the venue via
// external place services (MusicBrainz, Google Maps).
func (h *VenueConsumer) Handle(msg *message.Message) error {
	ctx := context.Background()

	var data messaging.VenueCreatedData
	if err := messaging.ParseCloudEventData(msg, &data); err != nil {
		h.logger.Error(ctx, "failed to parse venue.created event", err)
		return fmt.Errorf("parse venue.created event: %w", err)
	}

	h.logger.Info(ctx, "processing venue.created event for enrichment",
		slog.String("venue_id", data.VenueID),
		slog.String("venue_name", data.Name),
	)

	if err := h.venueEnrichUC.EnrichOne(ctx, data.VenueID); err != nil {
		return fmt.Errorf("enrich venue %s: %w", data.VenueID, err)
	}

	h.logger.Info(ctx, "venue enrichment completed",
		slog.String("venue_id", data.VenueID),
		slog.String("venue_name", data.Name),
	)

	return nil
}
