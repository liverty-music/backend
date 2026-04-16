package event

import (
	"fmt"
	"log/slog"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/liverty-music/backend/internal/infrastructure/messaging"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-logging/logging"
)

// NotificationConsumer handles concert.created.v1 events by delegating to the
// push notification use case. It is a thin adapter: parse the CloudEvent and
// hand off to the use case — no repository lookups or business logic here.
type NotificationConsumer struct {
	pushNotificationUC usecase.PushNotificationUseCase
	logger             *logging.Logger
}

// NewNotificationConsumer creates a new NotificationConsumer.
func NewNotificationConsumer(
	pushNotificationUC usecase.PushNotificationUseCase,
	logger *logging.Logger,
) *NotificationConsumer {
	return &NotificationConsumer{
		pushNotificationUC: pushNotificationUC,
		logger:             logger,
	}
}

// Handle processes a concert.created.v1 event by notifying all followers of the artist.
func (h *NotificationConsumer) Handle(msg *message.Message) error {
	ctx := msg.Context()

	var data usecase.ConcertCreatedData
	if err := messaging.ParseCloudEventData(msg, &data); err != nil {
		h.logger.Error(ctx, "failed to parse concert.created event", err)
		return fmt.Errorf("parse concert.created event: %w", err)
	}

	h.logger.Info(ctx, "processing concert.created event for notifications",
		slog.String("artist_id", data.ArtistID),
		slog.Int("concert_count", len(data.ConcertIDs)),
	)

	if err := h.pushNotificationUC.NotifyNewConcerts(ctx, data); err != nil {
		return fmt.Errorf("notify for artist %s: %w", data.ArtistID, err)
	}

	return nil
}
