package event

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/messaging"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-logging/logging"
)

// NotificationConsumer handles concert.created.v1 events by sending push
// notifications to all followers of the artist.
type NotificationConsumer struct {
	artistRepo         entity.ArtistRepository
	concertRepo        entity.ConcertRepository
	pushNotificationUC usecase.PushNotificationUseCase
	logger             *logging.Logger
}

// NewNotificationConsumer creates a new NotificationConsumer.
func NewNotificationConsumer(
	artistRepo entity.ArtistRepository,
	concertRepo entity.ConcertRepository,
	pushNotificationUC usecase.PushNotificationUseCase,
	logger *logging.Logger,
) *NotificationConsumer {
	return &NotificationConsumer{
		artistRepo:         artistRepo,
		concertRepo:        concertRepo,
		pushNotificationUC: pushNotificationUC,
		logger:             logger,
	}
}

// Handle processes a concert.created.v1 event by notifying all followers of the artist.
func (h *NotificationConsumer) Handle(msg *message.Message) error {
	ctx := context.Background()

	var data messaging.ConcertCreatedData
	if err := messaging.ParseCloudEventData(msg, &data); err != nil {
		h.logger.Error(ctx, "failed to parse concert.created event", err)
		return fmt.Errorf("parse concert.created event: %w", err)
	}

	h.logger.Info(ctx, "processing concert.created event for notifications",
		slog.String("artist_id", data.ArtistID),
		slog.String("artist_name", data.ArtistName),
		slog.Int("concert_count", data.ConcertCount),
	)

	// Get artist entity for notification context.
	artist, err := h.artistRepo.Get(ctx, data.ArtistID)
	if err != nil {
		return fmt.Errorf("get artist %s: %w", data.ArtistID, err)
	}

	// Get upcoming concerts for the notification payload.
	concerts, err := h.concertRepo.ListByArtist(ctx, data.ArtistID, true)
	if err != nil {
		return fmt.Errorf("list concerts for artist %s: %w", data.ArtistID, err)
	}

	if err := h.pushNotificationUC.NotifyNewConcerts(ctx, artist, concerts); err != nil {
		return fmt.Errorf("notify new concerts for artist %s: %w", data.ArtistID, err)
	}

	h.logger.Info(ctx, "notifications sent for artist",
		slog.String("artist_id", data.ArtistID),
		slog.String("artist_name", data.ArtistName),
	)

	return nil
}
