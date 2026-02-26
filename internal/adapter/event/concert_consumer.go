// Package event provides Watermill event consumers for the consumer process.
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

// ConcertConsumer handles concert.discovered.v1 events by delegating to
// ConcertCreationUseCase for venue resolution, concert persistence, and
// downstream event publishing.
type ConcertConsumer struct {
	concertCreationUC usecase.ConcertCreationUseCase
	logger            *logging.Logger
}

// NewConcertConsumer creates a new ConcertConsumer.
func NewConcertConsumer(
	concertCreationUC usecase.ConcertCreationUseCase,
	logger *logging.Logger,
) *ConcertConsumer {
	return &ConcertConsumer{
		concertCreationUC: concertCreationUC,
		logger:            logger,
	}
}

// Handle processes a concert.discovered.v1 event.
func (h *ConcertConsumer) Handle(msg *message.Message) error {
	ctx := context.Background()

	var data messaging.ConcertDiscoveredData
	if err := messaging.ParseCloudEventData(msg, &data); err != nil {
		h.logger.Error(ctx, "failed to parse concert.discovered event", err)
		return fmt.Errorf("parse concert.discovered event: %w", err)
	}

	h.logger.Info(ctx, "processing concert.discovered event",
		slog.String("artist_id", data.ArtistID),
		slog.String("artist_name", data.ArtistName),
		slog.Int("concert_count", len(data.Concerts)),
	)

	if err := h.concertCreationUC.CreateFromDiscovered(ctx, data); err != nil {
		return fmt.Errorf("create concerts from discovered event: %w", err)
	}

	return nil
}
