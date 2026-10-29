package entity_test

import (
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/stretchr/testify/assert"
)

// stockOf returns a TicketStock with the given quantity, sold count and held
// count.
func stockOf(quantity, sold, held int) *entity.TicketStock {
	return &entity.TicketStock{Quantity: quantity, SoldCount: sold, HeldCount: held}
}

func TestTicketStock_Validate(t *testing.T) {
	t.Parallel()

	// @spec components/entity/ticket-stock "Valid stock"
	t.Run("quantity of 150", func(t *testing.T) {
		t.Parallel()
		assert.NoError(t, entity.NewTicketStock("event-1", "seat-type-1", 150).Validate())
	})

	// @spec components/entity/ticket-stock "Empty stock"
	t.Run("quantity of 0", func(t *testing.T) {
		t.Parallel()
		assert.Error(t, entity.NewTicketStock("event-1", "seat-type-1", 0).Validate())
	})
}

func TestTicketStock_Remaining(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		stock *entity.TicketStock
		want  int
	}{
		{
			// @spec components/entity/ticket-stock "Some sold, some held"
			name:  "100 sold and 6 held of 150",
			stock: stockOf(150, 100, 6),
			want:  44,
		},
		{
			// @spec components/entity/ticket-stock "Presale winners count as sold"
			name:  "120 won in a presale of 150",
			stock: stockOf(150, 120, 0),
			want:  30,
		},
		{
			name:  "never below 0",
			stock: stockOf(2, 2, 1),
			want:  0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, tt.stock.Remaining())
		})
	}
}

func TestTicketStock_State(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		stock    *entity.TicketStock
		want     entity.TicketStockState
		lowStock bool
	}{
		{
			// @spec components/entity/ticket-stock "Sold out"
			name:  "150 sold of 150",
			stock: stockOf(150, 150, 0),
			want:  entity.TicketStockStateSoldOut,
		},
		{
			// @spec components/entity/ticket-stock "Resold tickets do not count again"
			// A resale moves a sold ticket to another fan; the sold count stays at
			// the quantity, so the stock stays SoldOut.
			name:  "sold out after a resale",
			stock: stockOf(150, 150, 0),
			want:  entity.TicketStockStateSoldOut,
		},
		{
			// @spec components/entity/ticket-stock "Last tickets held by others"
			name:  "147 sold and 3 held of 150",
			stock: stockOf(150, 147, 3),
			want:  entity.TicketStockStateAllHeld,
		},
		{
			// @spec components/entity/ticket-stock "Few left"
			name:     "15 remaining of 150",
			stock:    stockOf(150, 135, 0),
			want:     entity.TicketStockStateAvailable,
			lowStock: true,
		},
		{
			// @spec components/entity/ticket-stock "Small stock, last ticket"
			name:     "1 remaining of 9",
			stock:    stockOf(9, 8, 0),
			want:     entity.TicketStockStateAvailable,
			lowStock: true,
		},
		{
			name:  "16 remaining of 150",
			stock: stockOf(150, 134, 0),
			want:  entity.TicketStockStateAvailable,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, tt.stock.State())
			assert.Equal(t, tt.lowStock, tt.stock.IsLowStock())
		})
	}
}

func TestTicketStock_IsResaleOpenAt(t *testing.T) {
	t.Parallel()

	deadline := time.Date(2026, 12, 20, 15, 0, 0, 0, time.UTC)
	tests := []struct {
		name  string
		stock *entity.TicketStock
		at    time.Time
		want  bool
	}{
		{
			// @spec components/entity/ticket-stock "Sold out a week before the event"
			name:  "sold out a week before the deadline",
			stock: stockOf(150, 150, 0),
			at:    deadline.Add(-7 * 24 * time.Hour),
			want:  true,
		},
		{
			// @spec components/entity/ticket-stock "After the deadline"
			name:  "sold out at the deadline",
			stock: stockOf(150, 150, 0),
			at:    deadline,
		},
		{
			// @spec components/entity/ticket-stock "A ticket came back"
			name:  "one ticket back after a reverted commit",
			stock: stockOf(150, 149, 0),
			at:    deadline.Add(-7 * 24 * time.Hour),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, tt.stock.IsResaleOpenAt(tt.at, deadline))
		})
	}

	t.Run("a ticket that came back leaves the stock Available", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, entity.TicketStockStateAvailable, stockOf(150, 149, 0).State())
	})
}
