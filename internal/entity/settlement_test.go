package entity_test

import (
	"testing"
	"time"

	"github.com/pannpers/go-apperr/apperr"

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
		assert.ErrorIs(t, s.GiveUpResoldShare(1, 2), apperr.ErrFailedPrecondition)
	})

	t.Run("more resold than issued", func(t *testing.T) {
		t.Parallel()
		assert.ErrorIs(t, held(9500).GiveUpResoldShare(3, 2), apperr.ErrFailedPrecondition)
	})
}

func TestSettlement_ReleasePlan(t *testing.T) {
	t.Parallel()

	const charge = int64(16000)
	tests := []struct {
		name    string
		splits  []entity.SettlementSplit
		wantErr bool
	}{
		{"single organizer split within the charge", []entity.SettlementSplit{{PayeeOrganizerID: "org-1", Amount: 15200}}, false},
		{"split exactly equal to the charge", []entity.SettlementSplit{{PayeeOrganizerID: "org-1", Amount: charge}}, false},
		// @spec components/entity/settlement "Splits exceed the order"
		{"sum of splits exceeds the charge", []entity.SettlementSplit{{PayeeOrganizerID: "org-1", Amount: charge + 1}}, true},
		{"several splits exceed the charge together", []entity.SettlementSplit{{PayeeOrganizerID: "org-1", Amount: 9000}, {PayeeOrganizerID: "org-2", Amount: 9000}}, true},
		// @spec components/entity/settlement "No split"
		{"no splits", nil, true},
		// @spec components/entity/settlement "Non-positive split"
		{"split with zero amount", []entity.SettlementSplit{{PayeeOrganizerID: "org-1", Amount: 0}}, true},
		{"split below zero", []entity.SettlementSplit{{PayeeOrganizerID: "org-1", Amount: -1}}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := &entity.Settlement{Splits: tt.splits}

			plan, err := s.ReleasePlan(charge)

			if tt.wantErr {
				assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
				assert.Empty(t, plan.Splits)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.splits, plan.Splits)
		})
	}

	t.Run("the platform fee is what the splits leave of the charge", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/settlement "Single Organizer split"
		order := &entity.Order{ID: "order-1", Amount: charge}
		s := entity.NewHeldSettlement(order, "org-1", "event-1", 500, time.Now())

		plan, err := s.ReleasePlan(charge)

		require.NoError(t, err)
		require.Len(t, plan.Splits, 1)
		assert.Equal(t, int64(15200), plan.Splits[0].Amount)
		assert.Equal(t, int64(800), charge-plan.Splits[0].Amount, "the platform fee")
	})
}

func TestSettlement_Status(t *testing.T) {
	t.Parallel()

	for status, want := range map[entity.SettlementStatus][2]bool{
		entity.SettlementStatusHeld:     {false, false},
		entity.SettlementStatusReleased: {false, true},
		entity.SettlementStatusReversed: {true, false},
	} {
		s := &entity.Settlement{Status: status}
		assert.Equal(t, want[0], s.IsReversed(), status.String())
		assert.Equal(t, want[1], s.HasPaidOut(), status.String())
	}
}
