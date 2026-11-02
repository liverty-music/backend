package event

import (
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

// LotteryEntryEnteredConsumer handles ORDER.lottery_entry_entered: it records
// the entering fan's ticket journey for the event as Applied, set by the
// platform. MarkApplied stores nothing the second time, so a redelivery
// changes nothing. A returned error makes the router redeliver, and after the
// redelivery limit the message goes to the poison queue.
type LotteryEntryEnteredConsumer struct {
	ticketJourneyUC usecase.TicketJourneyUseCase
	stocks          ticketStockReader
	logger          *logging.Logger
}

// NewLotteryEntryEnteredConsumer creates a new LotteryEntryEnteredConsumer.
func NewLotteryEntryEnteredConsumer(
	ticketJourneyUC usecase.TicketJourneyUseCase,
	stocks ticketStockReader,
	logger *logging.Logger,
) *LotteryEntryEnteredConsumer {
	return &LotteryEntryEnteredConsumer{ticketJourneyUC: ticketJourneyUC, stocks: stocks, logger: logger}
}

// decode reads the announcement: the new entry's Pending Order, with the
// event of its TicketStock read by id.
func (h *LotteryEntryEnteredConsumer) decode(msg *message.Message) (entity.LotteryEntryEnteredData, error) {
	var order entityv1.Order
	if err := messaging.ParseProtobufData(msg, &order); err != nil {
		return entity.LotteryEntryEnteredData{}, err
	}
	data := entity.LotteryEntryEnteredData{
		LotteryEntryID: entity.LotteryEntryID(order.GetLotteryEntryId().GetValue()),
		UserID:         order.GetUserId().GetValue(),
	}
	stockID := order.GetTicketStockId().GetValue()
	if data.LotteryEntryID == "" || data.UserID == "" || stockID == "" {
		return entity.LotteryEntryEnteredData{}, errors.New("the Order names no lottery entry, user or TicketStock")
	}
	stock, err := h.stocks.Get(msg.Context(), entity.TicketStockID(stockID), time.Now())
	if err != nil {
		return entity.LotteryEntryEnteredData{}, fmt.Errorf("read the TicketStock %s of lottery entry %s: %w", stockID, data.LotteryEntryID, err)
	}
	data.EventID = stock.EventID
	return data, nil
}

// HandleMarkApplied records the fan's ticket journey for the event as Applied.
func (h *LotteryEntryEnteredConsumer) HandleMarkApplied(msg *message.Message) error {
	data, err := h.decode(msg)
	if err != nil {
		h.logger.Error(msg.Context(), "failed to parse ORDER.lottery_entry_entered event", err)
		return fmt.Errorf("parse ORDER.lottery_entry_entered event: %w", err)
	}
	h.logger.Info(msg.Context(), "processing ORDER.lottery_entry_entered for the ticket journey",
		slog.String("lottery_entry_id", string(data.LotteryEntryID)))
	if err := h.ticketJourneyUC.MarkApplied(msg.Context(), data); err != nil {
		return fmt.Errorf("mark ticket journey applied for lottery entry %s: %w", data.LotteryEntryID, err)
	}
	return nil
}
