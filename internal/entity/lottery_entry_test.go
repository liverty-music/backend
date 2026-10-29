package entity_test

import (
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/stretchr/testify/assert"
)

func TestNewLotteryEntry(t *testing.T) {
	t.Parallel()

	// @spec components/entity/lottery-entry "New entry"
	order := &entity.Order{ID: "order-1", Status: entity.OrderStatusPending, CardAuthorizationID: "pi_1"}
	e := entity.NewLotteryEntry(order, "sale-1")

	assert.NotEmpty(t, e.ID)
	assert.Equal(t, entity.LotteryEntryStateEntered, e.State)
	assert.Equal(t, order.ID, e.OrderID)
	assert.Equal(t, "pi_1", e.Order.CardAuthorizationID)
	assert.Nil(t, e.DrawPosition)
	assert.Equal(t, entity.TicketSaleID("sale-1"), e.TicketSaleID)
	assert.Equal(t, e.ID, order.LotteryEntryID)
	assert.NoError(t, order.ValidateSource())
}

func TestValidateLotteryEntrySale(t *testing.T) {
	t.Parallel()

	stock := entity.NewTicketStock("event-1", "seat-s", 150)

	t.Run("lottery sale of the event", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/lottery-entry "Lottery sale of the event"
		sale := entity.NewTicketSale("series-1", "FC先行", entity.TicketSaleMethodLottery, nov(1, 10, 0), nov(10, 10, 0), "event-1")
		assert.NoError(t, entity.ValidateLotteryEntrySale(sale, stock))
	})

	t.Run("first-come sale", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/lottery-entry "First-come sale"
		sale := entity.NewTicketSale("series-1", "一般発売", entity.TicketSaleMethodFirstCome, nov(1, 10, 0), nov(20, 18, 0), "event-1")
		assert.Error(t, entity.ValidateLotteryEntrySale(sale, stock))
	})

	t.Run("lottery sale of another event", func(t *testing.T) {
		t.Parallel()
		sale := entity.NewTicketSale("series-1", "FC先行", entity.TicketSaleMethodLottery, nov(1, 10, 0), nov(10, 10, 0), "event-2")
		assert.Error(t, entity.ValidateLotteryEntrySale(sale, stock))
	})
}

func TestLotteryEntryState_Values(t *testing.T) {
	t.Parallel()

	// The numbers are stored and sent on the wire; they must match the proto
	// enum LotteryEntryState and the DB CHECK.
	assert.EqualValues(t, 1, entity.LotteryEntryStateEntered)
	assert.EqualValues(t, 2, entity.LotteryEntryStateWon)
	assert.EqualValues(t, 3, entity.LotteryEntryStateLost)
}
