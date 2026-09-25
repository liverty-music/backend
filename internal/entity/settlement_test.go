package entity_test

import (
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/stretchr/testify/assert"
)

// TestPlatformFee verifies the platform's flat 5% fee formula (floor(amount *
// 5 / 100)), per the business decision at liverty-music/specification#778.
func TestPlatformFee(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		args          int64 // Order amount in whole yen.
		wantFee       int64
		wantOrganizer int64 // amount - fee: the Organizer's Settlement split.
	}{
		{
			// @spec components/entity/settlement "Round order amount"
			name:          "round order amount",
			args:          10000,
			wantFee:       500,
			wantOrganizer: 9500,
		},
		{
			// @spec components/entity/settlement "Amount that does not divide evenly"
			name:          "amount that does not divide evenly",
			args:          3333,
			wantFee:       166,
			wantOrganizer: 3167,
		},
		{
			// @spec components/entity/settlement "Fee rounds down to zero"
			name:          "fee rounds down to zero at the lower bound",
			args:          19,
			wantFee:       0,
			wantOrganizer: 19,
		},
		{
			// Extra rigor for the same scenario at the opposite edge (smallest
			// positive amount), not a distinct spec scenario.
			name:          "fee rounds down to zero for the smallest positive amount",
			args:          1,
			wantFee:       0,
			wantOrganizer: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fee := entity.PlatformFee(tt.args)

			assert.Equal(t, tt.wantFee, fee)
			assert.Equal(t, tt.wantOrganizer, tt.args-fee)
		})
	}
}
