package geo_test

import (
	"testing"

	"github.com/liverty-music/backend/internal/infrastructure/geo"
	"github.com/stretchr/testify/assert"
)

func TestResolveCentroid(t *testing.T) {
	type args struct {
		code string
	}
	tests := []struct {
		name   string
		args   args
		wantOK bool
	}{
		{
			name:   "known JP code JP-13 (Tokyo)",
			args:   args{code: "JP-13"},
			wantOK: true,
		},
		{
			name:   "known JP code JP-01 (Hokkaido)",
			args:   args{code: "JP-01"},
			wantOK: true,
		},
		{
			name:   "known JP code JP-47 (Okinawa)",
			args:   args{code: "JP-47"},
			wantOK: true,
		},
		{
			name:   "unsupported US code",
			args:   args{code: "US-NY"},
			wantOK: false,
		},
		{
			name:   "empty string",
			args:   args{code: ""},
			wantOK: false,
		},
		{
			name:   "invalid code",
			args:   args{code: "XX-99"},
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			coords, ok := geo.ResolveCentroid(tt.args.code)

			assert.Equal(t, tt.wantOK, ok)

			if tt.wantOK {
				assert.NotZero(t, coords.Latitude, "latitude should be non-zero for known code")
				assert.NotZero(t, coords.Longitude, "longitude should be non-zero for known code")
			}
		})
	}
}

func TestResolveCentroid_TokyoValues(t *testing.T) {
	coords, ok := geo.ResolveCentroid("JP-13")
	assert.True(t, ok)
	assert.InDelta(t, 35.6894, coords.Latitude, 0.01)
	assert.InDelta(t, 139.6917, coords.Longitude, 0.01)
}
