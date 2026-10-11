package entity_test

import (
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/stretchr/testify/assert"
)

// firstComeSale is a FirstCome sale open from 2026-11-01 10:00 to 2026-11-20
// 18:00 JST.
func firstComeSale() *entity.TicketSale {
	return entity.NewTicketSale("series-1", "一般発売", entity.TicketSaleMethodFirstCome, nov(1, 10, 0), nov(20, 18, 0))
}

// stockOf returns a first-come TicketType with the given quantity, sold count
// and held count, priced at 3000 yen.
func stockOf(quantity, sold, held int) *entity.TicketType {
	return &entity.TicketType{
		Price:           3000,
		Quantity:        quantity,
		PerAccountLimit: 4,
		SoldCount:       sold,
		HeldCount:       held,
	}
}

func TestTicketType_Validate(t *testing.T) {
	t.Parallel()

	type args struct {
		price    int64
		quantity int
		limit    int
	}
	tests := []struct {
		name  string
		args  args
		valid bool
	}{
		{
			// @spec components/entity/ticket-type "Valid sizing"
			name:  "valid sizing",
			args:  args{price: 8000, quantity: 100, limit: 4},
			valid: true,
		},
		{
			// @spec components/entity/ticket-type "Limit larger than the quantity"
			name: "limit larger than the quantity",
			args: args{price: 8000, quantity: 3, limit: 4},
		},
		{
			// @spec components/entity/ticket-type "Limit larger than one admission code"
			name: "limit larger than one admission code",
			args: args{price: 8000, quantity: 100, limit: 11},
		},
		{
			// @spec components/entity/ticket-type "Price out of range"
			name: "price of 0 yen",
			args: args{price: 0, quantity: 100, limit: 4},
		},
		{
			name: "price of 1,000,001 yen",
			args: args{price: 1_000_001, quantity: 100, limit: 4},
		},
		{
			name:  "price of 1,000,000 yen",
			args:  args{price: 1_000_000, quantity: 100, limit: 4},
			valid: true,
		},
		{
			name: "quantity of 0",
			args: args{price: 8000, quantity: 0, limit: 4},
		},
		{
			name: "negative limit",
			args: args{price: 8000, quantity: 100, limit: -1},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ticketType := entity.NewTicketType(firstComeSale(), "event-1", tt.args.price, tt.args.quantity, tt.args.limit)
			err := ticketType.Validate()

			if tt.valid {
				assert.NoError(t, err)
				return
			}
			assert.Error(t, err)
		})
	}
}

func TestNewTicketType(t *testing.T) {
	t.Parallel()

	// @spec components/entity/ticket-type "No limit given"
	sale := firstComeSale()
	ticketType := entity.NewTicketType(sale, "event-1", 8000, 100, 0)

	assert.Equal(t, 4, ticketType.PerAccountLimit)
	assert.Zero(t, ticketType.SoldCount)
	assert.Equal(t, sale.ID, ticketType.TicketSaleID)
	assert.Equal(t, []*entity.TicketType{ticketType}, sale.TicketTypes)
	assert.NoError(t, ticketType.Validate())
}

func TestTicketType_AmountFor(t *testing.T) {
	t.Parallel()

	// @spec components/entity/ticket-type "Two tickets"
	ticketType := &entity.TicketType{Price: 8000}

	assert.Equal(t, int64(16000), ticketType.AmountFor(2))
}

func TestTicketType_FitsPerAccountLimit(t *testing.T) {
	t.Parallel()

	ticketType := &entity.TicketType{PerAccountLimit: 4}
	tests := []struct {
		name      string
		held      int
		requested int
		want      bool
	}{
		// @spec components/entity/ticket-type "Presale winner in a general sale with the same limit"
		{name: "presale winner in a general sale with the same limit", held: 4, requested: 1, want: false},
		// @spec components/entity/ticket-type "Topping up to a higher limit"
		{name: "topping up to a higher limit", held: 2, requested: 2, want: true},
		{name: "first purchase at the limit", held: 0, requested: 4, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, ticketType.FitsPerAccountLimit(tt.held, tt.requested))
		})
	}
}

