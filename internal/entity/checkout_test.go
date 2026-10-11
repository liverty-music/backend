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
	ticketType := &entity.TicketType{ID: "type-1", Price: 3000}
	now := time.Date(2026, 11, 5, 18, 0, 0, 0, jst)

	c := entity.NewCheckout(ticketType, "user-1", 2, now)

	assert.Equal(t, entity.CheckoutStatusHeld, c.Status)
	assert.Equal(t, int64(6000), c.Amount)
	assert.Equal(t, time.Date(2026, 11, 5, 18, 15, 0, 0, jst), c.HoldExpireTime)
	assert.Equal(t, ticketType.ID, c.TicketTypeID)
	assert.Equal(t, entity.UserID("user-1"), c.UserID)
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
