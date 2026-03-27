package entity_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func TestScrapedConcert_ToConcert(t *testing.T) {
	t.Parallel()

	localDate := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)
	startTime := time.Date(2026, 6, 15, 19, 0, 0, 0, time.UTC)
	openTime := time.Date(2026, 6, 15, 18, 0, 0, 0, time.UTC)
	adminArea := "JP-13"

	tests := []struct {
		name      string
		sc        *entity.ScrapedConcert
		artistID  string
		eventID   string
		venueID   string
		wantCheck func(t *testing.T, got *entity.Concert)
	}{
		{
			name: "maps all fields including optional times",
			sc: &entity.ScrapedConcert{
				Title:           "Live Show",
				ListedVenueName: "Zepp Tokyo",
				AdminArea:       &adminArea,
				LocalDate:       localDate,
				StartTime:       startTime,
				OpenTime:        openTime,
				SourceURL:       "https://example.com/live",
			},
			artistID: "artist-1",
			eventID:  "event-1",
			venueID:  "venue-1",
			wantCheck: func(t *testing.T, got *entity.Concert) {
				t.Helper()
				assert.Equal(t, "artist-1", got.ArtistID)
				assert.Equal(t, "event-1", got.ID)
				assert.Equal(t, "venue-1", got.VenueID)
				assert.Equal(t, "Live Show", got.Title)
				assert.Equal(t, "Zepp Tokyo", *got.ListedVenueName)
				assert.Equal(t, localDate, got.LocalDate)
				assert.Equal(t, &startTime, got.StartTime)
				assert.Equal(t, &openTime, got.OpenTime)
				assert.Equal(t, "https://example.com/live", got.SourceURL)
			},
		},
		{
			name: "maps nil optional times",
			sc: &entity.ScrapedConcert{
				Title:           "Minimal Show",
				ListedVenueName: "Some Venue",
				LocalDate:       localDate,
				SourceURL:       "https://example.com",
			},
			artistID: "artist-2",
			eventID:  "",
			venueID:  "",
			wantCheck: func(t *testing.T, got *entity.Concert) {
				t.Helper()
				assert.Equal(t, "artist-2", got.ArtistID)
				assert.Empty(t, got.ID)
				assert.Empty(t, got.VenueID)
				assert.Nil(t, got.StartTime)
				assert.Nil(t, got.OpenTime)
				assert.Equal(t, "Minimal Show", got.Title)
			},
		},
		{
			name: "distinct outputs for different IDs",
			sc: &entity.ScrapedConcert{
				Title:           "Same Show",
				ListedVenueName: "Same Venue",
				LocalDate:       localDate,
				SourceURL:       "https://example.com",
			},
			artistID: "artist-A",
			eventID:  "event-A",
			venueID:  "venue-A",
			wantCheck: func(t *testing.T, got *entity.Concert) {
				t.Helper()
				assert.Equal(t, "artist-A", got.ArtistID)
				assert.Equal(t, "event-A", got.ID)
				assert.Equal(t, "venue-A", got.VenueID)
			},
		},
		{
			name: "ListedVenueName is an independent copy",
			sc: &entity.ScrapedConcert{
				Title:           "Copy Test",
				ListedVenueName: "Original Venue",
				LocalDate:       localDate,
				SourceURL:       "https://example.com",
			},
			artistID: "artist-3",
			eventID:  "event-3",
			venueID:  "venue-3",
			wantCheck: func(t *testing.T, got *entity.Concert) {
				t.Helper()
				require.NotNil(t, got.ListedVenueName)
				assert.Equal(t, "Original Venue", *got.ListedVenueName)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := tt.sc.ToConcert(tt.artistID, tt.eventID, tt.venueID)
			require.NotNil(t, got)
			tt.wantCheck(t, got)
		})
	}
}

