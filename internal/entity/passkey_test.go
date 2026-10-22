package entity_test

import (
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/stretchr/testify/assert"
)

func TestPasskey_ProviderName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		aaguid string
		want   string
	}{
		{
			// @spec components/entity/passkey "Known provider"
			name:   "the list's name for a listed AAGUID",
			aaguid: "ea9b8d66-4d01-1d21-3ce4-b6b48cb575d4",
			want:   "Google Password Manager",
		},
		{
			// @spec components/entity/passkey "Provider not revealed or not listed"
			name:   "no name for an all-zero AAGUID",
			aaguid: entity.ZeroAAGUID,
			want:   "",
		},
		{
			// @spec components/entity/passkey "Provider not revealed or not listed"
			name:   "no name for an AAGUID the list does not contain",
			aaguid: "11111111-2222-3333-4444-555555555555",
			want:   "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p := &entity.Passkey{AAGUID: tt.aaguid}

			assert.Equal(t, tt.want, p.ProviderName())
		})
	}
}
