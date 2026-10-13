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

// TicketSaleReminderConsumer handles SALES_PHASE.reminder.due events by delegating
// to the delivery use case. It is a thin adapter: parse the CloudEvent and hand
// off to the use case — no repository lookups or business logic here.
type TicketSaleReminderConsumer struct {
	deliveryUC usecase.TicketSaleReminderDeliveryUseCase
	logger     *logging.Logger
}

// NewTicketSaleReminderConsumer creates a new TicketSaleReminderConsumer.
func NewTicketSaleReminderConsumer(
	deliveryUC usecase.TicketSaleReminderDeliveryUseCase,
	logger *logging.Logger,
) *TicketSaleReminderConsumer {
	return &TicketSaleReminderConsumer{
		deliveryUC: deliveryUC,
		logger:     logger,
	}
}

// Handle processes a SALES_PHASE.reminder.due event by delegating to the delivery use case.
func (h *TicketSaleReminderConsumer) Handle(msg *message.Message) error {
	ctx := msg.Context()

	var data entity.TicketSaleReminderDueData
	if err := messaging.ParseCloudEventData(msg, &data); err != nil {
		h.logger.Error(ctx, "ticket_sale_reminder_consumer: failed to parse event", err)
		return fmt.Errorf("parse SALES_PHASE.reminder.due: %w", err)
	}

	h.logger.Info(ctx, "ticket_sale_reminder_consumer: processing",
		slog.String("user_id", data.UserID),
		slog.String("ticket_sale_id", data.TicketSaleID),
		slog.Int("stage", int(data.Stage)),
	)

	if err := h.deliveryUC.DeliverReminder(ctx, data); err != nil {
		return fmt.Errorf("ticket_sale_reminder_consumer: deliver reminder: %w", err)
	}
	return nil
}