func TestScrapedConcerts_FilterNew(t *testing.T) {
	t.Parallel()

	date1 := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
	date2 := time.Date(2026, 3, 17, 0, 0, 0, 0, time.UTC)
	date3 := time.Date(2026, 3, 16, 0, 0, 0, 0, time.UTC)

	sc1 := &entity.ScrapedConcert{LocalDate: date1, ListedVenueName: "Zepp Tokyo", Title: "Live A"}
	sc2 := &entity.ScrapedConcert{LocalDate: date2, ListedVenueName: "Zepp Osaka", Title: "Live B"}
	sc3 := &entity.ScrapedConcert{LocalDate: date3, ListedVenueName: "Zepp Nagoya", Title: "Live C"}
	sc1Dup := &entity.ScrapedConcert{LocalDate: date1, ListedVenueName: "Other Venue", Title: "Live A2"}

	existing1 := &entity.Concert{Event: entity.Event{LocalDate: date1}}
	existing2 := &entity.Concert{Event: entity.Event{LocalDate: date2}}

	type args struct {
		scraped  entity.ScrapedConcerts
		existing []*entity.Concert
	}
	tests := []struct {
		name string
		args args
		want entity.ScrapedConcerts
	}{
		{
			name: "return nil when scraped is nil",
			args: args{
				scraped:  nil,
				existing: []*entity.Concert{existing1},
			},
			want: nil,
		},
		{
			name: "return nil when scraped is empty",
			args: args{
				scraped:  entity.ScrapedConcerts{},
				existing: []*entity.Concert{existing1},
			},
			want: nil,
		},
		{
			name: "return all scraped when existing is empty",
			args: args{
				scraped:  entity.ScrapedConcerts{sc1, sc2, sc3},
				existing: []*entity.Concert{},
			},
			want: entity.ScrapedConcerts{sc1, sc2, sc3},
		},
		{
			name: "return nil when all scraped conflict with existing",
			args: args{
				scraped:  entity.ScrapedConcerts{sc1, sc2},
				existing: []*entity.Concert{existing1, existing2},
			},
			want: nil,
		},
		{
			name: "return only non-conflicting concerts",
			args: args{
				scraped:  entity.ScrapedConcerts{sc1, sc2, sc3},
				existing: []*entity.Concert{existing1},
			},
			want: entity.ScrapedConcerts{sc2, sc3},
		},
		{
			name: "deduplicate within-batch same-date concerts",
			args: args{
				scraped:  entity.ScrapedConcerts{sc1, sc1Dup},
				existing: []*entity.Concert{},
			},
			want: entity.ScrapedConcerts{sc1},
		},
		{
			name: "return nil when within-batch duplicate conflicts with existing",
			args: args{
				scraped:  entity.ScrapedConcerts{sc1, sc1Dup},
				existing: []*entity.Concert{existing1},
			},
			want: nil,
		},
		{
			name: "preserve original order of scraped concerts",
			args: args{
				scraped:  entity.ScrapedConcerts{sc1, sc2, sc3},
				existing: []*entity.Concert{},
			},
			want: entity.ScrapedConcerts{sc1, sc2, sc3},
		},
		{
			name: "return all scraped when existing is nil",
			args: args{
				scraped:  entity.ScrapedConcerts{sc1, sc2},
				existing: nil,
			},
			want: entity.ScrapedConcerts{sc1, sc2},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := tt.args.scraped.FilterNew(tt.args.existing)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestScrapedConcert_JSONSerialization(t *testing.T) {
	t.Parallel()

	localDate := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
	startTime := time.Date(2026, 3, 15, 19, 0, 0, 0, time.UTC)
	adminArea := "JP-13"

	tests := []struct {
		name           string
		concert        *entity.ScrapedConcert
		wantKeys       []string
		wantAbsentKeys []string
	}{
		{
			name: "omit zero optional fields",
			concert: &entity.ScrapedConcert{
				Title:           "Live Show",
				ListedVenueName: "Zepp Tokyo",
				AdminArea:       nil,
				LocalDate:       localDate,
				SourceURL:       "https://example.com",
			},
			wantKeys:       []string{"title", "listed_venue_name", "local_date", "source_url"},
			wantAbsentKeys: []string{"admin_area", "start_time", "open_time"},
		},
		{
			name: "include all populated fields",
			concert: &entity.ScrapedConcert{
				Title:           "Live Show",
				ListedVenueName: "Zepp Tokyo",
				AdminArea:       &adminArea,
				LocalDate:       localDate,
				StartTime:       startTime,
				OpenTime:        startTime,
				SourceURL:       "https://example.com",
			},
			wantKeys:       []string{"title", "listed_venue_name", "admin_area", "local_date", "start_time", "open_time", "source_url"},
			wantAbsentKeys: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			data, err := json.Marshal(tt.concert)
			assert.NoError(t, err)

			var m map[string]any
			assert.NoError(t, json.Unmarshal(data, &m))

			for _, key := range tt.wantKeys {
				assert.Contains(t, m, key, "expected key %q in JSON", key)
			}
			for _, key := range tt.wantAbsentKeys {
				assert.NotContains(t, m, key, "unexpected key %q in JSON", key)
			}
		})
	}
}
