package entity_test

import (
	"testing"

	"github.com/pannpers/go-apperr/apperr"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
		assert.ErrorIs(t, entity.ValidateLotteryEntrySale(sale, stock), apperr.ErrFailedPrecondition)
	})

	t.Run("lottery sale of another event", func(t *testing.T) {
		t.Parallel()
		sale := entity.NewTicketSale("series-1", "FC先行", entity.TicketSaleMethodLottery, nov(1, 10, 0), nov(10, 10, 0), "event-2")
		assert.ErrorIs(t, entity.ValidateLotteryEntrySale(sale, stock), apperr.ErrFailedPrecondition)
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

func TestLotteryEntry_RevertWin(t *testing.T) {
	t.Parallel()

	entryStates := []entity.LotteryEntryState{entity.LotteryEntryStateEntered, entity.LotteryEntryStateWon, entity.LotteryEntryStateLost}
	orderStatuses := []entity.OrderStatus{entity.OrderStatusPending, entity.OrderStatusPaid, entity.OrderStatusRefunded, entity.OrderStatusVoid}

	for _, state := range entryStates {
		for _, status := range orderStatuses {
			t.Run(state.String()+" entry with a "+status.String()+" order", func(t *testing.T) {
				t.Parallel()
				// @spec components/entity/lottery-entry/revert-win "Win reverted"
				e := &entity.LotteryEntry{State: state, Order: &entity.Order{Status: status}}
				entry, order, err := e.RevertWin(entity.LotteryEntryLostReasonCardNotCharged)

				switch {
				case state == entity.LotteryEntryStateLost:
					// Already Lost: nothing changes, whatever the Order is.
					require.NoError(t, err)
					assert.False(t, entry.Changed())
					assert.False(t, order.Changed())
					assert.Equal(t, entity.LotteryEntryStateLost, e.State)
					assert.Equal(t, status, e.Order.Status)
				case state == entity.LotteryEntryStateWon && status == entity.OrderStatusPending:
					require.NoError(t, err)
					assert.Equal(t, entity.Transition[entity.LotteryEntryState]{From: []entity.LotteryEntryState{entity.LotteryEntryStateWon}, To: entity.LotteryEntryStateLost}, entry)
					assert.Equal(t, entity.Transition[entity.OrderStatus]{From: []entity.OrderStatus{entity.OrderStatusPending}, To: entity.OrderStatusVoid}, order)
					assert.Equal(t, entity.LotteryEntryStateLost, e.State)
					assert.Equal(t, entity.LotteryEntryLostReasonCardNotCharged, e.LostReason)
					assert.Equal(t, entity.OrderStatusVoid, e.Order.Status)
				default:
					assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
					assert.False(t, entry.Changed())
					assert.False(t, order.Changed())
					assert.Equal(t, state, e.State, "a refused revert leaves the entry as it is")
					assert.Equal(t, status, e.Order.Status)
					assert.Equal(t, entity.LotteryEntryLostReasonUnspecified, e.LostReason)
				}
			})
		}
	}

	t.Run("an entry without its order", func(t *testing.T) {
		t.Parallel()
		e := &entity.LotteryEntry{State: entity.LotteryEntryStateWon}
		_, _, err := e.RevertWin(entity.LotteryEntryLostReasonCardNotCharged)
		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})
}
