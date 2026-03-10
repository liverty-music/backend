package geo

import (
	"math"
	"testing"
)

func TestHaversine(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		lat1      float64
		lng1      float64
		lat2      float64
		lng2      float64
		wantKm    float64
		tolerance float64
	}{
		{
			name: "Tokyo to Saitama (short distance)",
			lat1: 35.6894, lng1: 139.6917, // Tokyo
			lat2: 35.8569, lng2: 139.6489, // Saitama
			wantKm:    19.0,
			tolerance: 2.0,
		},
		{
			name: "Tokyo to Osaka (medium distance)",
			lat1: 35.6894, lng1: 139.6917, // Tokyo
			lat2: 34.6863, lng2: 135.5200, // Osaka
			wantKm:    397.0,
			tolerance: 10.0,
		},
		{
			name: "Tokyo to Sapporo (long distance)",
			lat1: 35.6894, lng1: 139.6917, // Tokyo
			lat2: 43.0642, lng2: 141.3469, // Sapporo
			wantKm:    831.0,
			tolerance: 20.0,
		},
		{
			name: "same point returns zero",
			lat1: 35.6894, lng1: 139.6917,
			lat2: 35.6894, lng2: 139.6917,
			wantKm:    0.0,
			tolerance: 0.001,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := Haversine(tt.lat1, tt.lng1, tt.lat2, tt.lng2)
			if math.Abs(got-tt.wantKm) > tt.tolerance {
				t.Errorf("Haversine() = %.2f km, want %.2f km (±%.1f)", got, tt.wantKm, tt.tolerance)
			}
		})
	}
}
