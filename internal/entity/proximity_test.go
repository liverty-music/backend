package entity_test

import (
	"testing"

	"github.com/liverty-music/backend/internal/entity"
)

func TestConcert_ProximityTo(t *testing.T) {
	t.Parallel()

	tokyoLat, tokyoLng := 35.6894, 139.6917
	osakaLat, osakaLng := 34.6863, 135.5200
	sapporoLat, sapporoLng := 43.0642, 141.3469

	tokyoLevel1 := "JP-13"
	osakaLevel1 := "JP-27"

	home := &entity.Home{
		ID:          "home-1",
		CountryCode: "JP",
		Level1:      tokyoLevel1,
		Centroid:    &entity.Coordinates{Latitude: tokyoLat, Longitude: tokyoLng},
	}

	tests := []struct {
		name string
		home *entity.Home
		c    *entity.Concert
		want entity.Proximity
	}{
		{
			name: "HOME: venue admin_area matches home level1",
			home: home,
			c: &entity.Concert{
				Event: entity.Event{
					Venue: &entity.Venue{
						AdminArea:   &tokyoLevel1,
						Coordinates: &entity.Coordinates{Latitude: tokyoLat, Longitude: tokyoLng},
					},
				},
			},
			want: entity.ProximityHome,
		},
		{
			name: "NEARBY: venue within 200km of home centroid",
			home: home,
			c: &entity.Concert{
				Event: entity.Event{
					Venue: &entity.Venue{
						AdminArea:   &osakaLevel1,
						Coordinates: &entity.Coordinates{Latitude: tokyoLat, Longitude: tokyoLng},
					},
				},
			},
			want: entity.ProximityNearby,
		},
		{
			name: "AWAY: venue beyond 200km",
			home: home,
			c: &entity.Concert{
				Event: entity.Event{
					Venue: &entity.Venue{
						AdminArea:   &osakaLevel1,
						Coordinates: &entity.Coordinates{Latitude: osakaLat, Longitude: osakaLng},
					},
				},
			},
			want: entity.ProximityAway,
		},
		{
			name: "AWAY: venue far away (Sapporo)",
			home: home,
			c: &entity.Concert{
				Event: entity.Event{
					Venue: &entity.Venue{
						AdminArea:   &osakaLevel1,
						Coordinates: &entity.Coordinates{Latitude: sapporoLat, Longitude: sapporoLng},
					},
				},
			},
			want: entity.ProximityAway,
		},
		{
			name: "AWAY: nil home",
			home: nil,
			c: &entity.Concert{
				Event: entity.Event{
					Venue: &entity.Venue{
						AdminArea:   &tokyoLevel1,
						Coordinates: &entity.Coordinates{Latitude: tokyoLat, Longitude: tokyoLng},
					},
				},
			},
			want: entity.ProximityAway,
		},
		{
			name: "AWAY: nil venue",
			home: home,
			c: &entity.Concert{
				Event: entity.Event{
					Venue: nil,
				},
			},
			want: entity.ProximityAway,
		},
		{
			name: "AWAY: venue missing coordinates",
			home: home,
			c: &entity.Concert{
				Event: entity.Event{
					Venue: &entity.Venue{
						AdminArea: &osakaLevel1,
					},
				},
			},
			want: entity.ProximityAway,
		},
		{
			name: "AWAY: home centroid is nil (unsupported country)",
			home: &entity.Home{
				ID:          "home-2",
				CountryCode: "JP",
				Level1:      tokyoLevel1,
				Centroid:    nil,
			},
			c: &entity.Concert{
				Event: entity.Event{
					Venue: &entity.Venue{
						AdminArea:   &osakaLevel1,
						Coordinates: &entity.Coordinates{Latitude: osakaLat, Longitude: osakaLng},
					},
				},
			},
			want: entity.ProximityAway,
		},
		{
			name: "HOME: admin_area match takes priority over distance",
			home: home,
			c: &entity.Concert{
				Event: entity.Event{
					Venue: &entity.Venue{
						AdminArea:   &tokyoLevel1,
						Coordinates: &entity.Coordinates{Latitude: sapporoLat, Longitude: sapporoLng},
					},
				},
			},
			want: entity.ProximityHome,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := tt.c.ProximityTo(tt.home)
			if got != tt.want {
				t.Errorf("ProximityTo() = %v, want %v", got, tt.want)
			}
		})
	}
}
