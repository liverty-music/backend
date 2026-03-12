package entity_test

import (
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/stretchr/testify/assert"
)

func TestConcert_ProximityTo_Extended(t *testing.T) {
	t.Parallel()

	// Tokyo (JP-13): 35.6762, 139.6503
	tokyoLevel1 := "JP-13"
	tokyoCoords := &entity.Coordinates{Latitude: 35.6762, Longitude: 139.6503}

	// Yokohama (JP-14): 35.4437, 139.6380 — ~30 km from Tokyo (NEARBY)
	yokohamaLevel1 := "JP-14"
	yokohamaCoords := &entity.Coordinates{Latitude: 35.4437, Longitude: 139.6380}

	// Osaka (JP-27): 34.6937, 135.5023 — ~400 km from Tokyo (AWAY)
	osakaLevel1 := "JP-27"
	osakaCoords := &entity.Coordinates{Latitude: 34.6937, Longitude: 135.5023}

	tokyoHome := &entity.Home{
		CountryCode: "JP",
		Level1:      tokyoLevel1,
		Centroid:    tokyoCoords,
	}

	type args struct {
		concert *entity.Concert
		home    *entity.Home
	}
	tests := []struct {
		name string
		args args
		want entity.Proximity
	}{
		{
			name: "return Away when home is nil",
			args: args{
				concert: &entity.Concert{
					Event: entity.Event{
						Venue: &entity.Venue{
							AdminArea:   &tokyoLevel1,
							Coordinates: tokyoCoords,
						},
					},
				},
				home: nil,
			},
			want: entity.ProximityAway,
		},
		{
			name: "return Away when venue is nil",
			args: args{
				concert: &entity.Concert{
					Event: entity.Event{Venue: nil},
				},
				home: tokyoHome,
			},
			want: entity.ProximityAway,
		},
		{
			name: "return Home when admin area matches home level1",
			args: args{
				concert: &entity.Concert{
					Event: entity.Event{
						Venue: &entity.Venue{
							AdminArea:   &tokyoLevel1,
							Coordinates: tokyoCoords,
						},
					},
				},
				home: tokyoHome,
			},
			want: entity.ProximityHome,
		},
		{
			name: "return Nearby when admin area mismatches but coordinates are within 30km",
			args: args{
				concert: &entity.Concert{
					Event: entity.Event{
						Venue: &entity.Venue{
							AdminArea:   &yokohamaLevel1,
							Coordinates: yokohamaCoords,
						},
					},
				},
				home: tokyoHome,
			},
			want: entity.ProximityNearby,
		},
		{
			name: "return Away when admin area mismatches and coordinates are ~400km away",
			args: args{
				concert: &entity.Concert{
					Event: entity.Event{
						Venue: &entity.Venue{
							AdminArea:   &osakaLevel1,
							Coordinates: osakaCoords,
						},
					},
				},
				home: tokyoHome,
			},
			want: entity.ProximityAway,
		},
		{
			name: "return Away when venue has no coordinates",
			args: args{
				concert: &entity.Concert{
					Event: entity.Event{
						Venue: &entity.Venue{
							AdminArea:   &yokohamaLevel1,
							Coordinates: nil,
						},
					},
				},
				home: tokyoHome,
			},
			want: entity.ProximityAway,
		},
		{
			name: "return Away when home centroid is nil",
			args: args{
				concert: &entity.Concert{
					Event: entity.Event{
						Venue: &entity.Venue{
							AdminArea:   &yokohamaLevel1,
							Coordinates: yokohamaCoords,
						},
					},
				},
				home: &entity.Home{
					CountryCode: "JP",
					Level1:      tokyoLevel1,
					Centroid:    nil,
				},
			},
			want: entity.ProximityAway,
		},
		{
			name: "return Home when admin area matches regardless of distant coordinates",
			args: args{
				concert: &entity.Concert{
					Event: entity.Event{
						Venue: &entity.Venue{
							AdminArea:   &tokyoLevel1,
							Coordinates: osakaCoords, // far away but admin area matches
						},
					},
				},
				home: tokyoHome,
			},
			want: entity.ProximityHome,
		},
		{
			name: "return Nearby when venue admin area is nil but coordinates are within 30km",
			args: args{
				concert: &entity.Concert{
					Event: entity.Event{
						Venue: &entity.Venue{
							AdminArea:   nil,
							Coordinates: yokohamaCoords,
						},
					},
				},
				home: tokyoHome,
			},
			want: entity.ProximityNearby,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := tt.args.concert.ProximityTo(tt.args.home)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestGroupByDateAndProximity(t *testing.T) {
	t.Parallel()

	tokyoLevel1 := "JP-13"
	tokyoCoords := &entity.Coordinates{Latitude: 35.6762, Longitude: 139.6503}

	yokohamaLevel1 := "JP-14"
	yokohamaCoords := &entity.Coordinates{Latitude: 35.4437, Longitude: 139.6380}

	osakaLevel1 := "JP-27"
	osakaCoords := &entity.Coordinates{Latitude: 34.6937, Longitude: 135.5023}

	tokyoHome := &entity.Home{
		CountryCode: "JP",
		Level1:      tokyoLevel1,
		Centroid:    tokyoCoords,
	}

	date1 := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	date2 := time.Date(2025, 6, 2, 0, 0, 0, 0, time.UTC)

	type args struct {
		concerts []*entity.Concert
		home     *entity.Home
	}
	tests := []struct {
		name         string
		args         args
		wantLen      int
		wantDate1Len [3]int // [home, nearby, away]
	}{
		{
			name: "return nil for empty concert list",
			args: args{
				concerts: nil,
				home:     tokyoHome,
			},
			wantLen: 0,
		},
		{
			name: "group concerts into correct proximity buckets for single date",
			args: args{
				concerts: []*entity.Concert{
					{Event: entity.Event{LocalDate: date1, Venue: &entity.Venue{AdminArea: &tokyoLevel1, Coordinates: tokyoCoords}}},
					{Event: entity.Event{LocalDate: date1, Venue: &entity.Venue{AdminArea: &yokohamaLevel1, Coordinates: yokohamaCoords}}},
					{Event: entity.Event{LocalDate: date1, Venue: &entity.Venue{AdminArea: &osakaLevel1, Coordinates: osakaCoords}}},
				},
				home: tokyoHome,
			},
			wantLen:      1,
			wantDate1Len: [3]int{1, 1, 1},
		},
		{
			name: "group concerts across multiple dates preserving order",
			args: args{
				concerts: []*entity.Concert{
					{Event: entity.Event{LocalDate: date1, Venue: &entity.Venue{AdminArea: &tokyoLevel1, Coordinates: tokyoCoords}}},
					{Event: entity.Event{LocalDate: date2, Venue: &entity.Venue{AdminArea: &osakaLevel1, Coordinates: osakaCoords}}},
				},
				home: tokyoHome,
			},
			wantLen: 2,
		},
		{
			name: "classify all concerts as away when home is nil",
			args: args{
				concerts: []*entity.Concert{
					{Event: entity.Event{LocalDate: date1, Venue: &entity.Venue{AdminArea: &tokyoLevel1, Coordinates: tokyoCoords}}},
					{Event: entity.Event{LocalDate: date1, Venue: &entity.Venue{AdminArea: &osakaLevel1, Coordinates: osakaCoords}}},
				},
				home: nil,
			},
			wantLen:      1,
			wantDate1Len: [3]int{0, 0, 2},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := entity.GroupByDateAndProximity(tt.args.concerts, tt.args.home)

			if tt.wantLen == 0 {
				assert.Nil(t, got)
				return
			}

			assert.Len(t, got, tt.wantLen)

			if tt.wantDate1Len != [3]int{} {
				g := got[0]
				assert.Len(t, g.Home, tt.wantDate1Len[0])
				assert.Len(t, g.Nearby, tt.wantDate1Len[1])
				assert.Len(t, g.Away, tt.wantDate1Len[2])
			}
		})
	}
}

func TestScrapedConcert_DateVenueKey(t *testing.T) {
	t.Parallel()

	date := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)

	type args struct {
		concert *entity.ScrapedConcert
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{
			name: "return date and venue joined by pipe",
			args: args{
				concert: &entity.ScrapedConcert{
					LocalDate:       date,
					ListedVenueName: "Budokan",
				},
			},
			want: "2025-06-01|Budokan",
		},
		{
			name: "return date and empty venue name when venue is empty",
			args: args{
				concert: &entity.ScrapedConcert{
					LocalDate:       date,
					ListedVenueName: "",
				},
			},
			want: "2025-06-01|",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := tt.args.concert.DateVenueKey()
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestScrapedConcert_DedupeKey(t *testing.T) {
	t.Parallel()

	date := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	startTime := time.Date(2025, 6, 1, 19, 0, 0, 0, time.UTC)
	// Same instant in a different timezone to verify UTC normalization.
	jst := time.FixedZone("JST", 9*60*60)
	startTimeJST := time.Date(2025, 6, 2, 4, 0, 0, 0, jst) // same UTC instant

	type args struct {
		concert *entity.ScrapedConcert
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{
			name: "return DateVenueKey when start time is nil",
			args: args{
				concert: &entity.ScrapedConcert{
					LocalDate:       date,
					ListedVenueName: "Budokan",
					StartTime:       nil,
				},
			},
			want: "2025-06-01|Budokan",
		},
		{
			name: "return full key with UTC-normalized start time when start time is set",
			args: args{
				concert: &entity.ScrapedConcert{
					LocalDate:       date,
					ListedVenueName: "Budokan",
					StartTime:       &startTime,
				},
			},
			want: "2025-06-01|Budokan|19:00:00Z",
		},
		{
			name: "produce same key for equal instant in different timezone",
			args: args{
				concert: &entity.ScrapedConcert{
					LocalDate:       date,
					ListedVenueName: "Budokan",
					StartTime:       &startTimeJST,
				},
			},
			want: "2025-06-01|Budokan|19:00:00Z",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := tt.args.concert.DedupeKey()
			assert.Equal(t, tt.want, got)
		})
	}
}
