package entity_test

import (
	"strings"
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/stretchr/testify/assert"
)

func TestSeatType_Validate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		seatType *entity.SeatType
		valid    bool
	}{
		{
			// @spec components/entity/seat-type "Valid seat type"
			name:     "S席 at 8000 yen",
			seatType: entity.NewSeatType("series-1", "S席", 8000),
			valid:    true,
		},
		{
			name:     "name of 50 characters",
			seatType: entity.NewSeatType("series-1", strings.Repeat("席", 50), 8000),
			valid:    true,
		},
		{
			// @spec components/entity/seat-type "Empty name"
			name:     "empty name",
			seatType: entity.NewSeatType("series-1", "", 8000),
		},
		{
			name:     "name of 51 characters",
			seatType: entity.NewSeatType("series-1", strings.Repeat("席", 51), 8000),
		},
		{
			// @spec components/entity/seat-type "Price out of range"
			name:     "price of 0 yen",
			seatType: entity.NewSeatType("series-1", "S席", 0),
		},
		{
			// @spec components/entity/seat-type "Price out of range"
			name:     "price of 1,000,001 yen",
			seatType: entity.NewSeatType("series-1", "S席", 1_000_001),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.seatType.Validate()
			if tt.valid {
				assert.NoError(t, err)
			} else {
				assert.Error(t, err)
			}
		})
	}
}

// @spec components/entity/seat-type "Two tickets"
func TestSeatType_AmountFor(t *testing.T) {
	t.Parallel()

	assert.Equal(t, int64(16000), entity.NewSeatType("series-1", "S席", 8000).AmountFor(2))
}
