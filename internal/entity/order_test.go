package entity_test

import (
	"testing"
	"time"

	"github.com/pannpers/go-apperr/apperr"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
			// @spec components/entity/order "Order from a resale entry"
			name:  "order from a resale entry",
			order: &entity.Order{ResaleEntryID: "resale-entry-1"},
		},
		{
			// @spec components/entity/order "Two sources"
			name:    "two sources",
			order:   &entity.Order{LotteryEntryID: "entry-1", ResaleEntryID: "resale-entry-1"},
			wantErr: true,
		},
		{
			name:    "a checkout and a lottery entry",
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
				assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
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
	assert.Nil(t, order.Refund)
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
				assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
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

var orderStatuses = []entity.OrderStatus{entity.OrderStatusPending, entity.OrderStatusPaid, entity.OrderStatusRefunded, entity.OrderStatusVoid}

func TestOrder_IsPaid(t *testing.T) {
	t.Parallel()
	want := map[entity.OrderStatus]bool{
		entity.OrderStatusPending: false, entity.OrderStatusPaid: true, entity.OrderStatusRefunded: true, entity.OrderStatusVoid: false,
	}
	for _, status := range orderStatuses {
		assert.Equal(t, want[status], (&entity.Order{Status: status}).IsPaid(), status.String())
	}
}

func TestOrder_IssuanceOutcome(t *testing.T) {
	t.Parallel()
	tests := []struct {
		status  entity.OrderStatus
		wantErr error
	}{
		{entity.OrderStatusPending, nil},
		{entity.OrderStatusPaid, apperr.ErrAlreadyExists},
		{entity.OrderStatusRefunded, apperr.ErrAlreadyExists},
		{entity.OrderStatusVoid, apperr.ErrFailedPrecondition},
	}
	for _, tt := range tests {
		t.Run(tt.status.String(), func(t *testing.T) {
			t.Parallel()
			err := (&entity.Order{Status: tt.status}).IssuanceOutcome()
			if tt.wantErr == nil {
				assert.NoError(t, err)
				return
			}
			assert.ErrorIs(t, err, tt.wantErr)
		})
	}
}

func TestOrder_CheckChargeable(t *testing.T) {
	t.Parallel()
	for _, status := range orderStatuses {
		err := (&entity.Order{Status: status}).CheckChargeable()
		if status == entity.OrderStatusPending {
			assert.NoError(t, err)
			continue
		}
		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition, status.String())
	}
}

func TestOrder_MarkPaid(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	o := &entity.Order{ID: "order-1", Status: entity.OrderStatusPending}

	o.MarkPaid(&entity.CapturedPayment{Provider: entity.PaymentProviderStripe, ProviderPaymentID: "pi_1", CardBrand: "visa", CardLast4: "4242"}, now)

	assert.Equal(t, entity.OrderStatusPaid, o.Status)
	require.NotNil(t, o.PayTime)
	assert.Equal(t, now, *o.PayTime)
	assert.Equal(t, entity.Payment{Provider: entity.PaymentProviderStripe, ProviderPaymentID: "pi_1", CardBrand: "visa", CardLast4: "4242"}, o.Payment)
}
