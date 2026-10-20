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

func TestNewResaleCheckout(t *testing.T) {
	t.Parallel()

	ticketType := &entity.TicketType{ID: "type-1", Price: 5000}
	deadline := time.Date(2026, 11, 20, 15, 0, 0, 0, jst)

	t.Run("resale checkout long before the deadline", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/checkout "Resale checkout long before the deadline"
		c := entity.NewResaleCheckout(ticketType, "user-1", 1, time.Date(2026, 11, 20, 12, 0, 0, 0, jst), deadline)

		assert.True(t, c.Resale)
		assert.Equal(t, entity.CheckoutStatusHeld, c.Status)
		assert.Equal(t, int64(5000), c.Amount)
		assert.Equal(t, time.Date(2026, 11, 20, 12, 15, 0, 0, jst), c.HoldExpireTime)
	})

	t.Run("resale checkout just before the deadline", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/checkout "Resale checkout just before the deadline"
		c := entity.NewResaleCheckout(ticketType, "user-1", 1, time.Date(2026, 11, 20, 14, 50, 0, 0, jst), deadline)

		assert.Equal(t, deadline, c.HoldExpireTime)
	})
}

func TestCheckout_ValidateResaleListings(t *testing.T) {
	t.Parallel()

	resale := &entity.Checkout{TicketTypeID: "type-1", UserID: "buyer", TicketCount: 2, Resale: true}
	listing := func(user entity.UserID) *entity.ResaleListing {
		return &entity.ResaleListing{TicketTypeID: "type-1", UserID: user}
	}

	tests := []struct {
		name     string
		checkout *entity.Checkout
		listings []*entity.ResaleListing
		wantErr  bool
	}{
		{
			// @spec components/entity/checkout "Two listings of two fans"
			name: "two listings of two fans", checkout: resale,
			listings: []*entity.ResaleListing{listing("fan-a"), listing("fan-b")},
		},
		{
			// @spec components/entity/checkout "The fan's own listing"
			name: "the fan's own listing", checkout: resale,
			listings: []*entity.ResaleListing{listing("fan-a"), listing("buyer")}, wantErr: true,
		},
		{
			name: "fewer listings than tickets", checkout: resale,
			listings: []*entity.ResaleListing{listing("fan-a")}, wantErr: true,
		},
		{
			name: "listing of another ticket type", checkout: resale,
			listings: []*entity.ResaleListing{listing("fan-a"), {TicketTypeID: "type-2", UserID: "fan-b"}}, wantErr: true,
		},
		{
			name: "stock checkout holds no listing", checkout: &entity.Checkout{TicketTypeID: "type-1", UserID: "buyer", TicketCount: 1},
			listings: []*entity.ResaleListing{listing("fan-a")}, wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.checkout.ValidateResaleListings(tt.listings)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
		})
	}
}
