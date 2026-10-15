package entity_test

import (
	"strings"
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/stretchr/testify/assert"
)

func TestReferralCode_Validate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		code    entity.ReferralCode
		wantErr bool
	}{
		// @spec components/entity/referral "Code from a link"
		{name: "code from a link", code: "member-a"},
		{name: "32 characters", code: entity.ReferralCode(strings.Repeat("a", 32))},
		{name: "digits only", code: "2026"},
		// @spec components/entity/referral "Empty or too long"
		{name: "empty", code: "", wantErr: true},
		{name: "33 characters", code: entity.ReferralCode(strings.Repeat("a", 33)), wantErr: true},
		// @spec components/entity/referral "Other characters"
		{name: "underscore", code: "member_a", wantErr: true},
		{name: "non-ASCII", code: "メンバーA", wantErr: true},
		{name: "space", code: "a b", wantErr: true},
		{name: "uppercase", code: "Member-A", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.code.Validate()

			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
		})
	}
}

func TestReferralCode_OneCodeWhateverTheCase(t *testing.T) {
	t.Parallel()
	// @spec components/entity/referral "One code whatever the case in links"
	// The fan app lowercases a link's code before it keeps and sends it, so
	// both links yield the one valid code the server records.
	fromUpper := entity.ReferralCode(strings.ToLower("Member-A"))
	fromLower := entity.ReferralCode(strings.ToLower("member-a"))

	assert.NoError(t, fromUpper.Validate())
	assert.Equal(t, entity.ReferralCode("member-a"), fromUpper)
	assert.Equal(t, fromUpper, fromLower)
}

func TestReferral_Validate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		source  entity.ReferralSource
		wantErr bool
	}{
		// @spec components/entity/referral "Referral of a checkout"
		{name: "referral of a checkout", source: entity.ReferralSource{CheckoutID: "c-1"}},
		{name: "referral of a lottery entry", source: entity.ReferralSource{LotteryEntryID: "le-1"}},
		// @spec components/entity/referral "Two sources"
		{name: "two sources", source: entity.ReferralSource{CheckoutID: "c-1", LotteryEntryID: "le-1"}, wantErr: true},
		{name: "no source", source: entity.ReferralSource{}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := &entity.Referral{Source: tt.source, Code: "member-a"}

			err := r.Validate()

			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
		})
	}

	t.Run("malformed code", func(t *testing.T) {
		t.Parallel()
		r := &entity.Referral{Source: entity.ReferralSource{CheckoutID: "c-1"}, Code: "member_a"}

		assert.Error(t, r.Validate())
	})
}

func TestCheckoutStatus_AcceptsReferral(t *testing.T) {
	t.Parallel()

	tests := []struct {
		status entity.CheckoutStatus
		want   bool
	}{
		// @spec components/entity/referral "Held checkout"
		{status: entity.CheckoutStatusHeld, want: true},
		// @spec components/entity/referral "Confirmed source"
		{status: entity.CheckoutStatusCommitted, want: false},
		{status: entity.CheckoutStatusCompleted, want: false},
		{status: entity.CheckoutStatusExpired, want: false},
		{status: entity.CheckoutStatusReleased, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.status.String(), func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, tt.status.AcceptsReferral())
		})
	}
}

func TestLotteryEntryState_AcceptsReferral(t *testing.T) {
	t.Parallel()

	tests := []struct {
		state entity.LotteryEntryState
		want  bool
	}{
		// @spec components/entity/referral "Draft entry"
		{state: entity.LotteryEntryStateDraft, want: true},
		// @spec components/entity/referral "Confirmed source"
		{state: entity.LotteryEntryStateEntered, want: false},
		{state: entity.LotteryEntryStateWon, want: false},
		{state: entity.LotteryEntryStateLost, want: false},
		{state: entity.LotteryEntryStateWithdrawn, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.state.String(), func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, tt.state.AcceptsReferral())
		})
	}
}
