package event

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
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
	stocks          ticketStockReader
	logger          *logging.Logger
}

// ticketStockReader reads the TicketStock of a paid Order, whose event the
// Order does not carry (TicketStock.Get).
type ticketStockReader interface {
	Get(ctx context.Context, id entity.TicketStockID, at time.Time) (*entity.TicketStock, error)
}

// NewOrderPaidConsumer creates a new OrderPaidConsumer.
func NewOrderPaidConsumer(
	notificationUC usecase.NotificationUseCase,
	ticketJourneyUC usecase.TicketJourneyUseCase,
	stocks ticketStockReader,
	logger *logging.Logger,
) *OrderPaidConsumer {
	return &OrderPaidConsumer{notificationUC: notificationUC, ticketJourneyUC: ticketJourneyUC, stocks: stocks, logger: logger}
}

// parse reads the ORDER.paid announcement: the paid Order, with the event of
// its TicketStock read by id, or the JSON of one published before the outbox.
func (h *OrderPaidConsumer) parse(msg *message.Message) (entity.OrderPaidData, error) {
	data, err := h.decode(msg)
	if err != nil {
		h.logger.Error(msg.Context(), "failed to parse ORDER.paid event", err)
		return data, fmt.Errorf("parse ORDER.paid event: %w", err)
	}
	return data, nil
}

func (h *OrderPaidConsumer) decode(msg *message.Message) (entity.OrderPaidData, error) {
	if !messaging.IsProtobuf(msg) {
		var data entity.OrderPaidData
		err := messaging.ParseCloudEventData(msg, &data)
		return data, err
	}
	var order entityv1.Order
	if err := messaging.ParseProtobufData(msg, &order); err != nil {
		return entity.OrderPaidData{}, err
	}
	data := entity.OrderPaidData{
		OrderID:     order.GetId().GetValue(),
		UserID:      order.GetUserId().GetValue(),
		TicketCount: int(order.GetTicketCount()),
		Amount:      order.GetAmount(),
		Currency:    order.GetCurrency(),
	}
	stockID := order.GetTicketStockId().GetValue()
	if data.OrderID == "" || stockID == "" {
		return entity.OrderPaidData{}, errors.New("the Order has no id or no TicketStock")
	}
	stock, err := h.stocks.Get(msg.Context(), entity.TicketStockID(stockID), time.Now())
	if err != nil {
		return entity.OrderPaidData{}, fmt.Errorf("read the TicketStock %s of order %s: %w", stockID, data.OrderID, err)
	}
	data.EventID = stock.EventID
	return data, nil
}

// HandleSendOrderConfirmation sends the fan's confirmation email and push.
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

// HandleMarkPaid sets the fan's ticket journey for the event to Paid.
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
