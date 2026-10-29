package entity_test

import (
	"testing"
	"time"

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
				assert.Error(t, err)
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
