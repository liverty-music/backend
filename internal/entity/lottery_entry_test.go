package entity_test

import (
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/stretchr/testify/assert"
)

func TestNewLotteryEntry(t *testing.T) {
	t.Parallel()

	// @spec components/entity/lottery-entry "New entry"
	code := entity.ReferralCode("member-a")
	e := entity.NewLotteryEntry("type-1", "user-1", 2, "pi_1", &code)

	assert.NotEmpty(t, e.ID)
	assert.Equal(t, entity.LotteryEntryStateEntered, e.State)
	assert.Equal(t, "pi_1", e.CardAuthorizationID)
	assert.Nil(t, e.DrawPosition)
	assert.Equal(t, entity.TicketTypeID("type-1"), e.TicketTypeID)
	assert.Equal(t, entity.UserID("user-1"), e.UserID)
	assert.Equal(t, 2, e.TicketCount)
	assert.Equal(t, &code, e.ReferralCode)
}

func TestLotteryEntry_TicketCount(t *testing.T) {
	t.Parallel()

	ticketType := &entity.TicketType{PerAccountLimit: 4}
	tests := []struct {
		name  string
		count int
		valid bool
	}{
		// @spec components/entity/lottery-entry "Count within the limit"
		{name: "count within the limit", count: 2, valid: true},
		// @spec components/entity/lottery-entry "Zero tickets"
		{name: "zero tickets requested", count: 0},
		// @spec components/entity/lottery-entry "Count over the limit"
		{name: "count over the limit", count: 5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := ticketType.ValidateTicketCount(tt.count)

			if tt.valid {
				assert.NoError(t, err)
				return
			}
			assert.Error(t, err)
		})
	}
}

func TestLotteryEntryState_Values(t *testing.T) {
	t.Parallel()

	// The numbers are stored and sent on the wire; they must match the proto
	// enum LotteryEntryState and the DB CHECK.
	assert.EqualValues(t, 1, entity.LotteryEntryStateEntered)
	assert.EqualValues(t, 2, entity.LotteryEntryStateWon)
	assert.EqualValues(t, 3, entity.LotteryEntryStateLost)
}
