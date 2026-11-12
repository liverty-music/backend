package entity_test

import (
	"github.com/pannpers/go-apperr/apperr"
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

	jst := time.FixedZone("JST", 9*60*60)
	date := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	on := func(h int) *time.Time { t := time.Date(2026, 11, 20, h, 0, 0, 0, jst); return &t }

	t.Run("event with an open time", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/resale-listing "Event with an open time"
		got, ok := entity.ResaleDeadlineOf(&entity.Event{LocalDate: date, OpenTime: on(18), StartTime: on(19)})
		require.True(t, ok)
		assert.True(t, got.Equal(*on(12)), "12:00 that day, earlier than 3 hours before the start")
	})

	t.Run("event without an open time", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/resale-listing "Event without an open time"
		got, ok := entity.ResaleDeadlineOf(&entity.Event{LocalDate: date, StartTime: on(14)})
		require.True(t, ok)
		assert.True(t, got.Equal(*on(11)), "3 hours before a 14:00 start, earlier than 12:00")
	})

	t.Run("event without a start time has no deadline", func(t *testing.T) {
		t.Parallel()
		_, ok := entity.ResaleDeadlineOf(&entity.Event{LocalDate: date})
		assert.False(t, ok)
	})

	t.Run("resale is closed when admission opens", func(t *testing.T) {
		t.Parallel()
		event := &entity.Event{LocalDate: date, OpenTime: on(13), StartTime: on(14)}
		deadline, ok := entity.ResaleDeadlineOf(event)
		require.True(t, ok)
		window, ok := entity.AdmissionWindowOf(event)
		require.True(t, ok)
		assert.False(t, deadline.After(window.OpenTime))
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

func TestResaleListing_IsWithdrawableAt(t *testing.T) {
	t.Parallel()

	deadline := at(16, 0, 0)
	listed := &entity.ResaleListing{Status: entity.ResaleListingStatusListed}

	t.Run("open listing", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/resale-listing "Open listing"
		assert.True(t, listed.IsWithdrawableAt(at(12, 0, 0), deadline))
	})

	t.Run("expired listing", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/resale-listing "Expired listing"
		assert.False(t, listed.IsWithdrawableAt(deadline, deadline))
	})
}

func TestNewResaleListing(t *testing.T) {
	t.Parallel()

	ticket := &entity.Ticket{ID: "t1", OrderID: "o1", UserID: "u1", Status: entity.TicketStatusIssued}
	seatType := &entity.SeatType{ID: "seat-s", Price: 5000}
	l := entity.NewResaleListing(ticket, "stock-1", seatType, at(12, 0, 0))

	assert.NotEmpty(t, l.ID)
	assert.Equal(t, entity.TicketID("t1"), l.TicketID)
	assert.Equal(t, entity.OrderID("o1"), l.OrderID)
	assert.Equal(t, entity.UserID("u1"), l.UserID)
	assert.Equal(t, entity.TicketStockID("stock-1"), l.TicketStockID)
	assert.Equal(t, int64(5000), l.Price)
	assert.Equal(t, entity.ResaleListingStatusListed, l.Status)
	assert.True(t, l.CreateTime.Equal(at(12, 0, 0)))
}

func TestResaleListing_NeedsRefund(t *testing.T) {
	t.Parallel()

	refunded := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name    string
		l       *entity.ResaleListing
		want    bool
		wantErr bool
	}{
		{"sold, not refunded", &entity.ResaleListing{Status: entity.ResaleListingStatusSold}, true, false},
		{"sold, refunded", &entity.ResaleListing{Status: entity.ResaleListingStatusSold, RefundTime: &refunded}, false, false},
		{"listed", &entity.ResaleListing{Status: entity.ResaleListingStatusListed}, false, true},
		{"cancelled", &entity.ResaleListing{Status: entity.ResaleListingStatusCancelled}, false, true},
	}
	for _, tt := range tests {
		got, err := tt.l.NeedsRefund()
		assert.Equal(t, tt.want, got, tt.name)
		if tt.wantErr {
			assert.ErrorIs(t, err, apperr.ErrFailedPrecondition, tt.name)
		} else {
			assert.NoError(t, err, tt.name)
		}
	}
}
