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
	"github.com/liverty-music/backend/internal/infrastructure/messaging"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-logging/logging"
)

// LotteryEntryDrawnConsumer handles ORDER.lottery_entry_drawn events, which
// LotteryEntry.PersistDrawOutcome and LotteryEntry.RevertWin record, with two
// independent durables: the start of the workflow that charges a Won entry
// or releases a Lost one, and the loss email and push of a Lost entry. The
// workflow starter is guarded by the workflow named after the entry's Order,
// the loss notice by the entry's loss email send time and a Deliver named
// after the entry, so a redelivery starts and sends nothing twice. A mail
// outage never delays the start of a release: each durable has its own
// cursor.
type LotteryEntryDrawnConsumer struct {
	orderUC        usecase.OrderUseCase
	notificationUC usecase.NotificationUseCase
	logger         *logging.Logger
}

// lossEmailRetryDelay is how long a loss notice waits for the mail service
// after it was unavailable.
const lossEmailRetryDelay = time.Minute

// NewLotteryEntryDrawnConsumer creates a new LotteryEntryDrawnConsumer.
func NewLotteryEntryDrawnConsumer(orderUC usecase.OrderUseCase, notificationUC usecase.NotificationUseCase, logger *logging.Logger) *LotteryEntryDrawnConsumer {
	return &LotteryEntryDrawnConsumer{orderUC: orderUC, notificationUC: notificationUC, logger: logger}
}

// parse reads the ORDER.lottery_entry_drawn announcement: the entry as it is
// after the draw or the reverted win.
func (h *LotteryEntryDrawnConsumer) parse(msg *message.Message) (*entity.LotteryEntry, error) {
	var e entityv1.LotteryEntry
	if err := messaging.ParseProtobufData(msg, &e); err != nil {
		h.logger.Error(msg.Context(), "failed to parse ORDER.lottery_entry_drawn event", err)
		return nil, fmt.Errorf("parse ORDER.lottery_entry_drawn event: %w", err)
	}
	entry := pb.LotteryEntryFromAnnouncement(&e)
	if entry.ID == "" || entry.OrderID == "" {
		err := errors.New("the LotteryEntry has no id or no Order")
		h.logger.Error(msg.Context(), "failed to parse ORDER.lottery_entry_drawn event", err)
		return nil, err
	}
	return entry, nil
}

// HandleStartWorkflow starts the issue workflow of a Won entry's Order and
// the release workflow of a Lost entry's Order.
func (h *LotteryEntryDrawnConsumer) HandleStartWorkflow(msg *message.Message) error {
	entry, err := h.parse(msg)
	if err != nil {
		return err
	}
	h.logger.Info(msg.Context(), "processing ORDER.lottery_entry_drawn for its workflow",
		slog.String("lottery_entry_id", string(entry.ID)),
		slog.String("state", entry.State.String()))
	if err := h.orderUC.StartDrawnEntryWorkflow(msg.Context(), entry); err != nil {
		return fmt.Errorf("start the workflow of lottery entry %s: %w", entry.ID, err)
	}
	return nil
}

// HandleSendLotteryLoss sends a Lost entry's loss email and push. While the
// mail service is unavailable the message is delivered again after
// lossEmailRetryDelay, as often as it takes: the entry is announced Lost
// once, so this delivery is the only way the fan is told.
func (h *LotteryEntryDrawnConsumer) HandleSendLotteryLoss(msg *message.Message) error {
	entry, err := h.parse(msg)
	if err != nil {
		return err
	}
	if entry.State != entity.LotteryEntryStateLost {
		return nil
	}
	if err := h.notificationUC.SendLotteryLoss(msg.Context(), entry.ID); err != nil {
		if errors.Is(err, apperr.ErrUnavailable) || errors.Is(err, apperr.ErrDeadlineExceeded) {
			return messaging.RedeliverLater(msg, err, lossEmailRetryDelay)
		}
		return fmt.Errorf("send the loss notice of lottery entry %s: %w", entry.ID, err)
	}
	return nil
}
