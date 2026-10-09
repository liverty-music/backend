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

// OrderPaidConsumer handles ORDER.paid events with two independent durables:
// the purchase confirmation (email then push) and the ticket journey update.
// Each is idempotent by the Order id, so a redelivery sends no second email
// and leaves the journey Paid. A returned error makes the router redeliver,
// and after the redelivery limit the message goes to the poison queue.
type OrderPaidConsumer struct {
	notificationUC  usecase.NotificationUseCase
	ticketJourneyUC usecase.TicketJourneyUseCase
	logger          *logging.Logger
}

// NewOrderPaidConsumer creates a new OrderPaidConsumer.
func NewOrderPaidConsumer(
	notificationUC usecase.NotificationUseCase,
	ticketJourneyUC usecase.TicketJourneyUseCase,
	logger *logging.Logger,
) *OrderPaidConsumer {
	return &OrderPaidConsumer{notificationUC: notificationUC, ticketJourneyUC: ticketJourneyUC, logger: logger}
}

// parse reads the ORDER.paid payload.
func (h *OrderPaidConsumer) parse(msg *message.Message) (entity.OrderPaidData, error) {
	var data entity.OrderPaidData
	if err := messaging.ParseCloudEventData(msg, &data); err != nil {
		h.logger.Error(msg.Context(), "failed to parse ORDER.paid event", err)
		return data, fmt.Errorf("parse ORDER.paid event: %w", err)
	}
	return data, nil
}

// HandleSendOrderConfirmation sends the buyer's confirmation email and push.
func (h *OrderPaidConsumer) HandleSendOrderConfirmation(msg *message.Message) error {
	data, err := h.parse(msg)
	if err != nil {
		return err
	}
	h.logger.Info(msg.Context(), "processing ORDER.paid for the order confirmation",
		slog.String("order_id", data.OrderID))
	if err := h.notificationUC.SendOrderConfirmation(msg.Context(), data); err != nil {
		return fmt.Errorf("send order confirmation for order %s: %w", data.OrderID, err)
	}
	return nil
}

// HandleMarkPaid sets the buyer's ticket journey for the event to Paid.
func (h *OrderPaidConsumer) HandleMarkPaid(msg *message.Message) error {
	data, err := h.parse(msg)
	if err != nil {
		return err
	}
	if err := h.ticketJourneyUC.MarkPaid(msg.Context(), data); err != nil {
		return fmt.Errorf("mark ticket journey paid for order %s: %w", data.OrderID, err)
	}
	return nil
}
