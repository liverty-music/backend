package pb_test

import (
	"testing"

	"github.com/liverty-music/backend/internal/adapter/pb"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/stretchr/testify/assert"
)

// TestFromOrder_Source checks that the Order's source is sent as the one of
// checkout_id, lottery_entry_id and resale_entry_id that the Order names.
func TestFromOrder_Source(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		order        *entity.Order
		wantCheckout string
		wantLottery  string
		wantResale   string
	}{
		{
			name:         "send a Checkout as checkout_id",
			order:        &entity.Order{ID: "order-1", CheckoutID: "checkout-1"},
			wantCheckout: "checkout-1",
		},
		{
			name:        "send a LotteryEntry as lottery_entry_id",
			order:       &entity.Order{ID: "order-1", LotteryEntryID: "lottery-entry-1"},
			wantLottery: "lottery-entry-1",
		},
		{
			name:       "send a ResaleEntry as resale_entry_id, not as an empty lottery entry",
			order:      &entity.Order{ID: "order-1", ResaleEntryID: "resale-entry-1"},
			wantResale: "resale-entry-1",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := pb.FromOrder(tt.order)

			assert.True(t, got.HasSource())
			assert.Equal(t, tt.wantCheckout != "", got.HasCheckoutId())
			assert.Equal(t, tt.wantLottery != "", got.HasLotteryEntryId())
			assert.Equal(t, tt.wantResale != "", got.HasResaleEntryId())
			assert.Equal(t, tt.wantCheckout, got.GetCheckoutId().GetValue())
			assert.Equal(t, tt.wantLottery, got.GetLotteryEntryId().GetValue())
			assert.Equal(t, tt.wantResale, got.GetResaleEntryId().GetValue())
		})
	}
}
