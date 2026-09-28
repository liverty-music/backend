package event

import (
	"fmt"
	"log/slog"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/messaging"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-logging/logging"
)

// DeliverNotificationConsumer handles NOTIFICATION.requested events by
// delegating to NotificationUseCase.Deliver. It is a thin adapter: parse the
// CloudEvent and hand off to the use case — no repository lookups or business
// logic here. Every notification producer (PushNotificationUseCase.
// NotifyNewConcerts, SalesPhaseAnnouncementUseCase.AnnounceDiscoveredPhase)
// publishes to this subject instead of depending on NotificationUseCase directly.
type DeliverNotificationConsumer struct {
	notificationUC usecase.NotificationUseCase
	logger         *logging.Logger
}

// NewDeliverNotificationConsumer creates a new DeliverNotificationConsumer.
func NewDeliverNotificationConsumer(
	notificationUC usecase.NotificationUseCase,
	logger *logging.Logger,
) *DeliverNotificationConsumer {
	return &DeliverNotificationConsumer{
		notificationUC: notificationUC,
		logger:         logger,
	}
}

// Handle processes a NOTIFICATION.requested event. A Notification
// record-creation failure inside Deliver is returned so the router retries
// (and, if retries are exhausted, poison-queues and logs it); a failed push
// send is an outcome Deliver already recorded, not an error, so it does not
// reach here.
func (h *DeliverNotificationConsumer) Handle(msg *message.Message) error {
	ctx := msg.Context()

	var data entity.NotificationRequestedData
	if err := messaging.ParseCloudEventData(msg, &data); err != nil {
		h.logger.Error(ctx, "failed to parse NOTIFICATION.requested event", err)
		return fmt.Errorf("parse NOTIFICATION.requested event: %w", err)
	}

	h.logger.Info(ctx, "processing NOTIFICATION.requested event",
		slog.String("user_id", data.UserID),
		slog.String("type", string(data.Type)),
	)

	if _, err := h.notificationUC.Deliver(ctx, data.UserID, data.Type, data.Payload); err != nil {
		return fmt.Errorf("deliver notification to user %s: %w", data.UserID, err)
	}

	return nil
}
