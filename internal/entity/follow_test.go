package entity_test

import (
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/stretchr/testify/assert"
)

func TestHype_IsValid(t *testing.T) {
	t.Parallel()

	type args struct {
		hype entity.Hype
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "return true for HypeWatch",
			args: args{hype: entity.HypeWatch},
			want: true,
		},
		{
			name: "return true for HypeHome",
			args: args{hype: entity.HypeHome},
			want: true,
		},
		{
			name: "return true for HypeNearby",
			args: args{hype: entity.HypeNearby},
			want: true,
		},
		{
			name: "return true for HypeAway",
			args: args{hype: entity.HypeAway},
			want: true,
		},
		{
			name: "return false for empty string",
			args: args{hype: entity.Hype("")},
			want: false,
		},
		{
			name: "return false for unknown value",
			args: args{hype: entity.Hype("unknown")},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := tt.args.hype.IsValid()
			assert.Equal(t, tt.want, got)
		})
	}
}

// hypeTestFixture builds the shared home + per-proximity concerts used by both
// the MatchingConcerts and ShouldNotify tests. Concerts carry stable IDs so the
// subset assertions can check identity, not just length.
func hypeTestFixture() (tokyoHome *entity.Home, tokyo, yokohama, osaka *entity.Concert) {
	// Tokyo (JP-13): lat 35.6762, lng 139.6503
	tokyoLevel1 := "JP-13"
	tokyoCentroid := &entity.Coordinates{Latitude: 35.6762, Longitude: 139.6503}

	// Yokohama (JP-14): lat 35.4437, lng 139.6380 — ~30 km from Tokyo (NEARBY)
	yokohamaLevel1 := "JP-14"
	yokohamaCoords := &entity.Coordinates{Latitude: 35.4437, Longitude: 139.6380}

	// Osaka (JP-27): lat 34.6937, lng 135.5023 — ~400 km from Tokyo (AWAY)
	osakaLevel1 := "JP-27"
	osakaCoords := &entity.Coordinates{Latitude: 34.6937, Longitude: 135.5023}

	tokyoHome = &entity.Home{
		CountryCode: "JP",
		Level1:      tokyoLevel1,
		Centroid:    tokyoCentroid,
	}

	date := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)

	// A concert at a Tokyo venue (HOME proximity).
	tokyo = &entity.Concert{
		Event: entity.Event{
			ID:        "concert-tokyo",
			LocalDate: date,
			Venue: &entity.Venue{
				AdminArea:   &tokyoLevel1,
				Coordinates: tokyoCentroid,
			},
		},
	}

	// A concert at a Yokohama venue — no admin-area match but within 200 km (NEARBY).
	yokohama = &entity.Concert{
		Event: entity.Event{
			ID:        "concert-yokohama",
			LocalDate: date,
			Venue: &entity.Venue{
				AdminArea:   &yokohamaLevel1,
				Coordinates: yokohamaCoords,
			},
		},
	}

	// A concert at an Osaka venue — no admin-area match and >200 km (AWAY).
	osaka = &entity.Concert{
		Event: entity.Event{
			ID:        "concert-osaka",
			LocalDate: date,
			Venue: &entity.Venue{
				AdminArea:   &osakaLevel1,
				Coordinates: osakaCoords,
			},
		},
	}
	return tokyoHome, tokyo, yokohama, osaka
}

func TestHype_MatchingConcerts(t *testing.T) {
	t.Parallel()

	tokyoHome, tokyo, yokohama, osaka := hypeTestFixture()
	all := []*entity.Concert{tokyo, yokohama, osaka}

	tests := []struct {
		name     string
		hype     entity.Hype
		home     *entity.Home
		concerts []*entity.Concert
		want     []*entity.Concert
	}{
		{
			name:     "HypeWatch matches nothing",
			hype:     entity.HypeWatch,
			home:     tokyoHome,
			concerts: all,
			want:     nil,
		},
		{
			name:     "HypeHome selects only in-area concerts",
			hype:     entity.HypeHome,
			home:     tokyoHome,
			concerts: all,
			want:     []*entity.Concert{tokyo},
		},
		{
			name:     "HypeHome matches nothing when no concert is in-area",
			hype:     entity.HypeHome,
			home:     tokyoHome,
			concerts: []*entity.Concert{yokohama, osaka},
			want:     nil,
		},
		{
			name:     "HypeHome matches nothing when home is nil",
			hype:     entity.HypeHome,
			home:     nil,
			concerts: all,
			want:     nil,
		},
		{
			name:     "HypeHome matches nothing when home level1 is empty",
			hype:     entity.HypeHome,
			home:     &entity.Home{CountryCode: "JP", Level1: ""},
			concerts: all,
			want:     nil,
		},
		{
			name:     "HypeNearby selects in-area and in-range concerts",
			hype:     entity.HypeNearby,
			home:     tokyoHome,
			concerts: all,
			want:     []*entity.Concert{tokyo, yokohama},
		},
		{
			name:     "HypeNearby matches nothing when all concerts are away",
			hype:     entity.HypeNearby,
			home:     tokyoHome,
			concerts: []*entity.Concert{osaka},
			want:     nil,
		},
		{
			name:     "HypeNearby matches nothing when home is nil",
			hype:     entity.HypeNearby,
			home:     nil,
			concerts: all,
			want:     nil,
		},
		{
			name:     "HypeAway selects all concerts",
			hype:     entity.HypeAway,
			home:     tokyoHome,
			concerts: all,
			want:     []*entity.Concert{tokyo, yokohama, osaka},
		},
		{
			name:     "HypeAway matches nothing for an empty batch",
			hype:     entity.HypeAway,
			home:     tokyoHome,
			concerts: nil,
			want:     nil,
		},
		{
			name:     "unknown hype matches nothing",
			hype:     entity.Hype("unknown"),
			home:     tokyoHome,
			concerts: all,
			want:     nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := tt.hype.MatchingConcerts(tt.home, tt.concerts)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestHype_ShouldNotify(t *testing.T) {
	t.Parallel()

	tokyoHome, tokyo, _, osaka := hypeTestFixture()

	tests := []struct {
		name     string
		hype     entity.Hype
		home     *entity.Home
		concerts []*entity.Concert
		want     bool
	}{
		{
			name:     "false for HypeWatch",
			hype:     entity.HypeWatch,
			home:     tokyoHome,
			concerts: []*entity.Concert{tokyo},
			want:     false,
		},
		{
			name:     "true for HypeHome with an in-area concert",
			hype:     entity.HypeHome,
			home:     tokyoHome,
			concerts: []*entity.Concert{tokyo},
			want:     true,
		},
		{
			name:     "false for HypeHome with only out-of-area concerts",
			hype:     entity.HypeHome,
			home:     tokyoHome,
			concerts: []*entity.Concert{osaka},
			want:     false,
		},
		{
			name:     "true for HypeAway with any concert",
			hype:     entity.HypeAway,
			home:     tokyoHome,
			concerts: []*entity.Concert{osaka},
			want:     true,
		},
		{
			name:     "false for unknown hype",
			hype:     entity.Hype("unknown"),
			home:     tokyoHome,
			concerts: []*entity.Concert{tokyo},
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := tt.hype.ShouldNotify(tt.home, tt.concerts)
			assert.Equal(t, tt.want, got)
		})
	}
}
