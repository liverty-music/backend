package entity_test

import (
	"github.com/stretchr/testify/require"
	"testing"
	"time"

	"github.com/pannpers/go-apperr/apperr"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/stretchr/testify/assert"
)

func TestNewCheckout(t *testing.T) {
	t.Parallel()

	// @spec components/entity/checkout "New checkout"
	order := &entity.Order{ID: "order-1", Status: entity.OrderStatusPending}
	now := time.Date(2026, 11, 5, 18, 0, 0, 0, jst)

	c := entity.NewCheckout(order, "sale-1", now)

	assert.Equal(t, entity.CheckoutStatusHeld, c.Status)
	assert.Equal(t, time.Date(2026, 11, 5, 18, 15, 0, 0, jst), c.HoldExpireTime)
	assert.Equal(t, order.ID, c.OrderID)
	assert.Equal(t, entity.TicketSaleID("sale-1"), c.TicketSaleID)
	assert.Equal(t, c.ID, order.CheckoutID)
	assert.NoError(t, order.ValidateSource())
}

func TestValidateCheckoutSale(t *testing.T) {
	t.Parallel()

	stock := entity.NewTicketStock("event-1", "seat-s", 150)
	tests := []struct {
		name  string
		sale  *entity.TicketSale
		valid bool
	}{
		{
			// @spec components/entity/checkout "First-come sale of the event"
			name:  "first-come sale naming the stock's event",
			sale:  entity.NewTicketSale("series-1", "一般発売", entity.TicketSaleMethodFirstCome, nov(1, 10, 0), nov(20, 18, 0), "event-1"),
			valid: true,
		},
		{
			// @spec components/entity/checkout "Lottery sale"
			name: "lottery sale",
			sale: entity.NewTicketSale("series-1", "FC先行", entity.TicketSaleMethodLottery, nov(1, 10, 0), nov(10, 10, 0), "event-1"),
		},
		{
			// @spec components/entity/checkout "No sale"
			name: "no sale",
		},
		{
			name: "first-come sale of another event",
			sale: entity.NewTicketSale("series-1", "一般発売", entity.TicketSaleMethodFirstCome, nov(1, 10, 0), nov(20, 18, 0), "event-2"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := entity.ValidateCheckoutSale(tt.sale, stock)
			if tt.valid {
				assert.NoError(t, err)
			} else {
				assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
			}
		})
	}
}

func TestCheckout_IsHoldingAt(t *testing.T) {
	t.Parallel()

	expiry := time.Date(2026, 11, 5, 18, 15, 0, 0, jst)
	held := &entity.Checkout{Status: entity.CheckoutStatusHeld, HoldExpireTime: expiry}

	// @spec components/entity/checkout "Within the hold"
	assert.True(t, held.IsHoldingAt(expiry.Add(-time.Minute)))
	// @spec components/entity/checkout "Hold lapsed"
	assert.False(t, held.IsHoldingAt(expiry))

	committed := &entity.Checkout{Status: entity.CheckoutStatusCommitted, HoldExpireTime: expiry}
	assert.False(t, committed.IsHoldingAt(expiry.Add(-time.Minute)), "only a Held checkout is holding")
}

func TestCheckout_StatusAt(t *testing.T) {
	t.Parallel()

	expiry := time.Date(2026, 11, 5, 18, 15, 0, 0, jst)
	held := &entity.Checkout{Status: entity.CheckoutStatusHeld, HoldExpireTime: expiry}

	assert.Equal(t, entity.CheckoutStatusHeld, held.StatusAt(expiry.Add(-time.Minute)))
	assert.Equal(t, entity.CheckoutStatusExpired, held.StatusAt(expiry), "a lapsed hold reads as Expired")
}

func TestCheckout_CanBecome(t *testing.T) {
	t.Parallel()

	captured := time.Date(2026, 11, 5, 18, 14, 0, 0, jst)

	t.Run("charged checkout can only become completed", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/checkout "Charged checkout"
		c := &entity.Checkout{Status: entity.CheckoutStatusCommitted, CaptureTime: &captured}

		assert.True(t, c.CanBecome(entity.CheckoutStatusCompleted))
		assert.False(t, c.CanBecome(entity.CheckoutStatusReleased))
		assert.False(t, c.CanBecome(entity.CheckoutStatusExpired))
	})

	t.Run("uncharged commit can be released", func(t *testing.T) {
		t.Parallel()
		c := &entity.Checkout{Status: entity.CheckoutStatusCommitted}

		assert.True(t, c.CanBecome(entity.CheckoutStatusReleased))
	})
}

