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