func TestTicketType_ValidateTicketCount(t *testing.T) {
	t.Parallel()

	ticketType := &entity.TicketType{PerAccountLimit: 4}

	assert.NoError(t, ticketType.ValidateTicketCount(1))
	assert.NoError(t, ticketType.ValidateTicketCount(4))
	assert.Error(t, ticketType.ValidateTicketCount(0))
	assert.Error(t, ticketType.ValidateTicketCount(5))
}

func TestTicketType_Remaining(t *testing.T) {
	t.Parallel()

	// @spec components/entity/ticket-type "Some sold, some held"
	assert.Equal(t, 44, stockOf(150, 100, 6).Remaining())
	assert.Equal(t, 0, stockOf(150, 148, 6).Remaining(), "never below 0")
}

func TestTicketType_StateAt(t *testing.T) {
	t.Parallel()

	sale := firstComeSale()
	inWindow := nov(5, 12, 0)

	tests := []struct {
		name       string
		ticketType *entity.TicketType
		at         time.Time
		wantState  entity.TicketTypeState
		wantLow    bool
	}{
		{
			// @spec components/entity/ticket-type "Before opening"
			name:       "before opening",
			ticketType: stockOf(150, 0, 0),
			at:         nov(1, 9, 59),
			wantState:  entity.TicketTypeStateNotYetOnSale,
		},
		{
			// @spec components/entity/ticket-type "Last tickets held by others"
			name:       "last tickets held by others",
			ticketType: stockOf(150, 147, 3),
			at:         inWindow,
			wantState:  entity.TicketTypeStateAllHeld,
		},
		{
			// @spec components/entity/ticket-type "Sold out"
			name:       "sold out",
			ticketType: stockOf(150, 150, 0),
			at:         inWindow,
			wantState:  entity.TicketTypeStateSoldOut,
		},
		{
			// @spec components/entity/ticket-type "Few left"
			name:       "few left",
			ticketType: stockOf(150, 135, 0),
			at:         inWindow,
			wantState:  entity.TicketTypeStateOnSale,
			wantLow:    true,
		},
		{
			name:       "one more than a tenth left",
			ticketType: stockOf(150, 134, 0),
			at:         inWindow,
			wantState:  entity.TicketTypeStateOnSale,
			wantLow:    false,
		},
		{
			// @spec components/entity/ticket-type "Small sale, last ticket"
			name:       "small sale, last ticket",
			ticketType: stockOf(9, 8, 0),
			at:         inWindow,
			wantState:  entity.TicketTypeStateOnSale,
			wantLow:    true,
		},
		{
			// @spec components/entity/ticket-type "Closed"
			name:       "closed at the sale end",
			ticketType: stockOf(150, 150, 0),
			at:         nov(20, 18, 0),
			wantState:  entity.TicketTypeStateEnded,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.wantState, tt.ticketType.StateAt(sale, tt.at))
			assert.Equal(t, tt.wantLow, tt.ticketType.IsLowStockAt(sale, tt.at))
		})
	}
}

func TestTicketType_ValidatePriceChange(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		ticketType *entity.TicketType
		wantFixed  bool
	}{
		{
			// @spec components/entity/ticket-type "Price change before any ticket is held"
			name:       "price change before any ticket is held",
			ticketType: stockOf(150, 0, 0),
		},
		{
			// @spec components/entity/ticket-type "Price change while tickets are held"
			name:       "price change while tickets are held",
			ticketType: stockOf(150, 0, 2),
			wantFixed:  true,
		},
		{
			// @spec components/entity/ticket-type "Price change after tickets sold"
			name:       "price change after tickets sold",
			ticketType: stockOf(150, 1, 0),
			wantFixed:  true,
		},
		{
			// @spec components/entity/ticket-type "Price change after every checkout lapsed"
			name: "price change after every checkout lapsed",
			// The only Checkout expired, so nothing is sold or held.
			ticketType: stockOf(150, 0, 0),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.ticketType.ValidatePriceChange(3500)

			if tt.wantFixed {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.NoError(t, tt.ticketType.ValidatePriceChange(3000), "keeping the price is allowed")
		})
	}
}