func heldCheckout(id, saleID string, count int, expire time.Time) *entity.Checkout {
	return &entity.Checkout{
		ID: entity.CheckoutID(id), OrderID: entity.OrderID("order-" + id), TicketSaleID: entity.TicketSaleID(saleID),
		Status: entity.CheckoutStatusHeld, HoldExpireTime: expire, Order: &entity.Order{TicketCount: count},
	}
}

func TestPlanCheckoutStart(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	holding, lapsed := now.Add(5*time.Minute), now.Add(-time.Minute)

	t.Run("nothing held", func(t *testing.T) {
		t.Parallel()
		plan := entity.PlanCheckoutStart(nil, "sale-1", 2, now)
		assert.Nil(t, plan.Reuse)
		assert.Nil(t, plan.Replaced)
		assert.Empty(t, plan.Ends)
	})

	t.Run("the holding checkout of the same sale and count is reused", func(t *testing.T) {
		t.Parallel()
		c := heldCheckout("c1", "sale-1", 2, holding)
		plan := entity.PlanCheckoutStart([]*entity.Checkout{c}, "sale-1", 2, now)
		assert.Same(t, c, plan.Reuse)
		assert.Empty(t, plan.Ends, "nothing is ended")
	})

	t.Run("a holding checkout of another count is released and its tickets are free", func(t *testing.T) {
		t.Parallel()
		c := heldCheckout("c1", "sale-1", 2, holding)
		plan := entity.PlanCheckoutStart([]*entity.Checkout{c}, "sale-1", 3, now)
		assert.Nil(t, plan.Reuse)
		assert.Same(t, c, plan.Replaced)
		require.Len(t, plan.Ends, 1)
		assert.Equal(t, entity.Transition[entity.CheckoutStatus]{From: []entity.CheckoutStatus{entity.CheckoutStatusHeld}, To: entity.CheckoutStatusReleased}, plan.Ends[0].Transition)
		assert.Equal(t, entity.SubjectCheckoutReleased, plan.Ends[0].Subject())
	})

	t.Run("a holding checkout of another sale is replaced", func(t *testing.T) {
		t.Parallel()
		plan := entity.PlanCheckoutStart([]*entity.Checkout{heldCheckout("c1", "sale-2", 2, holding)}, "sale-1", 2, now)
		assert.Nil(t, plan.Reuse)
		require.Len(t, plan.Ends, 1)
	})

	t.Run("a lapsed checkout is expired and frees nothing", func(t *testing.T) {
		t.Parallel()
		c := heldCheckout("c1", "sale-1", 2, lapsed)
		plan := entity.PlanCheckoutStart([]*entity.Checkout{c}, "sale-1", 2, now)
		assert.Nil(t, plan.Reuse, "a lapsed checkout is not reused")
		assert.Nil(t, plan.Replaced)
		require.Len(t, plan.Ends, 1)
		assert.Equal(t, entity.CheckoutStatusExpired, plan.Ends[0].Transition.To)
		assert.Equal(t, entity.SubjectCheckoutExpired, plan.Ends[0].Subject())
	})

	t.Run("a holding and a lapsed checkout are both ended", func(t *testing.T) {
		t.Parallel()
		plan := entity.PlanCheckoutStart([]*entity.Checkout{heldCheckout("c1", "sale-1", 1, lapsed), heldCheckout("c2", "sale-1", 1, holding)}, "sale-1", 4, now)
		require.Len(t, plan.Ends, 2)
		assert.Equal(t, entity.CheckoutStatusExpired, plan.Ends[0].Transition.To)
		assert.Equal(t, entity.CheckoutStatusReleased, plan.Ends[1].Transition.To)
	})
}

func TestCheckout_CommitOutcome(t *testing.T) {
	t.Parallel()
	for status, want := range map[entity.CheckoutStatus]struct {
		outcome entity.CommitOutcome
		decided bool
	}{
		entity.CheckoutStatusHeld:      {0, false},
		entity.CheckoutStatusCommitted: {entity.CommitOutcomeCommitted, true},
		entity.CheckoutStatusCompleted: {entity.CommitOutcomeCommitted, true},
		entity.CheckoutStatusExpired:   {entity.CommitOutcomeNotHeld, true},
		entity.CheckoutStatusReleased:  {entity.CommitOutcomeNotHeld, true},
	} {
		outcome, decided := (&entity.Checkout{Status: status}).CommitOutcome()
		assert.Equal(t, want.decided, decided, status.String())
		assert.Equal(t, want.outcome, outcome, status.String())
	}
}

