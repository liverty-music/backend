package event

import (
	"errors"
	"fmt"
	"log/slog"
	"time"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-logging/logging"

	"github.com/liverty-music/backend/internal/adapter/pb"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/messaging"
	"github.com/liverty-music/backend/internal/usecase"
)

// ResaleEntryChangedConsumer handles ORDER.resale_entry_changed events, which
// ResaleEntry.PersistDrawOutcome, ResaleEntry.Withdraw and
// ResaleEntry.RevertWin record, with two independent durables: the start of
// the workflow that charges a Won entry or releases an ended one (the
// workflow starter, design D4), and the ended email and push of a Lapsed or
// Lost entry (design D6). The workflow starter is guarded by the workflow
// named after the entry's Order, the ended message by the entry's ended
// email send time and a Deliver named after the type and the entry, so a
// redelivery starts and sends nothing twice. Each durable has its own cursor,
// so a mail outage never delays a release.
type ResaleEntryChangedConsumer struct {
	orderUC        usecase.OrderUseCase
	notificationUC usecase.NotificationUseCase
	logger         *logging.Logger
}

// endedEmailRetryDelay is how long an ended message waits for the mail
// service after it was unavailable.
const endedEmailRetryDelay = time.Minute

// NewResaleEntryChangedConsumer creates a new ResaleEntryChangedConsumer.
func NewResaleEntryChangedConsumer(orderUC usecase.OrderUseCase, notificationUC usecase.NotificationUseCase, logger *logging.Logger) *ResaleEntryChangedConsumer {
	return &ResaleEntryChangedConsumer{orderUC: orderUC, notificationUC: notificationUC, logger: logger}
}

// parse reads the ORDER.resale_entry_changed announcement: the entry as it is
// after the change.
func (h *ResaleEntryChangedConsumer) parse(msg *message.Message) (*entity.ResaleEntry, error) {
	var e entityv1.ResaleEntry
	if err := messaging.ParseProtobufData(msg, &e); err != nil {
		h.logger.Error(msg.Context(), "failed to parse ORDER.resale_entry_changed event", err)
		return nil, fmt.Errorf("parse ORDER.resale_entry_changed event: %w", err)
	}
	entry := pb.ToResaleEntry(&e)
	if entry.ID == "" || entry.OrderID == "" {
		err := errors.New("the ResaleEntry has no id or no Order")
		h.logger.Error(msg.Context(), "failed to parse ORDER.resale_entry_changed event", err)
		return nil, err
	}
	return entry, nil
}

// HandleStartWorkflow starts the issue workflow of a Won entry's Order and
// the release workflow of an ended entry's Order.
func (h *ResaleEntryChangedConsumer) HandleStartWorkflow(msg *message.Message) error {
	entry, err := h.parse(msg)
	if err != nil {
		return err
	}
	h.logger.Info(msg.Context(), "processing ORDER.resale_entry_changed for its workflow",
		slog.String("resale_entry_id", string(entry.ID)),
		slog.String("state", entry.State.String()))
	if err := h.orderUC.StartResaleEntryWorkflow(msg.Context(), entry); err != nil {
		return fmt.Errorf("start the workflow of resale entry %s: %w", entry.ID, err)
	}
	return nil
}

// HandleSendResaleEntryEnded sends a Lapsed or Lost entry's ended email and
// push. While the mail service is unavailable the message is delivered again
// after endedEmailRetryDelay, as often as it takes: the entry is announced
// once, so this delivery is the only way the fan is told.
func (h *ResaleEntryChangedConsumer) HandleSendResaleEntryEnded(msg *message.Message) error {
	entry, err := h.parse(msg)
	if err != nil {
		return err
	}
	if entry.State != entity.ResaleEntryStateLapsed && entry.State != entity.ResaleEntryStateLost {
		return nil
	}
	if err := h.notificationUC.SendResaleEntryEnded(msg.Context(), entry.ID); err != nil {
		if errors.Is(err, apperr.ErrUnavailable) || errors.Is(err, apperr.ErrDeadlineExceeded) {
			return messaging.RedeliverLater(msg, err, endedEmailRetryDelay)
		}
		return fmt.Errorf("send the ended notice of resale entry %s: %w", entry.ID, err)
	}
	return nil
}

// ResaleListingSoldConsumer handles ORDER.resale_listing_sold events, which
// Order.Issue records for each listing it sells: it starts the listing's
// refund workflow, named after the listing, so a redelivery starts nothing
// new (design D10).
type ResaleListingSoldConsumer struct {
	resaleListingUC usecase.ResaleListingUseCase
	logger          *logging.Logger
}

// NewResaleListingSoldConsumer creates a new ResaleListingSoldConsumer.
func NewResaleListingSoldConsumer(resaleListingUC usecase.ResaleListingUseCase, logger *logging.Logger) *ResaleListingSoldConsumer {
	return &ResaleListingSoldConsumer{resaleListingUC: resaleListingUC, logger: logger}
}

// HandleStartRefund starts the refund workflow of the sold listing.
func (h *ResaleListingSoldConsumer) HandleStartRefund(msg *message.Message) error {
	var l entityv1.ResaleListing
	if err := messaging.ParseProtobufData(msg, &l); err != nil {
		h.logger.Error(msg.Context(), "failed to parse ORDER.resale_listing_sold event", err)
		return fmt.Errorf("parse ORDER.resale_listing_sold event: %w", err)
	}
	id := entity.ResaleListingID(l.GetId().GetValue())
	if id == "" {
		err := errors.New("the ResaleListing has no id")
		h.logger.Error(msg.Context(), "failed to parse ORDER.resale_listing_sold event", err)
		return err
	}
	if err := h.resaleListingUC.StartRefundSold(msg.Context(), id); err != nil {
		return fmt.Errorf("start the refund of resale listing %s: %w", id, err)
	}
	return nil
}
