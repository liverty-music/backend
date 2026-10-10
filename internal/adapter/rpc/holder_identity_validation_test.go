package rpc_test

import (
	"strings"
	"testing"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	"buf.build/go/protovalidate"
	"github.com/stretchr/testify/assert"
)

// TestHolderIdentity_Validation pins the proto rules on HolderIdentity —
// the identity the lottery boundary validates before any usecase runs, and the
// one a won application copies onto its ticket holder.
func TestHolderIdentity_Validation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		identity  *entityv1.HolderIdentity
		wantValid bool
	}{
		{
			// @spec components/entity/ticket-application "Identity complete"
			name:      "valid with a full name and an E.164 phone number",
			identity:  &entityv1.HolderIdentity{FullName: "山田太郎", PhoneNumber: "+819012345678"},
			wantValid: true,
		},
		{
			// @spec components/entity/ticket-application "Identity complete"
			name:      "valid with a full name at the 200-character limit",
			identity:  &entityv1.HolderIdentity{FullName: strings.Repeat("名", 200), PhoneNumber: "+819012345678"},
			wantValid: true,
		},
		{
			// @spec components/entity/ticket-application "Missing name or phone"
			name:      "invalid when the full name is empty",
			identity:  &entityv1.HolderIdentity{FullName: "", PhoneNumber: "+819012345678"},
			wantValid: false,
		},
		{
			// @spec components/entity/ticket-application "Missing name or phone"
			name:      "invalid when the phone number is empty",
			identity:  &entityv1.HolderIdentity{FullName: "山田太郎", PhoneNumber: ""},
			wantValid: false,
		},
		{
			// @spec components/entity/ticket-application "Domestic-format phone number"
			name:      "invalid when the phone number is domestic with hyphens",
			identity:  &entityv1.HolderIdentity{FullName: "山田太郎", PhoneNumber: "090-1234-5678"},
			wantValid: false,
		},
		{
			// @spec components/entity/ticket-application "Domestic-format phone number"
			name:      "invalid when the phone number is domestic without separators",
			identity:  &entityv1.HolderIdentity{FullName: "山田太郎", PhoneNumber: "09012345678"},
			wantValid: false,
		},
		{
			// @spec components/entity/ticket-application "Phone number too long"
			name:      "invalid when the phone number has 16 digits",
			identity:  &entityv1.HolderIdentity{FullName: "山田太郎", PhoneNumber: "+" + strings.Repeat("8", 16)},
			wantValid: false,
		},
		{
			name:      "valid with the 15-digit E.164 maximum",
			identity:  &entityv1.HolderIdentity{FullName: "山田太郎", PhoneNumber: "+" + strings.Repeat("8", 15)},
			wantValid: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := protovalidate.Validate(tt.identity)

			if tt.wantValid {
				assert.NoError(t, err)
				return
			}
			assert.Error(t, err)
		})
	}
}