func TestCheckout_CheckRevert(t *testing.T) {
	t.Parallel()
	at := time.Now()
	tests := []struct {
		name       string
		checkout   *entity.Checkout
		wantRevert bool
		wantErr    bool
	}{
		{"committed, uncharged", &entity.Checkout{Status: entity.CheckoutStatusCommitted}, true, false},
		{"committed, charged", &entity.Checkout{Status: entity.CheckoutStatusCommitted, CaptureTime: &at}, false, true},
		{"completed, charged", &entity.Checkout{Status: entity.CheckoutStatusCompleted, CaptureTime: &at}, false, false},
		{"held", &entity.Checkout{Status: entity.CheckoutStatusHeld}, false, false},
		{"released", &entity.Checkout{Status: entity.CheckoutStatusReleased}, false, false},
	}
	for _, tt := range tests {
		revert, err := tt.checkout.CheckRevert()
		assert.Equal(t, tt.wantRevert, revert, tt.name)
		if tt.wantErr {
			assert.ErrorIs(t, err, apperr.ErrFailedPrecondition, tt.name)
		} else {
			assert.NoError(t, err, tt.name)
		}
	}
}

func TestCheckout_CheckCaptureRecorded(t *testing.T) {
	t.Parallel()
	at := time.Now()
	assert.NoError(t, (&entity.Checkout{Status: entity.CheckoutStatusCommitted, CaptureTime: &at}).CheckCaptureRecorded(), "set once")
	assert.ErrorIs(t, (&entity.Checkout{Status: entity.CheckoutStatusHeld}).CheckCaptureRecorded(), apperr.ErrFailedPrecondition)
}

func TestTicketStock_RemainingWithout(t *testing.T) {
	t.Parallel()
	stock := &entity.TicketStock{Quantity: 10, SoldCount: 6, HeldCount: 3}
	assert.Equal(t, 1, stock.Remaining())
	assert.Equal(t, 3, stock.RemainingWithout(2), "the fan's own 2 held tickets count as free")
	over := &entity.TicketStock{Quantity: 5, SoldCount: 5, HeldCount: 2}
	assert.Equal(t, 0, over.RemainingWithout(1), "a stock over its quantity frees nothing")
}

func TestHoldingCheckoutOf(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	holding := heldCheckout("c1", "sale-1", 2, now.Add(time.Minute))
	holding.Order.TicketStockID = "stock-1"
	lapsed := heldCheckout("c2", "sale-1", 2, now.Add(-time.Minute))
	lapsed.Order.TicketStockID = "stock-1"
	other := heldCheckout("c3", "sale-1", 2, now.Add(time.Minute))
	other.Order.TicketStockID = "stock-2"

	assert.Same(t, holding, entity.HoldingCheckoutOf([]*entity.Checkout{lapsed, other, holding}, "stock-1", now))
	assert.Nil(t, entity.HoldingCheckoutOf([]*entity.Checkout{lapsed, other}, "stock-1", now), "a lapsed one and one of another stock do not hold it")
	assert.Nil(t, entity.HoldingCheckoutOf(nil, "stock-1", now))
}

func TestCheckout_NextStep(t *testing.T) {
	t.Parallel()
	for status, want := range map[entity.CheckoutStatus]entity.CheckoutStep{
		entity.CheckoutStatusHeld:      entity.CheckoutStepExpire,
		entity.CheckoutStatusExpired:   entity.CheckoutStepReleaseCardAuthorization,
		entity.CheckoutStatusReleased:  entity.CheckoutStepReleaseCardAuthorization,
		entity.CheckoutStatusCommitted: entity.CheckoutStepNone,
		entity.CheckoutStatusCompleted: entity.CheckoutStepNone,
	} {
		assert.Equal(t, want, (&entity.Checkout{Status: status}).NextStep(), status.String())
	}
}

func TestCheckout_CheckPlace(t *testing.T) {
	t.Parallel()
	for status, want := range map[entity.CheckoutStatus]struct {
		checks  bool
		wantErr bool
	}{
		entity.CheckoutStatusHeld:      {true, false},
		entity.CheckoutStatusCommitted: {false, false},
		entity.CheckoutStatusCompleted: {false, true},
		entity.CheckoutStatusExpired:   {false, true},
		entity.CheckoutStatusReleased:  {false, true},
	} {
		checks, err := (&entity.Checkout{Status: status}).CheckPlace()
		assert.Equal(t, want.checks, checks, status.String())
		if want.wantErr {
			assert.ErrorIs(t, err, apperr.ErrFailedPrecondition, status.String())
		} else {
			assert.NoError(t, err, status.String())
		}
	}
}
