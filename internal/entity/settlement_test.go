package entity_test

import (
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/stretchr/testify/assert"
)

// TestPlatformFee verifies the fee at the Organizer's rate in basis points
// (floor(amount * rate / 10000)), with the Organizer's split as the remainder.
func TestPlatformFee(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		amount        int64 // Order amount in whole yen.
		rateBps       int
		wantFee       int64
		wantOrganizer int64 // amount - fee: the Organizer's Settlement split.
	}{
		{
			// @spec components/entity/settlement "Round order amount"
			name:          "round order amount at 8%",
			amount:        10000,
			rateBps:       800,
			wantFee:       800,
			wantOrganizer: 9200,
		},
		{
			// @spec components/entity/settlement "Pilot rate"
			name:          "pilot rate of 5%",
			amount:        10000,
			rateBps:       500,
			wantFee:       500,
			wantOrganizer: 9500,
		},
		{
			// @spec components/entity/settlement "Amount that does not divide evenly"
			name:          "amount that does not divide evenly",
			amount:        3333,
			rateBps:       800,
			wantFee:       266,
			wantOrganizer: 3067,
		},
		{
			// @spec components/entity/settlement "Fee rounds down to zero"
			name:          "fee rounds down to zero",
			amount:        12,
			rateBps:       800,
			wantFee:       0,
			wantOrganizer: 12,
		},
		{
			name:          "zero rate",
			amount:        10000,
			rateBps:       0,
			wantFee:       0,
			wantOrganizer: 10000,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fee := entity.PlatformFee(tt.amount, tt.rateBps)

			assert.Equal(t, tt.wantFee, fee)
			assert.Equal(t, tt.wantOrganizer, tt.amount-fee)
		})
	}
}

// TestNewHeldSettlement verifies that the settlement keeps the rate it was
// created with.
func TestNewHeldSettlement(t *testing.T) {
	t.Parallel()

	// @spec components/entity/settlement "Rate changed after issuance"
	org := &entity.Organizer{ID: "org-1", PlatformFeeRateBps: 500}
	order := &entity.Order{ID: "order-1", Amount: 10000}
	s := entity.NewHeldSettlement(order, org.ID, "event-1", org.PlatformFeeRateBps, time.Now())
	org.PlatformFeeRateBps = 800

	assert.Equal(t, 500, s.PlatformFeeRateBps)
	assert.Equal(t, int64(9500), s.Splits[0].Amount)
	assert.Equal(t, entity.SettlementStatusHeld, s.Status)
	assert.Equal(t, order.ID, s.OrderID)
}
