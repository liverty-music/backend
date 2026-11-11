package event

import (
	"errors"
	"fmt"
	"log/slog"
	"time"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/liverty-music/backend/internal/adapter/pb"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/liverty-music/backend/pkg/eventmsg"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-logging/logging"
)

// LotteryEntryResultConsumer handles LOTTERY_ENTRY.won and
// LOTTERY_ENTRY.lost events, which LotteryEntry.PersistDrawOutcome,
// LotteryEntry.RevertWin and LotteryEntry.EndEnteredByEvent record, with two
// independent durables: the start of the workflow that charges a Won entry
// or releases a Lost one (both subjects), and the loss email and push of a
// Lost entry (LOTTERY_ENTRY.lost only). The subject tells the entry's state.
// The workflow starter is guarded by the workflow named after the entry's
// Order, the loss notice by the entry's loss email send time and a Deliver
// named after the entry, so a redelivery starts and sends nothing twice. A
// mail outage never delays the start of a release: each durable has its own
// cursor.
type LotteryEntryResultConsumer struct {
	orderUC        usecase.OrderUseCase
	notificationUC usecase.NotificationUseCase
	logger         *logging.Logger
}

// lossEmailRetryDelay is how long a loss notice waits for the mail service
// after it was unavailable.
const lossEmailRetryDelay = time.Minute

// NewLotteryEntryResultConsumer creates a new LotteryEntryResultConsumer.
func NewLotteryEntryResultConsumer(orderUC usecase.OrderUseCase, notificationUC usecase.NotificationUseCase, logger *logging.Logger) *LotteryEntryResultConsumer {
	return &LotteryEntryResultConsumer{orderUC: orderUC, notificationUC: notificationUC, logger: logger}
}

// parse reads a LOTTERY_ENTRY.won or LOTTERY_ENTRY.lost announcement: the
// entry as it is after the draw or the change, in the state its subject
// names.
func (h *LotteryEntryResultConsumer) parse(msg *message.Message) (*entity.LotteryEntry, error) {
	subject := eventmsg.CloudEventType(msg)
	var state entity.LotteryEntryState
	switch subject {
	case entity.SubjectLotteryEntryWon:
		state = entity.LotteryEntryStateWon
	case entity.SubjectLotteryEntryLost:
		state = entity.LotteryEntryStateLost
	default:
		err := fmt.Errorf("not a lottery entry result: %q", subject)
		h.logger.Error(msg.Context(), "failed to parse lottery entry result event", err)
		return nil, err
	}
	var e entityv1.LotteryEntry
	if err := eventmsg.ParseProtobufData(msg, &e); err != nil {
		h.logger.Error(msg.Context(), "failed to parse "+subject+" event", err)
		return nil, fmt.Errorf("parse %s event: %w", subject, err)
	}
	entry := pb.LotteryEntryFromAnnouncement(&e)
	if entry.ID == "" || entry.OrderID == "" {
		err := errors.New("the LotteryEntry has no id or no Order")
		h.logger.Error(msg.Context(), "failed to parse "+subject+" event", err)
		return nil, err
	}
	entry.State = state
	return entry, nil
}

// HandleStartWorkflow starts the issue workflow of a Won entry's Order and
// the release workflow of a Lost entry's Order.
func (h *LotteryEntryResultConsumer) HandleStartWorkflow(msg *message.Message) error {
	entry, err := h.parse(msg)
	if err != nil {
		return err
	}
	h.logger.Info(msg.Context(), "processing lottery entry result for its workflow",
		slog.String("lottery_entry_id", string(entry.ID)),
		slog.String("state", entry.State.String()))
	if err := h.orderUC.StartDrawnEntryWorkflow(msg.Context(), entry); err != nil {
		return fmt.Errorf("start the workflow of lottery entry %s: %w", entry.ID, err)
	}
	return nil
}

// HandleSendLotteryLoss sends a Lost entry's loss email and push; its
// durable receives LOTTERY_ENTRY.lost only. While the mail service is
// unavailable the message is delivered again after lossEmailRetryDelay, as
// often as it takes: the entry is announced Lost once, so this delivery is
// the only way the fan is told.
func (h *LotteryEntryResultConsumer) HandleSendLotteryLoss(msg *message.Message) error {
	entry, err := h.parse(msg)
	if err != nil {
		return err
	}
	if err := h.notificationUC.SendLotteryLoss(msg.Context(), entry.ID); err != nil {
		if errors.Is(err, apperr.ErrUnavailable) || errors.Is(err, apperr.ErrDeadlineExceeded) {
			return eventmsg.RedeliverLater(msg, err, lossEmailRetryDelay)
		}
		return fmt.Errorf("send the loss notice of lottery entry %s: %w", entry.ID, err)
	}
	return nil
}
