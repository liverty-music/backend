package event

import (
	"errors"
	"fmt"
	"log/slog"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/messaging"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-logging/logging"
)

// CancellationNoticeConsumer handles EVENT.cancelled, which
// Series.CancelEvents records once per cancelled Event, and ORDER.refunded,
// which Order.CommitRefund records once per refunded Order, each with its own
// durable. Each only decodes the announcement and starts the notices; the
// notice workflows are named after the Order, so a redelivery starts nothing
// new, and the workflow worker sends them.
type CancellationNoticeConsumer struct {
	notificationUC usecase.NotificationUseCase
	logger         *logging.Logger
}

// NewCancellationNoticeConsumer creates a new CancellationNoticeConsumer.
func NewCancellationNoticeConsumer(notificationUC usecase.NotificationUseCase, logger *logging.Logger) *CancellationNoticeConsumer {
	return &CancellationNoticeConsumer{notificationUC: notificationUC, logger: logger}
}

// HandleEventCancelled starts the cancellation notice of each Paid Order of
// the cancelled Event.
func (h *CancellationNoticeConsumer) HandleEventCancelled(msg *message.Message) error {
	var e entityv1.Event
	if err := messaging.ParseProtobufData(msg, &e); err != nil {
		h.logger.Error(msg.Context(), "failed to parse EVENT.cancelled event", err)
		return fmt.Errorf("parse %s event: %w", entity.SubjectEventCancelled, err)
	}
	eventID := e.GetId().GetValue()
	if eventID == "" {
		err := errors.New("the Event has no id")
		h.logger.Error(msg.Context(), "failed to parse EVENT.cancelled event", err)
		return err
	}
	h.logger.Info(msg.Context(), "processing EVENT.cancelled", slog.String("event_id", eventID))
	if err := h.notificationUC.SendEventCancelled(msg.Context(), eventID); err != nil {
		return fmt.Errorf("start the cancellation notices of event %s: %w", eventID, err)
	}
	return nil
}

// HandleOrderRefunded starts the refund notice of the refunded Order.
func (h *CancellationNoticeConsumer) HandleOrderRefunded(msg *message.Message) error {
	var o entityv1.Order
	if err := messaging.ParseProtobufData(msg, &o); err != nil {
		h.logger.Error(msg.Context(), "failed to parse ORDER.refunded event", err)
		return fmt.Errorf("parse %s event: %w", entity.SubjectOrderRefunded, err)
	}
	orderID := entity.OrderID(o.GetId().GetValue())
	if orderID == "" {
		err := errors.New("the Order has no id")
		h.logger.Error(msg.Context(), "failed to parse ORDER.refunded event", err)
		return err
	}
	h.logger.Info(msg.Context(), "processing ORDER.refunded", slog.String("order_id", string(orderID)))
	if err := h.notificationUC.SendOrderRefunded(msg.Context(), orderID); err != nil {
		return fmt.Errorf("start the refund notice of order %s: %w", orderID, err)
	}
	return nil
}
