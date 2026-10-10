package entity_test

import (
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/stretchr/testify/assert"
)

func saleOf(quantity, sold, held int) *entity.TicketSale {
	return &entity.TicketSale{
		SaleStartTime: time.Date(2026, 11, 1, 10, 0, 0, 0, jst),
		SaleEndTime:   time.Date(2026, 11, 20, 18, 0, 0, 0, jst),
		Price:         3000,
		Quantity:      quantity,
		SoldCount:     sold,
		HeldCount:     held,
	}
}

func TestTicketSale_Validate(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 11, 1, 10, 0, 0, 0, jst)
	end := time.Date(2026, 11, 20, 18, 0, 0, 0, jst)

	t.Run("usual sale gets the default limit", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/ticket-sale "Usual sale"
		sale := entity.NewTicketSale("event-1", start, end, 3000, 150, 0)

		assert.NoError(t, sale.Validate())
		assert.Equal(t, 4, sale.PerAccountLimit)
		assert.Equal(t, entity.TicketSaleMethodFirstCome, sale.Method)
		assert.Zero(t, sale.SoldCount)
	})

	tests := []struct {
		name  string
		start time.Time
		end   time.Time
		price int64
		qty   int
		limit int
	}{
		{
			// @spec components/entity/ticket-sale "End before start"
			name: "end before start", start: end, end: start, price: 3000, qty: 150, limit: 4,
		},
		{
			name: "end equal to start", start: start, end: start, price: 3000, qty: 150, limit: 4,
		},
		{
			// @spec components/entity/ticket-sale "Limit over 10"
			name: "limit over 10", start: start, end: end, price: 3000, qty: 150, limit: 11,
		},
		{
			name: "price zero", start: start, end: end, price: 0, qty: 150, limit: 4,
		},
		{
			name: "price over one million", start: start, end: end, price: 1_000_001, qty: 150, limit: 4,
		},
		{
			name: "quantity zero", start: start, end: end, price: 3000, qty: 0, limit: 4,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			sale := entity.NewTicketSale("event-1", tt.start, tt.end, tt.price, tt.qty, tt.limit)

			assert.Error(t, sale.Validate())
		})
	}
}

func TestTicketSale_Remaining(t *testing.T) {
	t.Parallel()

	// @spec components/entity/ticket-sale "Some sold, some held"
	assert.Equal(t, 44, saleOf(150, 100, 6).Remaining())
	assert.Equal(t, 0, saleOf(150, 148, 6).Remaining(), "never below 0")
}

func TestTicketSale_StateAt(t *testing.T) {
	t.Parallel()

	inWindow := time.Date(2026, 11, 5, 12, 0, 0, 0, jst)

	tests := []struct {
		name      string
		sale      *entity.TicketSale
		at        time.Time
		wantState entity.TicketSaleState
		wantLow   bool
	}{
		{
			// @spec components/entity/ticket-sale "Before opening"
			name:      "before opening",
			sale:      saleOf(150, 0, 0),
			at:        time.Date(2026, 11, 1, 9, 59, 0, 0, jst),
			wantState: entity.TicketSaleStateNotYetOnSale,
		},
		{
			// @spec components/entity/ticket-sale "Last tickets held by others"
			name:      "last tickets held by others",
			sale:      saleOf(150, 147, 3),
			at:        inWindow,
			wantState: entity.TicketSaleStateAllHeld,
		},
		{
			// @spec components/entity/ticket-sale "Sold out"
			name:      "sold out",
			sale:      saleOf(150, 150, 0),
			at:        inWindow,
			wantState: entity.TicketSaleStateSoldOut,
		},
		{
			// @spec components/entity/ticket-sale "Few left"
			name:      "few left",
			sale:      saleOf(150, 135, 0),
			at:        inWindow,
			wantState: entity.TicketSaleStateOnSale,
			wantLow:   true,
		},
		{
			name:      "one more than a tenth left",
			sale:      saleOf(150, 134, 0),
			at:        inWindow,
			wantState: entity.TicketSaleStateOnSale,
			wantLow:   false,
		},
		{
			// @spec components/entity/ticket-sale "Small sale, last ticket"
			name:      "small sale, last ticket",
			sale:      saleOf(9, 8, 0),
			at:        inWindow,
			wantState: entity.TicketSaleStateOnSale,
			wantLow:   true,
		},
		{
			// @spec components/entity/ticket-sale "Closed"
			name:      "closed at the sale end",
			sale:      saleOf(150, 150, 0),
			at:        time.Date(2026, 11, 20, 18, 0, 0, 0, jst),
			wantState: entity.TicketSaleStateEnded,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.wantState, tt.sale.StateAt(tt.at))
			assert.Equal(t, tt.wantLow, tt.sale.IsLowStockAt(tt.at))
		})
	}
}

func TestTicketSale_ValidatePriceChange(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		sale      *entity.TicketSale
		wantFixed bool
	}{
		{
			// @spec components/entity/ticket-sale "Price change before any ticket is held"
			name: "price change before any ticket is held",
			sale: saleOf(150, 0, 0),
		},
		{
			// @spec components/entity/ticket-sale "Price change while tickets are held"
			name:      "price change while tickets are held",
			sale:      saleOf(150, 0, 2),
			wantFixed: true,
		},
		{
			// @spec components/entity/ticket-sale "Price change after tickets sold"
			name:      "price change after tickets sold",
			sale:      saleOf(150, 1, 0),
			wantFixed: true,
		},
		{
			// @spec components/entity/ticket-sale "Price change after every checkout lapsed"
			name: "price change after every checkout lapsed",
			// The only Reservation expired, so nothing is sold or held.
			sale: saleOf(150, 0, 0),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.sale.ValidatePriceChange(3500)

			if tt.wantFixed {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.NoError(t, tt.sale.ValidatePriceChange(3000), "keeping the price is allowed")
		})
	}
}
