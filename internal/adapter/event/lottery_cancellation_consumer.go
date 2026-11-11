package event

import (
	"errors"
	"fmt"
	"log/slog"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/liverty-music/backend/pkg/eventmsg"
	"github.com/pannpers/go-logging/logging"
)

// LotteryCancellationConsumer handles EVENT.cancelled, which
// Series.CancelEvents records once per cancelled Event, with a durable of its
// own, apart from the cancellation notice, so a failure in one is retried
// without running the other. It only decodes the Event and ends its lottery
// entries; ending them again changes nothing, so a redelivery is harmless.
type LotteryCancellationConsumer struct {
	lotteryUC usecase.LotteryEntryUseCase
	logger    *logging.Logger
}

// NewLotteryCancellationConsumer creates a new LotteryCancellationConsumer.
func NewLotteryCancellationConsumer(lotteryUC usecase.LotteryEntryUseCase, logger *logging.Logger) *LotteryCancellationConsumer {
	return &LotteryCancellationConsumer{lotteryUC: lotteryUC, logger: logger}
}

// HandleEventCancelled ends the Entered lottery entries of the cancelled
// Event. A failure is returned so the announcement is delivered again.
func (h *LotteryCancellationConsumer) HandleEventCancelled(msg *message.Message) error {
	var e entityv1.Event
	if err := eventmsg.ParseProtobufData(msg, &e); err != nil {
		h.logger.Error(msg.Context(), "failed to parse EVENT.cancelled event", err)
		return fmt.Errorf("parse %s event: %w", entity.SubjectEventCancelled, err)
	}
	eventID := e.GetId().GetValue()
	if eventID == "" {
		err := errors.New("the Event has no id")
		h.logger.Error(msg.Context(), "failed to parse EVENT.cancelled event", err)
		return err
	}
	h.logger.Info(msg.Context(), "ending the lottery entries of a cancelled event", slog.String("event_id", eventID))
	if err := h.lotteryUC.EndForCancelledEvent(msg.Context(), eventID); err != nil {
		return fmt.Errorf("end the lottery entries of event %s: %w", eventID, err)
	}
	return nil
}
