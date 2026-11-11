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
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/liverty-music/backend/pkg/eventmsg"
)

// ResaleEntryConsumer handles RESALE_ENTRY.won, RESALE_ENTRY.lapsed,
// RESALE_ENTRY.lost and RESALE_ENTRY.withdrawn events, which
// ResaleEntry.PersistDrawOutcome, ResaleEntry.Withdraw and
// ResaleEntry.RevertWin record, with two independent durables: the start of
// the workflow that charges a Won entry or releases an ended one (all four
// subjects; the workflow starter, design D4), and the ended email and push
// of a Lapsed or Lost entry (those two subjects; design D6). The subject
// tells the entry's state. The workflow starter is guarded by the workflow
// named after the entry's Order, the ended message by the entry's ended
// email send time and a Deliver named after the type and the entry, so a
// redelivery starts and sends nothing twice. Each durable has its own cursor,
// so a mail outage never delays a release.
type ResaleEntryConsumer struct {
	orderUC        usecase.OrderUseCase
	notificationUC usecase.NotificationUseCase
	logger         *logging.Logger
}

// endedEmailRetryDelay is how long an ended message waits for the mail
// service after it was unavailable.
const endedEmailRetryDelay = time.Minute

// NewResaleEntryConsumer creates a new ResaleEntryConsumer.
func NewResaleEntryConsumer(orderUC usecase.OrderUseCase, notificationUC usecase.NotificationUseCase, logger *logging.Logger) *ResaleEntryConsumer {
	return &ResaleEntryConsumer{orderUC: orderUC, notificationUC: notificationUC, logger: logger}
}

// resaleEntryStates is the state each RESALE_ENTRY subject announces.
var resaleEntryStates = map[string]entity.ResaleEntryState{
	entity.SubjectResaleEntryWon:       entity.ResaleEntryStateWon,
	entity.SubjectResaleEntryLapsed:    entity.ResaleEntryStateLapsed,
	entity.SubjectResaleEntryLost:      entity.ResaleEntryStateLost,
	entity.SubjectResaleEntryWithdrawn: entity.ResaleEntryStateWithdrawn,
}

// parse reads a RESALE_ENTRY announcement: the entry as it is after the
// change, in the state its subject names.
func (h *ResaleEntryConsumer) parse(msg *message.Message) (*entity.ResaleEntry, error) {
	subject := eventmsg.CloudEventType(msg)
	state, ok := resaleEntryStates[subject]
	if !ok {
		err := fmt.Errorf("not a resale entry event: %q", subject)
		h.logger.Error(msg.Context(), "failed to parse resale entry event", err)
		return nil, err
	}
	var e entityv1.ResaleEntry
	if err := eventmsg.ParseProtobufData(msg, &e); err != nil {
		h.logger.Error(msg.Context(), "failed to parse "+subject+" event", err)
		return nil, fmt.Errorf("parse %s event: %w", subject, err)
	}
	entry := pb.ToResaleEntry(&e)
	if entry.ID == "" || entry.OrderID == "" {
		err := errors.New("the ResaleEntry has no id or no Order")
		h.logger.Error(msg.Context(), "failed to parse "+subject+" event", err)
		return nil, err
	}
	entry.State = state
	return entry, nil
}

// HandleStartWorkflow starts the issue workflow of a Won entry's Order and
// the release workflow of an ended entry's Order.
func (h *ResaleEntryConsumer) HandleStartWorkflow(msg *message.Message) error {
	entry, err := h.parse(msg)
	if err != nil {
		return err
	}
	h.logger.Info(msg.Context(), "processing resale entry event for its workflow",
		slog.String("resale_entry_id", string(entry.ID)),
		slog.String("state", entry.State.String()))
	if err := h.orderUC.StartResaleEntryWorkflow(msg.Context(), entry); err != nil {
		return fmt.Errorf("start the workflow of resale entry %s: %w", entry.ID, err)
	}
	return nil
}

// HandleSendResaleEntryEnded sends a Lapsed or Lost entry's ended email and
// push; its durable receives RESALE_ENTRY.lapsed and RESALE_ENTRY.lost only.
// While the mail service is unavailable the message is delivered again after
// endedEmailRetryDelay, as often as it takes: the entry is announced once, so
// this delivery is the only way the fan is told.
func (h *ResaleEntryConsumer) HandleSendResaleEntryEnded(msg *message.Message) error {
	entry, err := h.parse(msg)
	if err != nil {
		return err
	}
	if err := h.notificationUC.SendResaleEntryEnded(msg.Context(), entry.ID); err != nil {
		if errors.Is(err, apperr.ErrUnavailable) || errors.Is(err, apperr.ErrDeadlineExceeded) {
			return eventmsg.RedeliverLater(msg, err, endedEmailRetryDelay)
		}
		return fmt.Errorf("send the ended notice of resale entry %s: %w", entry.ID, err)
	}
	return nil
}

// ResaleListingSoldConsumer handles RESALE_LISTING.sold events, which
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
	if err := eventmsg.ParseProtobufData(msg, &l); err != nil {
		h.logger.Error(msg.Context(), "failed to parse RESALE_LISTING.sold event", err)
		return fmt.Errorf("parse RESALE_LISTING.sold event: %w", err)
	}
	id := entity.ResaleListingID(l.GetId().GetValue())
	if id == "" {
		err := errors.New("the ResaleListing has no id")
		h.logger.Error(msg.Context(), "failed to parse RESALE_LISTING.sold event", err)
		return err
	}
	if err := h.resaleListingUC.StartRefundSold(msg.Context(), id); err != nil {
		return fmt.Errorf("start the refund of resale listing %s: %w", id, err)
	}
	return nil
}
