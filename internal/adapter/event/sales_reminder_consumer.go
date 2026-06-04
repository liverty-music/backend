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

// SalesReminderConsumer handles SALES_PHASE.reminder.due events by delegating
// to the delivery use case. It is a thin adapter: parse the CloudEvent and hand
// off to the use case — no repository lookups or business logic here.
type SalesReminderConsumer struct {
	deliveryUC usecase.SalesReminderDeliveryUseCase
	logger     *logging.Logger
}

// NewSalesReminderConsumer creates a new SalesReminderConsumer.
func NewSalesReminderConsumer(
	deliveryUC usecase.SalesReminderDeliveryUseCase,
	logger *logging.Logger,
) *SalesReminderConsumer {
	return &SalesReminderConsumer{
		deliveryUC: deliveryUC,
		logger:     logger,
	}
}

// Handle processes a SALES_PHASE.reminder.due event by delegating to the delivery use case.
func (h *SalesReminderConsumer) Handle(msg *message.Message) error {
	ctx := msg.Context()

	var data entity.SalesPhaseReminderDueData
	if err := messaging.ParseCloudEventData(msg, &data); err != nil {
		h.logger.Error(ctx, "sales_reminder_consumer: failed to parse event", err)
		return fmt.Errorf("parse SALES_PHASE.reminder.due: %w", err)
	}

	h.logger.Info(ctx, "sales_reminder_consumer: processing",
		slog.String("user_id", data.UserID),
		slog.String("phase_id", data.PhaseID),
		slog.Int("stage", int(data.Stage)),
	)

	if err := h.deliveryUC.DeliverReminder(ctx, data); err != nil {
		return fmt.Errorf("sales_reminder_consumer: deliver reminder: %w", err)
	}
	return nil
}
