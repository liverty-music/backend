package geo_test

import (
	"testing"

	"github.com/liverty-music/backend/pkg/geo"
	"github.com/stretchr/testify/assert"
)

func TestHaversine(t *testing.T) {
	t.Parallel()

	type args struct {
		lat1, lng1 float64
		lat2, lng2 float64
	}
	tests := []struct {
		name          string
		args          args
		wantApproxKm  float64
		toleranceKm   float64
	}{
		{
			name: "same point returns zero distance",
			args: args{
				lat1: 35.6895, lng1: 139.6917,
				lat2: 35.6895, lng2: 139.6917,
			},
			wantApproxKm: 0.0,
			toleranceKm:  0.001,
		},
		{
			name: "Tokyo to Osaka is approximately 400 km",
			args: args{
				lat1: 35.6895, lng1: 139.6917, // Tokyo
				lat2: 34.6937, lng2: 135.5023, // Osaka
			},
			wantApproxKm: 400.0,
			toleranceKm:  10.0,
		},
		{
			name: "antipodal points return approximately half Earth circumference",
			args: args{
				lat1: 0.0, lng1: 0.0,
				lat2: 0.0, lng2: 180.0,
			},
			wantApproxKm: 20015.0, // half of 40030 km equatorial circumference
			toleranceKm:  10.0,
		},
		{
			name: "North Pole to South Pole is approximately 20015 km",
			args: args{
				lat1: 90.0, lng1: 0.0,
				lat2: -90.0, lng2: 0.0,
			},
			wantApproxKm: 20015.0,
			toleranceKm:  10.0,
		},
		{
			name: "New York to London is approximately 5570 km",
			args: args{
				lat1: 40.7128, lng1: -74.0060, // New York
				lat2: 51.5074, lng2: -0.1278,  // London
			},
			wantApproxKm: 5570.0,
			toleranceKm:  20.0,
		},
		{
			name: "result is symmetric — swapping points gives same distance",
			args: args{
				lat1: 34.6937, lng1: 135.5023, // Osaka
				lat2: 35.6895, lng2: 139.6917, // Tokyo
			},
			wantApproxKm: 400.0,
			toleranceKm:  10.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := geo.Haversine(tt.args.lat1, tt.args.lng1, tt.args.lat2, tt.args.lng2)

			assert.InDelta(t, tt.wantApproxKm, got, tt.toleranceKm,
				"expected ~%.1f km, got %.4f km", tt.wantApproxKm, got)
		})
	}
}

func TestHaversine_Symmetry(t *testing.T) {
	t.Parallel()

	// The Haversine formula must be symmetric: d(A,B) == d(B,A).
	d1 := geo.Haversine(35.6895, 139.6917, 34.6937, 135.5023)
	d2 := geo.Haversine(34.6937, 135.5023, 35.6895, 139.6917)

	assert.InDelta(t, d1, d2, 0.0001)
}
