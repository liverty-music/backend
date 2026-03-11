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

func TestHype_ShouldNotify(t *testing.T) {
	t.Parallel()

	// Tokyo (JP-13): lat 35.6762, lng 139.6503
	tokyoLevel1 := "JP-13"
	tokyoCentroid := &entity.Coordinates{Latitude: 35.6762, Longitude: 139.6503}

	// Yokohama (JP-14): lat 35.4437, lng 139.6380 — ~30 km from Tokyo (NEARBY)
	yokohamaLevel1 := "JP-14"
	yokohama := &entity.Coordinates{Latitude: 35.4437, Longitude: 139.6380}

	// Osaka (JP-27): lat 34.6937, lng 135.5023 — ~400 km from Tokyo (AWAY)
	osakaLevel1 := "JP-27"
	osaka := &entity.Coordinates{Latitude: 34.6937, Longitude: 135.5023}

	tokyoHome := &entity.Home{
		CountryCode: "JP",
		Level1:      tokyoLevel1,
		Centroid:    tokyoCentroid,
	}

	date := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)

	// A concert at a Tokyo venue (HOME proximity).
	tokyoConcert := &entity.Concert{
		Event: entity.Event{
			LocalDate: date,
			Venue: &entity.Venue{
				AdminArea:   &tokyoLevel1,
				Coordinates: tokyoCentroid,
			},
		},
	}

	// A concert at a Yokohama venue — no admin-area match but within 200 km (NEARBY).
	yokohamaConcert := &entity.Concert{
		Event: entity.Event{
			LocalDate: date,
			Venue: &entity.Venue{
				AdminArea:   &yokohamaLevel1,
				Coordinates: yokohama,
			},
		},
	}

	// A concert at an Osaka venue — no admin-area match and >200 km (AWAY).
	osakaConcert := &entity.Concert{
		Event: entity.Event{
			LocalDate: date,
			Venue: &entity.Venue{
				AdminArea:   &osakaLevel1,
				Coordinates: osaka,
			},
		},
	}

	type args struct {
		hype       entity.Hype
		home       *entity.Home
		venueAreas map[string]struct{}
		concerts   []*entity.Concert
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "return false for HypeWatch regardless of home",
			args: args{
				hype:       entity.HypeWatch,
				home:       tokyoHome,
				venueAreas: map[string]struct{}{tokyoLevel1: {}},
				concerts:   []*entity.Concert{tokyoConcert},
			},
			want: false,
		},
		{
			name: "return true for HypeHome when home level1 matches venue area",
			args: args{
				hype:       entity.HypeHome,
				home:       tokyoHome,
				venueAreas: map[string]struct{}{tokyoLevel1: {}},
				concerts:   []*entity.Concert{tokyoConcert},
			},
			want: true,
		},
		{
			name: "return false for HypeHome when home level1 does not match any venue area",
			args: args{
				hype:       entity.HypeHome,
				home:       tokyoHome,
				venueAreas: map[string]struct{}{osakaLevel1: {}},
				concerts:   []*entity.Concert{osakaConcert},
			},
			want: false,
		},
		{
			name: "return false for HypeHome when home is nil",
			args: args{
				hype:       entity.HypeHome,
				home:       nil,
				venueAreas: map[string]struct{}{tokyoLevel1: {}},
				concerts:   []*entity.Concert{tokyoConcert},
			},
			want: false,
		},
		{
			name: "return false for HypeHome when home level1 is empty",
			args: args{
				hype: entity.HypeHome,
				home: &entity.Home{
					CountryCode: "JP",
					Level1:      "",
					Centroid:    tokyoCentroid,
				},
				venueAreas: map[string]struct{}{tokyoLevel1: {}},
				concerts:   []*entity.Concert{tokyoConcert},
			},
			want: false,
		},
		{
			name: "return true for HypeNearby when a concert is nearby",
			args: args{
				hype:       entity.HypeNearby,
				home:       tokyoHome,
				venueAreas: map[string]struct{}{yokohamaLevel1: {}},
				concerts:   []*entity.Concert{yokohamaConcert},
			},
			want: true,
		},
		{
			name: "return false for HypeNearby when all concerts are distant",
			args: args{
				hype:       entity.HypeNearby,
				home:       tokyoHome,
				venueAreas: map[string]struct{}{osakaLevel1: {}},
				concerts:   []*entity.Concert{osakaConcert},
			},
			want: false,
		},
		{
			name: "return false for HypeNearby when home is nil",
			args: args{
				hype:       entity.HypeNearby,
				home:       nil,
				venueAreas: map[string]struct{}{yokohamaLevel1: {}},
				concerts:   []*entity.Concert{yokohamaConcert},
			},
			want: false,
		},
		{
			name: "return true for HypeAway regardless of proximity",
			args: args{
				hype:       entity.HypeAway,
				home:       tokyoHome,
				venueAreas: map[string]struct{}{osakaLevel1: {}},
				concerts:   []*entity.Concert{osakaConcert},
			},
			want: true,
		},
		{
			name: "return false for unknown hype value",
			args: args{
				hype:       entity.Hype("unknown"),
				home:       tokyoHome,
				venueAreas: map[string]struct{}{tokyoLevel1: {}},
				concerts:   []*entity.Concert{tokyoConcert},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := tt.args.hype.ShouldNotify(tt.args.home, tt.args.venueAreas, tt.args.concerts)
			assert.Equal(t, tt.want, got)
		})
	}
}
