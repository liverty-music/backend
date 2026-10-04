package geo_test

import (
	"testing"

	"github.com/liverty-music/backend/internal/infrastructure/geo"
	"github.com/stretchr/testify/assert"
)

func TestNormalizeAdminArea(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want *string
	}{
		{name: "japanese prefecture name", in: "東京都", want: ptr("JP-13")},
		{name: "japanese prefecture without suffix", in: "大阪", want: ptr("JP-27")},
		{name: "english prefecture name", in: "Hokkaido", want: ptr("JP-01")},
		{name: "iso code passes through", in: "JP-13", want: ptr("JP-13")},
		{name: "overseas iso code passes through", in: "TW-TPE", want: ptr("TW-TPE")},
		{name: "lower-case iso code is upper-cased", in: " us-ca ", want: ptr("US-CA")},
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

func ptr(s string) *string { return &s }
