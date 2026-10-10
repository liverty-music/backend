package entity_test

import (
	"strings"
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/stretchr/testify/assert"
)

func TestHolderIdentity_Validate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		identity entity.HolderIdentity
		wantErr  bool
	}{
		{
			// @spec components/entity/user "Saved identity"
			name:     "saved identity",
			identity: entity.HolderIdentity{FullName: "山田 花子", PhoneNumber: "+819012345678"},
		},
		{
			name:     "name at 200 characters",
			identity: entity.HolderIdentity{FullName: strings.Repeat("名", 200), PhoneNumber: "+819012345678"},
		},
		{
			// @spec components/entity/user "Domestic-format phone number"
			name:     "domestic-format phone number",
			identity: entity.HolderIdentity{FullName: "山田 花子", PhoneNumber: "09012345678"},
			wantErr:  true,
		},
		{
			name:     "empty name",
			identity: entity.HolderIdentity{FullName: "", PhoneNumber: "+819012345678"},
			wantErr:  true,
		},
		{
			name:     "name over 200 characters",
			identity: entity.HolderIdentity{FullName: strings.Repeat("名", 201), PhoneNumber: "+819012345678"},
			wantErr:  true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.identity.Validate()

			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
		})
	}
}
