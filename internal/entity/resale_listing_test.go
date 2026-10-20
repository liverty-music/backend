package entity_test

import (
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResaleListing_FeeAndRefundAmount(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		price      int64
		wantFee    int64
		wantRefund int64
	}{
		{
			// @spec components/entity/resale-listing "Round price"
			name: "round price", price: 5000, wantFee: 500, wantRefund: 4500,
		},
		{
			// @spec components/entity/resale-listing "Price that does not divide evenly"
			name: "price that does not divide evenly", price: 3333, wantFee: 333, wantRefund: 3000,
		},
		{
			// @spec components/entity/resale-listing "Smallest price"
			name: "smallest price", price: 9, wantFee: 0, wantRefund: 9,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			l := &entity.ResaleListing{Price: tt.price}
			assert.Equal(t, tt.wantFee, l.Fee())
			assert.Equal(t, tt.wantRefund, l.RefundAmount())
		})
	}
}

func TestResaleDeadlineOf(t *testing.T) {
	t.Parallel()

	date := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	ptr := func(t time.Time) *time.Time { return &t }

	t.Run("event with an open time", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/resale-listing "Event with an open time"
		got, ok := entity.ResaleDeadlineOf(&entity.Event{LocalDate: date, OpenTime: ptr(at(18, 0, 0)), StartTime: ptr(at(19, 0, 0))})
		require.True(t, ok)
		assert.True(t, got.Equal(at(16, 0, 0)))
	})

	t.Run("event without an open time", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/resale-listing "Event without an open time"
		got, ok := entity.ResaleDeadlineOf(&entity.Event{LocalDate: date, StartTime: ptr(at(19, 0, 0))})
		require.True(t, ok)
		assert.True(t, got.Equal(at(16, 0, 0)))
	})

	t.Run("event without a start time has no deadline", func(t *testing.T) {
		t.Parallel()
		_, ok := entity.ResaleDeadlineOf(&entity.Event{LocalDate: date})
		assert.False(t, ok)
	})

	t.Run("the deadline is when admission opens", func(t *testing.T) {
		t.Parallel()
		event := &entity.Event{LocalDate: date, OpenTime: ptr(at(17, 0, 0)), StartTime: ptr(at(19, 0, 0))}
		deadline, ok := entity.ResaleDeadlineOf(event)
		require.True(t, ok)
		window, ok := entity.AdmissionWindowOf(event)
		require.True(t, ok)
		assert.True(t, deadline.Equal(window.OpenTime))
	})
}

func TestResaleListing_IsOpenAt(t *testing.T) {
	t.Parallel()

	deadline := at(16, 0, 0)
	tests := []struct {
		name       string
		status     entity.ResaleListingStatus
		at         time.Time
		wantOpen   bool
		wantStatus entity.ResaleListingStatus
	}{
		{
			// @spec components/entity/resale-listing "Before the deadline"
			name: "before the deadline", status: entity.ResaleListingStatusListed, at: at(15, 59, 0),
			wantOpen: true, wantStatus: entity.ResaleListingStatusListed,
		},
		{
			// @spec components/entity/resale-listing "At the deadline"
			name: "at the deadline", status: entity.ResaleListingStatusListed, at: at(16, 0, 0),
			wantOpen: false, wantStatus: entity.ResaleListingStatusExpired,
		},
		{
			// @spec components/entity/resale-listing "Sold listing"
			name: "sold listing", status: entity.ResaleListingStatusSold, at: at(15, 0, 0),
			wantOpen: false, wantStatus: entity.ResaleListingStatusSold,
		},
		{
			name: "withdrawn listing after the deadline stays withdrawn", status: entity.ResaleListingStatusWithdrawn, at: at(17, 0, 0),
			wantOpen: false, wantStatus: entity.ResaleListingStatusWithdrawn,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			l := &entity.ResaleListing{Status: tt.status}
			assert.Equal(t, tt.wantOpen, l.IsOpenAt(tt.at, deadline))
			assert.Equal(t, tt.wantStatus, l.StatusAt(tt.at, deadline))
		})
	}
}

func TestResaleListing_IsFreeAt(t *testing.T) {
	t.Parallel()

	deadline := at(16, 0, 0)
	now := at(12, 0, 0)
	holding := &entity.Checkout{Status: entity.CheckoutStatusHeld, HoldExpireTime: at(12, 10, 0)}
	tests := []struct {
		name     string
		checkout *entity.Checkout
		want     bool
	}{
		{
			// @spec components/entity/resale-listing "Never held"
			name: "never held", checkout: nil, want: true,
		},
		{
			// @spec components/entity/resale-listing "Held by another fan"
			name: "held by another fan", checkout: holding, want: false,
		},
		{
			// @spec components/entity/resale-listing "Hold lapsed"
			name: "hold lapsed", checkout: &entity.Checkout{Status: entity.CheckoutStatusExpired, HoldExpireTime: at(11, 50, 0)}, want: true,
		},
		{
			name: "held but lapsed before the sweeper ran", checkout: &entity.Checkout{Status: entity.CheckoutStatusHeld, HoldExpireTime: at(11, 59, 0)}, want: true,
		},
		{
			// @spec components/entity/resale-listing "Being charged"
			name: "being charged", checkout: &entity.Checkout{Status: entity.CheckoutStatusCommitted, HoldExpireTime: at(11, 50, 0)}, want: false,
		},
		{
			name: "commit given back", checkout: &entity.Checkout{Status: entity.CheckoutStatusReleased, HoldExpireTime: at(12, 10, 0)}, want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			l := &entity.ResaleListing{Status: entity.ResaleListingStatusListed, Checkout: tt.checkout}
			assert.Equal(t, tt.want, l.IsFreeAt(now, deadline))
		})
	}

	t.Run("expired listing is not free", func(t *testing.T) {
		t.Parallel()
		l := &entity.ResaleListing{Status: entity.ResaleListingStatusListed}
		assert.False(t, l.IsFreeAt(deadline, deadline))
	})
}

func TestNewResaleListing(t *testing.T) {
	t.Parallel()

	ticket := &entity.Ticket{ID: "t1", OrderID: "o1", UserID: "u1", Status: entity.TicketStatusIssued}
	tt := &entity.TicketType{ID: "tt1", Price: 5000}
	l := entity.NewResaleListing(ticket, tt, at(12, 0, 0))

	assert.NotEmpty(t, l.ID)
	assert.Equal(t, entity.TicketID("t1"), l.TicketID)
	assert.Equal(t, entity.OrderID("o1"), l.OrderID)
	assert.Equal(t, entity.UserID("u1"), l.UserID)
	assert.Equal(t, entity.TicketTypeID("tt1"), l.TicketTypeID)
	assert.Equal(t, int64(5000), l.Price)
	assert.Equal(t, entity.ResaleListingStatusListed, l.Status)
	assert.True(t, l.CreateTime.Equal(at(12, 0, 0)))
}
