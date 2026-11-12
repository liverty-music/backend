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

	pos := 3
	wonWith := func(status entity.OrderStatus) *entity.LotteryEntry {
		return &entity.LotteryEntry{State: entity.LotteryEntryStateWon, DrawPosition: &pos, Order: &entity.Order{Status: status}}
	}

	t.Run("win reverted", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/lottery-entry/revert-win "Win reverted"
		e := wonWith(entity.OrderStatusPending)
		entry, order, err := e.RevertWin(entity.LotteryEntryLostReasonCardNotCharged)

		require.NoError(t, err)
		assert.Equal(t, entity.Transition[entity.LotteryEntryState]{From: []entity.LotteryEntryState{entity.LotteryEntryStateWon}, To: entity.LotteryEntryStateLost}, entry)
		assert.Equal(t, entity.Transition[entity.OrderStatus]{From: []entity.OrderStatus{entity.OrderStatusPending}, To: entity.OrderStatusVoid}, order)
		assert.Equal(t, entity.LotteryEntryStateLost, e.State)
		assert.Equal(t, entity.LotteryEntryLostReasonCardNotCharged, e.LostReason)
		assert.Equal(t, entity.OrderStatusVoid, e.Order.Status)
		require.NotNil(t, e.DrawPosition)
		assert.Equal(t, 3, *e.DrawPosition, "the draw position is kept")
	})

	t.Run("win reverted for a cancelled concert", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/lottery-entry/revert-win "Win reverted for a cancelled concert"
		e := wonWith(entity.OrderStatusPending)
		_, _, err := e.RevertWin(entity.LotteryEntryLostReasonConcertCancelled)

		require.NoError(t, err)
		assert.Equal(t, entity.LotteryEntryStateLost, e.State)
		assert.Equal(t, entity.LotteryEntryLostReasonConcertCancelled, e.LostReason)
	})

	t.Run("already reverted", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/lottery-entry/revert-win "Already reverted"
		e := &entity.LotteryEntry{State: entity.LotteryEntryStateLost, LostReason: entity.LotteryEntryLostReasonCardNotCharged, Order: &entity.Order{Status: entity.OrderStatusVoid}}
		entry, order, err := e.RevertWin(entity.LotteryEntryLostReasonConcertCancelled)

		require.NoError(t, err)
		assert.False(t, entry.Changed())
		assert.False(t, order.Changed())
		assert.Equal(t, entity.LotteryEntryLostReasonCardNotCharged, e.LostReason, "the first reason is kept")
	})

	t.Run("win already issued", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/lottery-entry/revert-win "Win already issued"
		for _, status := range []entity.OrderStatus{entity.OrderStatusPaid, entity.OrderStatusRefunded} {
			e := wonWith(status)
			entry, order, err := e.RevertWin(entity.LotteryEntryLostReasonCardNotCharged)

			assert.ErrorIs(t, err, apperr.ErrFailedPrecondition, status.String())
			assert.False(t, entry.Changed())
			assert.False(t, order.Changed())
			assert.Equal(t, entity.LotteryEntryStateWon, e.State)
			assert.Equal(t, status, e.Order.Status)
		}
	})

	t.Run("not drawn", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/lottery-entry/revert-win "Not drawn"
		e := &entity.LotteryEntry{State: entity.LotteryEntryStateEntered, Order: &entity.Order{Status: entity.OrderStatusPending}}
		entry, order, err := e.RevertWin(entity.LotteryEntryLostReasonCardNotCharged)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		assert.False(t, entry.Changed())
		assert.False(t, order.Changed())
		assert.Equal(t, entity.LotteryEntryStateEntered, e.State)
	})

	t.Run("an entry without its order is refused", func(t *testing.T) {
		t.Parallel()
		e := &entity.LotteryEntry{State: entity.LotteryEntryStateWon}
		_, _, err := e.RevertWin(entity.LotteryEntryLostReasonCardNotCharged)
		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})
}

func TestLotteryEntry_CheckChargeable(t *testing.T) {
	t.Parallel()
	for _, state := range []entity.LotteryEntryState{entity.LotteryEntryStateEntered, entity.LotteryEntryStateWon, entity.LotteryEntryStateLost} {
		for _, status := range []entity.OrderStatus{entity.OrderStatusPending, entity.OrderStatusPaid, entity.OrderStatusVoid} {
			e := &entity.LotteryEntry{State: state, Order: &entity.Order{Status: status}}
			err := e.CheckChargeable()
			if state == entity.LotteryEntryStateWon && status == entity.OrderStatusPending {
				assert.NoError(t, err)
				continue
			}
			assert.ErrorIs(t, err, apperr.ErrFailedPrecondition, state.String()+"/"+status.String())
		}
	}
}

