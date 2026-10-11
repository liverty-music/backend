package entity_test

import (
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/stretchr/testify/assert"
)

func TestNewLotteryEntry(t *testing.T) {
	t.Parallel()

	// @spec components/entity/lottery-entry "New entry"
	e := entity.NewLotteryEntry("type-1", "user-1", 2)

	assert.NotEmpty(t, e.ID)
	assert.Equal(t, entity.LotteryEntryStateDraft, e.State)
	assert.False(t, e.HasCardAuthorization())
	assert.Nil(t, e.DrawPosition)
	assert.Equal(t, entity.TicketTypeID("type-1"), e.TicketTypeID)
	assert.Equal(t, entity.UserID("user-1"), e.UserID)
	assert.Equal(t, 2, e.TicketCount)
}

func TestLotteryEntry_IsConfirmable(t *testing.T) {
	t.Parallel()

	hold := "pi_1"
	tests := []struct {
		name  string
		entry *entity.LotteryEntry
		want  bool
	}{
		{
			// @spec components/entity/lottery-entry "Authorized draft"
			name:  "authorized draft",
			entry: &entity.LotteryEntry{State: entity.LotteryEntryStateDraft, CardAuthorizationID: hold},
			want:  true,
		},
		{
			// @spec components/entity/lottery-entry "Draft without a hold"
			name:  "draft without a hold",
			entry: &entity.LotteryEntry{State: entity.LotteryEntryStateDraft},
		},
		// @spec components/entity/lottery-entry "Already entered"
		{name: "entered", entry: &entity.LotteryEntry{State: entity.LotteryEntryStateEntered, CardAuthorizationID: hold}},
		{name: "won", entry: &entity.LotteryEntry{State: entity.LotteryEntryStateWon, CardAuthorizationID: hold}},
		{name: "lost", entry: &entity.LotteryEntry{State: entity.LotteryEntryStateLost, CardAuthorizationID: hold}},
		{name: "withdrawn", entry: &entity.LotteryEntry{State: entity.LotteryEntryStateWithdrawn, CardAuthorizationID: hold}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, tt.entry.IsConfirmable())
		})
	}
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
		// @spec components/entity/lottery-entry "Zero tickets requested"
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

func TestLotteryEntry_IsActive(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		state entity.LotteryEntryState
		want  bool
	}{
		// @spec components/entity/lottery-entry "Withdrawn entry is not active"
		{name: "withdrawn", state: entity.LotteryEntryStateWithdrawn, want: false},
		// @spec components/entity/lottery-entry "Draft or drawn entry is active"
		{name: "draft", state: entity.LotteryEntryStateDraft, want: true},
		{name: "won", state: entity.LotteryEntryStateWon, want: true},
		{name: "lost", state: entity.LotteryEntryStateLost, want: true},
		{name: "entered", state: entity.LotteryEntryStateEntered, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, (&entity.LotteryEntry{State: tt.state}).IsActive())
		})
	}
}

func TestLotteryEntry_IsWithdrawable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		state entity.LotteryEntryState
		want  bool
	}{
		// @spec components/entity/lottery-entry "Draft or entered entry"
		{name: "draft", state: entity.LotteryEntryStateDraft, want: true},
		{name: "entered", state: entity.LotteryEntryStateEntered, want: true},
		// @spec components/entity/lottery-entry "Drawn or withdrawn entry"
		{name: "won", state: entity.LotteryEntryStateWon, want: false},
		{name: "lost", state: entity.LotteryEntryStateLost, want: false},
		{name: "withdrawn", state: entity.LotteryEntryStateWithdrawn, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, (&entity.LotteryEntry{State: tt.state}).IsWithdrawable())
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
	assert.EqualValues(t, 4, entity.LotteryEntryStateWithdrawn)
	assert.EqualValues(t, 5, entity.LotteryEntryStateDraft)
}
