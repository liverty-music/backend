package geo_test

import (
	"testing"

	"github.com/liverty-music/backend/internal/infrastructure/geo"
	"github.com/stretchr/testify/assert"
)

// @spec components/entity/concert/search "Prefecture in Japanese"
// @spec components/entity/concert/search "Prefecture in English"
// @spec components/entity/concert/search "Overseas venue"
// @spec components/entity/concert/search "Unrecognized area"
func TestNormalizeAdminArea(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want *string
	}{
		{name: "Prefecture in Japanese", in: "愛知県", want: new("JP-23")},
		{name: "Prefecture in English", in: "tokyo", want: new("JP-13")},
		{name: "Overseas venue", in: "TW-TPE", want: new("TW-TPE")},
		{name: "Unrecognized area", in: "California", want: nil},
		{name: "japanese prefecture name", in: "東京都", want: new("JP-13")},
		{name: "japanese prefecture without suffix", in: "大阪", want: new("JP-27")},
		{name: "english prefecture name", in: "Hokkaido", want: new("JP-01")},
		{name: "iso code passes through", in: "JP-13", want: new("JP-13")},
		{name: "lower-case iso code is upper-cased", in: " us-ca ", want: new("US-CA")},
		{name: "empty", in: "  ", want: nil},
		{name: "unknown free text", in: "台北市", want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, geo.NormalizeAdminArea(tt.in))
		})
	}
}
