package geo

import "testing"

func ptrFloat(v float64) *float64 { return &v }
func ptrStr(v string) *string     { return &v }

func TestClassifyLane(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		homeLevel1     string
		venueLat       *float64
		venueLng       *float64
		venueAdminArea *string
		want           Lane
	}{
		{
			name:           "HOME: admin_area matches",
			homeLevel1:     "JP-13",
			venueLat:       ptrFloat(35.6894),
			venueLng:       ptrFloat(139.6917),
			venueAdminArea: ptrStr("JP-13"),
			want:           LaneHome,
		},
		{
			name:           "HOME: admin_area match takes priority over distance",
			homeLevel1:     "JP-13",
			venueLat:       ptrFloat(34.6863), // Osaka coordinates (far away)
			venueLng:       ptrFloat(135.5200),
			venueAdminArea: ptrStr("JP-13"), // but admin_area says Tokyo
			want:           LaneHome,
		},
		{
			name:           "NEARBY: within 200km threshold (Tokyo user, Saitama venue)",
			homeLevel1:     "JP-13",
			venueLat:       ptrFloat(35.8569),
			venueLng:       ptrFloat(139.6489),
			venueAdminArea: ptrStr("JP-11"), // Saitama
			want:           LaneNearby,
		},
		{
			name:           "NEARBY: venue near threshold boundary (Tokyo user, Shizuoka venue)",
			homeLevel1:     "JP-13",
			venueLat:       ptrFloat(34.9769),
			venueLng:       ptrFloat(138.3831),
			venueAdminArea: ptrStr("JP-22"), // Shizuoka
			want:           LaneNearby,
		},
		{
			name:           "AWAY: beyond 200km threshold (Tokyo user, Osaka venue)",
			homeLevel1:     "JP-13",
			venueLat:       ptrFloat(34.6863),
			venueLng:       ptrFloat(135.5200),
			venueAdminArea: ptrStr("JP-27"), // Osaka
			want:           LaneAway,
		},
		{
			name:           "AWAY: missing venue coordinates",
			homeLevel1:     "JP-13",
			venueLat:       nil,
			venueLng:       nil,
			venueAdminArea: ptrStr("JP-27"),
			want:           LaneAway,
		},
		{
			name:           "AWAY: missing venue admin_area with coordinates beyond threshold",
			homeLevel1:     "JP-13",
			venueLat:       ptrFloat(34.6863),
			venueLng:       ptrFloat(135.5200),
			venueAdminArea: nil,
			want:           LaneAway,
		},
		{
			name:           "AWAY: unknown home prefecture code",
			homeLevel1:     "XX-99",
			venueLat:       ptrFloat(35.6894),
			venueLng:       ptrFloat(139.6917),
			venueAdminArea: ptrStr("JP-13"),
			want:           LaneAway,
		},
		{
			name:           "NEARBY: missing admin_area but within distance (Tokyo user, nearby venue)",
			homeLevel1:     "JP-13",
			venueLat:       ptrFloat(35.8569),
			venueLng:       ptrFloat(139.6489),
			venueAdminArea: nil,
			want:           LaneNearby,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := ClassifyLane(tt.homeLevel1, tt.venueLat, tt.venueLng, tt.venueAdminArea)
			if got != tt.want {
				t.Errorf("ClassifyLane() = %q, want %q", got, tt.want)
			}
		})
	}
}
