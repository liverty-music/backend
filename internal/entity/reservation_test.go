package entity_test

import (
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/stretchr/testify/assert"
)

func TestNewReservation(t *testing.T) {
	t.Parallel()

	// @spec components/entity/reservation "New checkout"
	sale := &entity.TicketSale{ID: "sale-1", Price: 3000}
	now := time.Date(2026, 11, 5, 18, 0, 0, 0, jst)

	r := entity.NewReservation(sale, "user-1", 2, now, "trace-1")

	assert.Equal(t, entity.ReservationStatusHeld, r.Status)
	assert.Equal(t, int64(6000), r.Amount)
	assert.Equal(t, time.Date(2026, 11, 5, 18, 15, 0, 0, jst), r.HoldExpireTime)
	assert.Equal(t, sale.ID, r.TicketSaleID)
	assert.Equal(t, "trace-1", r.TraceID)
}

func TestReservation_IsHoldingAt(t *testing.T) {
	t.Parallel()

	expiry := time.Date(2026, 11, 5, 18, 15, 0, 0, jst)
	held := &entity.Reservation{Status: entity.ReservationStatusHeld, HoldExpireTime: expiry}

	// @spec components/entity/reservation "Within the hold"
	assert.True(t, held.IsHoldingAt(expiry.Add(-time.Minute)))
	// @spec components/entity/reservation "Hold lapsed"
	assert.False(t, held.IsHoldingAt(expiry))

	committed := &entity.Reservation{Status: entity.ReservationStatusCommitted, HoldExpireTime: expiry}
	assert.False(t, committed.IsHoldingAt(expiry.Add(-time.Minute)), "only a Held reservation is holding")
}

func TestReservation_CanBecome(t *testing.T) {
	t.Parallel()

	captured := time.Date(2026, 11, 5, 18, 14, 0, 0, jst)

	t.Run("charged checkout can only become completed", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/reservation "Charged checkout"
		r := &entity.Reservation{Status: entity.ReservationStatusCommitted, CaptureTime: &captured}

		assert.True(t, r.CanBecome(entity.ReservationStatusCompleted))
		assert.False(t, r.CanBecome(entity.ReservationStatusReleased))
		assert.False(t, r.CanBecome(entity.ReservationStatusExpired))
	})

	t.Run("uncharged commit can be released", func(t *testing.T) {
		t.Parallel()
		r := &entity.Reservation{Status: entity.ReservationStatusCommitted}

		assert.True(t, r.CanBecome(entity.ReservationStatusReleased))
	})
}

func TestReservation_Validate(t *testing.T) {
	t.Parallel()

	t.Run("domestic-format phone number", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/reservation "Domestic-format phone number"
		r := &entity.Reservation{HolderIdentity: &entity.HolderIdentity{FullName: "山田 花子", PhoneNumber: "090-1234-5678"}}

		assert.Error(t, r.Validate())
	})

	t.Run("E.164 phone number", func(t *testing.T) {
		t.Parallel()
		r := &entity.Reservation{HolderIdentity: &entity.HolderIdentity{FullName: "山田 花子", PhoneNumber: "+819012345678"}}

		assert.NoError(t, r.Validate())
	})

	t.Run("no identity yet", func(t *testing.T) {
		t.Parallel()
		assert.NoError(t, (&entity.Reservation{}).Validate())
	})
}
