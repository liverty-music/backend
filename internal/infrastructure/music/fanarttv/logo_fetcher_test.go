package fanarttv

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateLogoURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{
			name: "valid assets.fanart.tv URL",
			url:  "https://assets.fanart.tv/fanart/music/logo.png",
		},
		{
			name: "valid fanart.tv root domain",
			url:  "https://fanart.tv/some/path",
		},
		{
			name: "valid subdomain of fanart.tv",
			url:  "https://cdn.assets.fanart.tv/img.png",
		},
		{
			name:    "HTTP scheme rejected",
			url:     "http://assets.fanart.tv/logo.png",
			wantErr: true,
		},
		{
			name:    "internal IP rejected",
			url:     "https://169.254.169.254/metadata",
			wantErr: true,
		},
		{
			name:    "different host rejected",
			url:     "https://evil.com/logo.png",
			wantErr: true,
		},
		{
			name:    "suffix trick rejected",
			url:     "https://notfanart.tv/logo.png",
			wantErr: true,
		},
		{
			name:    "empty URL rejected",
			url:     "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateLogoURL(tt.url)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			assert.NoError(t, err)
		})
	}
}
