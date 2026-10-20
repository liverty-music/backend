package entity_test

import (
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

// TestNewHeldSettlement verifies that the settlement's split is fixed at
// creation.
func TestNewHeldSettlement(t *testing.T) {
	t.Parallel()

	// @spec components/entity/settlement "Rate changed after issuance"
	org := &entity.Organizer{ID: "org-1", PlatformFeeRateBps: 500}
	order := &entity.Order{ID: "order-1", Amount: 10000}
	s := entity.NewHeldSettlement(order, org.ID, "event-1", org.PlatformFeeRateBps, time.Now())
	org.PlatformFeeRateBps = 800

	assert.Equal(t, int64(9500), s.Splits[0].Amount, "the split stays at the 5% fee")
	assert.Equal(t, entity.SettlementStatusHeld, s.Status)
	assert.Equal(t, order.ID, s.OrderID)
}

func TestSettlement_GiveUpResoldShare(t *testing.T) {
	t.Parallel()

	held := func(split int64) *entity.Settlement {
		return &entity.Settlement{
			Status: entity.SettlementStatusHeld,
			Splits: []entity.SettlementSplit{{PayeeOrganizerID: "org-1", Amount: split}},
		}
	}

	t.Run("one of two tickets resold", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/settlement "One of two tickets resold"
		s := held(9500)
		require.NoError(t, s.GiveUpResoldShare(1, 2))
		assert.Equal(t, int64(4750), s.Splits[0].Amount)
		assert.Equal(t, entity.SettlementStatusHeld, s.Status)
	})

	t.Run("one of three tickets resold", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/settlement "One of three tickets resold"
		s := held(9200)
		require.NoError(t, s.GiveUpResoldShare(1, 3))
		assert.Equal(t, int64(6134), s.Splits[0].Amount)
	})

	t.Run("last ticket resold", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/settlement "Last ticket resold"
		s := held(4750)
		require.NoError(t, s.GiveUpResoldShare(1, 1))
		assert.Equal(t, entity.SettlementStatusReversed, s.Status)
		assert.Equal(t, int64(4750), s.Splits[0].Amount, "a Reversed settlement keeps its split")
	})

	t.Run("not held", func(t *testing.T) {
		t.Parallel()
		s := held(9500)
		s.Status = entity.SettlementStatusReleased
		assert.Error(t, s.GiveUpResoldShare(1, 2))
	})

	t.Run("more resold than issued", func(t *testing.T) {
		t.Parallel()
		assert.Error(t, held(9500).GiveUpResoldShare(3, 2))
	})
}
