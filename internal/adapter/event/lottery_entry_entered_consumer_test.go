package event_test

import (
	"testing"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	"github.com/liverty-music/backend/internal/adapter/event"
	"github.com/liverty-music/backend/internal/entity"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
)

// enteredOrder is the LOTTERY_ENTRY.entered announcement of a new
// entry: its Pending Order.
func enteredOrder() *entityv1.Order {
	return &entityv1.Order{
		Id:            &entityv1.OrderId{Value: "order-1"},
		UserId:        &entityv1.UserId{Value: "fan-1"},
		TicketStockId: &entityv1.TicketStockId{Value: "stock-1"},
		TicketCount:   2,
		Status:        entityv1.OrderStatus_ORDER_STATUS_PENDING,
		Source:        &entityv1.Order_LotteryEntryId{LotteryEntryId: &entityv1.LotteryEntryId{Value: "entry-1"}},
	}
}

func TestLotteryEntryEnteredConsumer(t *testing.T) {
	t.Parallel()

	entered := entity.LotteryEntryEnteredData{LotteryEntryID: "entry-1", UserID: "fan-1", EventID: "event-1"}

	t.Run("marks the fan's journey for the event of its TicketStock applied", func(t *testing.T) {
		t.Parallel()
		journeys := ucmocks.NewMockTicketJourneyUseCase(t)
		journeys.EXPECT().MarkApplied(anyCtx, entered).Return(nil).Once()
		consumer := event.NewLotteryEntryEnteredConsumer(journeys, stubStocks{}, newTestLogger(t))

		assert.NoError(t, consumer.HandleMarkApplied(makeAnnouncementMsg(t, "announcement-1", "", enteredOrder())))
	})

	t.Run("a failed journey write is returned for redelivery", func(t *testing.T) {
		t.Parallel()
		journeys := ucmocks.NewMockTicketJourneyUseCase(t)
		journeys.EXPECT().MarkApplied(anyCtx, entered).Return(apperr.ErrUnavailable).Once()
		consumer := event.NewLotteryEntryEnteredConsumer(journeys, stubStocks{}, newTestLogger(t))

		assert.ErrorIs(t, consumer.HandleMarkApplied(makeAnnouncementMsg(t, "announcement-1", "", enteredOrder())), apperr.ErrUnavailable)
	})

	t.Run("an Order without a lottery entry is an error", func(t *testing.T) {
		t.Parallel()
		consumer := event.NewLotteryEntryEnteredConsumer(ucmocks.NewMockTicketJourneyUseCase(t), stubStocks{}, newTestLogger(t))
		order := enteredOrder()
		order.Source = nil

		assert.Error(t, consumer.HandleMarkApplied(makeAnnouncementMsg(t, "announcement-1", "", order)))
	})

	t.Run("a malformed message is an error", func(t *testing.T) {
		t.Parallel()
		consumer := event.NewLotteryEntryEnteredConsumer(ucmocks.NewMockTicketJourneyUseCase(t), stubStocks{}, newTestLogger(t))

		assert.Error(t, consumer.HandleMarkApplied(malformedMsg()))
	})
}
