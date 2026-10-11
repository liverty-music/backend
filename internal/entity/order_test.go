package entity_test

import (
	"testing"

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
