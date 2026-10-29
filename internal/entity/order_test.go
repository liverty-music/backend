package entity_test

import (
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/stretchr/testify/assert"
)

func TestOrder_ValidateSource(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		order   *entity.Order
		wantErr bool
	}{
		{
			// @spec components/entity/order "Order from a checkout"
			name:  "order from a checkout",
			order: &entity.Order{CheckoutID: "checkout-1"},
		},
		{
			// @spec components/entity/order "Order from a won lottery entry"
			name:  "order from a won lottery entry",
			order: &entity.Order{LotteryEntryID: "entry-1"},
		},
		{
			// @spec components/entity/order "Two sources"
			name:    "two sources",
			order:   &entity.Order{LotteryEntryID: "entry-1", CheckoutID: "checkout-1"},
			wantErr: true,
		},
		{
			name:    "no source",
			order:   &entity.Order{},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.order.ValidateSource()

			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
		})
	}
}

func TestNewPendingOrder(t *testing.T) {
	t.Parallel()

	// @spec components/entity/order "New order"
	stock := entity.NewTicketStock("event-1", "seat-s", 150)
	stock.SeatType = &entity.SeatType{ID: "seat-s", Name: "S席", Price: 8000}

	order := entity.NewPendingOrder("user-1", stock, 2, nil, entity.PaymentProviderStripe)

	assert.Equal(t, entity.OrderStatusPending, order.Status)
	assert.Equal(t, int64(16000), order.Amount)
	assert.Equal(t, stock.ID, order.TicketStockID)
	assert.Empty(t, order.Payment.ProviderPaymentID)
	assert.Nil(t, order.PayTime)
	assert.Empty(t, order.ProviderRefundID)
}

func TestValidateTicketCount(t *testing.T) {
	t.Parallel()

	limit := 4
	event := &entity.Event{PerAccountLimit: &limit}
	tests := []struct {
		name  string
		count int
		valid bool
	}{
		// @spec components/entity/order "Count within the limit"
		{name: "2 of a limit of 4", count: 2, valid: true},
		// @spec components/entity/order "Zero tickets"
		{name: "0 tickets", count: 0},
		// @spec components/entity/order "Count over the limit"
		{name: "5 of a limit of 4", count: 5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := entity.ValidateTicketCount(tt.count, event)
			if tt.valid {
				assert.NoError(t, err)
			} else {
				assert.Error(t, err)
			}
		})
	}
}

func TestOrder_CanBePaid(t *testing.T) {
	t.Parallel()

	paid := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)

	t.Run("paid order", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/order "Paid order"
		order := &entity.Order{CardAuthorizationID: "pi_1", Payment: entity.Payment{ProviderPaymentID: "pi_1"}, PayTime: &paid}
		assert.True(t, order.CanBePaid())
	})

	t.Run("paid without a payment", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/order "Paid without a payment"
		order := &entity.Order{CardAuthorizationID: "pi_1", PayTime: &paid}
		assert.False(t, order.CanBePaid())
	})
}

func TestOrder_CanBecomeVoid(t *testing.T) {
	t.Parallel()

	t.Run("pending order", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/order "Pending order"
		assert.True(t, (&entity.Order{Status: entity.OrderStatusPending}).CanBecomeVoid())
	})

	t.Run("paid order", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/order "Paid order"
		assert.False(t, (&entity.Order{Status: entity.OrderStatusPaid}).CanBecomeVoid())
	})
}