func TestLotteryOutcomes(t *testing.T) {
	t.Parallel()

	entered := []entity.LotteryEntryState{entity.LotteryEntryStateEntered}
	pending := []entity.OrderStatus{entity.OrderStatusPending}

	t.Run("selected by the draw", func(t *testing.T) {
		t.Parallel()
		o := entity.DrawWonOutcome()
		assert.Equal(t, entity.Transition[entity.LotteryEntryState]{From: entered, To: entity.LotteryEntryStateWon}, o.Entry)
		assert.Equal(t, entity.LotteryEntryLostReasonUnspecified, o.LostReason)
		assert.False(t, o.Order.Changed(), "the Order stays Pending until it is charged")
	})

	t.Run("not selected by the draw", func(t *testing.T) {
		t.Parallel()
		o := entity.DrawLostOutcome()
		assert.Equal(t, entity.Transition[entity.LotteryEntryState]{From: entered, To: entity.LotteryEntryStateLost}, o.Entry)
		assert.Equal(t, entity.LotteryEntryLostReasonNotDrawn, o.LostReason)
		assert.Equal(t, entity.Transition[entity.OrderStatus]{From: pending, To: entity.OrderStatusVoid}, o.Order)
	})

	t.Run("concert cancelled", func(t *testing.T) {
		t.Parallel()
		o := entity.ConcertCancelledOutcome()
		assert.Equal(t, entity.Transition[entity.LotteryEntryState]{From: entered, To: entity.LotteryEntryStateLost}, o.Entry)
		assert.Equal(t, entity.LotteryEntryLostReasonConcertCancelled, o.LostReason)
		assert.Equal(t, entity.Transition[entity.OrderStatus]{From: pending, To: entity.OrderStatusVoid}, o.Order)
	})
}

func TestLotteryEntry_NextStep(t *testing.T) {
	t.Parallel()
	for state, want := range map[entity.LotteryEntryState]entity.LotteryEntryStep{
		entity.LotteryEntryStateEntered: entity.LotteryEntryStepNone,
		entity.LotteryEntryStateWon:     entity.LotteryEntryStepIssue,
		entity.LotteryEntryStateLost:    entity.LotteryEntryStepReleaseCardAuthorization,
	} {
		assert.Equal(t, want, (&entity.LotteryEntry{State: state}).NextStep(), state.String())
	}
}

func TestNewLotteryEntryStats(t *testing.T) {
	t.Parallel()

	entry := func(state entity.LotteryEntryState, tickets int) *entity.LotteryEntry {
		return &entity.LotteryEntry{State: state, Order: &entity.Order{TicketCount: tickets}}
	}
	got := entity.NewLotteryEntryStats([]*entity.LotteryEntry{
		entry(entity.LotteryEntryStateEntered, 1),
		entry(entity.LotteryEntryStateWon, 2),
		entry(entity.LotteryEntryStateWon, 3),
		entry(entity.LotteryEntryStateLost, 4),
	})

	assert.Equal(t, entity.LotteryEntryStats{EntryCount: 4, TicketCount: 10, WonEntryCount: 2, WonTicketCount: 5, LostEntryCount: 1}, got)
	assert.Equal(t, entity.LotteryEntryStats{}, entity.NewLotteryEntryStats(nil))
}

func TestHasEntryForStocks(t *testing.T) {
	t.Parallel()

	stocks := []*entity.TicketStock{{ID: "stock-a"}, {ID: "stock-b"}}
	entryOf := func(stockID entity.TicketStockID, state entity.LotteryEntryState) *entity.LotteryEntry {
		return &entity.LotteryEntry{State: state, Order: &entity.Order{TicketStockID: stockID}}
	}

	assert.True(t, entity.HasEntryForStocks([]*entity.LotteryEntry{entryOf("stock-b", entity.LotteryEntryStateEntered)}, stocks), "an entry for another stock of the event counts")
	assert.True(t, entity.HasEntryForStocks([]*entity.LotteryEntry{entryOf("stock-a", entity.LotteryEntryStateLost)}, stocks), "in any state")
	assert.False(t, entity.HasEntryForStocks([]*entity.LotteryEntry{entryOf("stock-other-event", entity.LotteryEntryStateEntered)}, stocks), "an entry for a stock of another event does not")
	assert.False(t, entity.HasEntryForStocks(nil, stocks))
}
