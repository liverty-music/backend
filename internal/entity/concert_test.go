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
					Venue: &entity.Venue{
						AdminArea:   &tokyoLevel1,
						Coordinates: tokyoCoords,
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
					Venue: nil,
				},
				home: tokyoHome,
			},
			want: entity.ProximityAway,
		},
		{
			name: "return Home when admin area matches home level1",
			args: args{
				concert: &entity.Concert{
					Venue: &entity.Venue{
						AdminArea:   &tokyoLevel1,
						Coordinates: tokyoCoords,
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
					Venue: &entity.Venue{
						AdminArea:   &yokohamaLevel1,
						Coordinates: yokohamaCoords,
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
					Venue: &entity.Venue{
						AdminArea:   &osakaLevel1,
						Coordinates: osakaCoords,
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
					Venue: &entity.Venue{
						AdminArea:   &yokohamaLevel1,
						Coordinates: nil,
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
					Venue: &entity.Venue{
						AdminArea:   &yokohamaLevel1,
						Coordinates: yokohamaCoords,
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
					Venue: &entity.Venue{
						AdminArea:   &tokyoLevel1,
						Coordinates: osakaCoords, // far away but admin area matches
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
					Venue: &entity.Venue{
						AdminArea:   nil,
						Coordinates: yokohamaCoords,
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
					{LocalDate: date1, Venue: &entity.Venue{AdminArea: &tokyoLevel1, Coordinates: tokyoCoords}},
					{LocalDate: date1, Venue: &entity.Venue{AdminArea: &yokohamaLevel1, Coordinates: yokohamaCoords}},
					{LocalDate: date1, Venue: &entity.Venue{AdminArea: &osakaLevel1, Coordinates: osakaCoords}},
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
					{LocalDate: date1, Venue: &entity.Venue{AdminArea: &tokyoLevel1, Coordinates: tokyoCoords}},
					{LocalDate: date2, Venue: &entity.Venue{AdminArea: &osakaLevel1, Coordinates: osakaCoords}},
				},
				home: tokyoHome,
			},
			wantLen: 2,
		},
		{
			name: "classify all concerts as away when home is nil",
			args: args{
				concerts: []*entity.Concert{
					{LocalDate: date1, Venue: &entity.Venue{AdminArea: &tokyoLevel1, Coordinates: tokyoCoords}},
					{LocalDate: date1, Venue: &entity.Venue{AdminArea: &osakaLevel1, Coordinates: osakaCoords}},
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

func TestDiscoveredSeries_ToConcert(t *testing.T) {
	t.Parallel()

	localDate := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)
	startTime := time.Date(2026, 6, 15, 19, 0, 0, 0, time.UTC)
	openTime := time.Date(2026, 6, 15, 18, 0, 0, 0, time.UTC)
	adminArea := "JP-13"

	tests := []struct {
		name      string
		series    *entity.DiscoveredSeries
		ev        *entity.DiscoveredEvent
		artistID  string
		seriesID  string
		eventID   string
		venueID   string
		wantCheck func(t *testing.T, got *entity.Concert)
	}{
		{
			name: "maps all fields including optional times",
			series: &entity.DiscoveredSeries{
				Title:     "Live Show",
				Type:      entity.SeriesTypeSingle,
				SourceURL: "https://example.com/live",
			},
			ev: &entity.DiscoveredEvent{
				ListedVenueName: "Zepp Tokyo",
				AdminArea:       &adminArea,
				LocalDate:       localDate,
				StartTime:       startTime,
				OpenTime:        openTime,
			},
			artistID: "artist-1",
			seriesID: "series-1",
			eventID:  "event-1",
			venueID:  "venue-1",
			wantCheck: func(t *testing.T, got *entity.Concert) {
				t.Helper()
				require.Len(t, got.Performers, 1)
				assert.Equal(t, "artist-1", got.Performers[0].ID)
				assert.Equal(t, "event-1", got.ID)
				assert.Equal(t, "series-1", got.SeriesID)
				assert.Equal(t, "venue-1", got.VenueID)
				require.NotNil(t, got.Series)
				assert.Equal(t, "series-1", got.Series.ID)
				assert.Equal(t, "Live Show", got.Series.Title)
				assert.Equal(t, entity.SeriesTypeSingle, got.Series.Type)
				assert.Equal(t, "Zepp Tokyo", *got.ListedVenueName)
				assert.Equal(t, localDate, got.LocalDate)
				assert.Equal(t, &startTime, got.StartTime)
				assert.Equal(t, &openTime, got.OpenTime)
				assert.Equal(t, "https://example.com/live", got.Series.SourceURL)
			},
		},
		{
			name: "maps zero optional times to nil",
			series: &entity.DiscoveredSeries{
				Title:     "Minimal Show",
				Type:      entity.SeriesTypeSingle,
				SourceURL: "https://example.com",
			},
			ev: &entity.DiscoveredEvent{
				ListedVenueName: "Some Venue",
				LocalDate:       localDate,
				// StartTime and OpenTime are zero — NullableTime must return nil.
			},
			artistID: "artist-2",
			seriesID: "",
			eventID:  "",
			venueID:  "",
			wantCheck: func(t *testing.T, got *entity.Concert) {
				t.Helper()
				require.Len(t, got.Performers, 1)
				assert.Equal(t, "artist-2", got.Performers[0].ID)
				assert.Empty(t, got.ID)
				assert.Empty(t, got.VenueID)
				assert.Nil(t, got.StartTime)
				assert.Nil(t, got.OpenTime)
				require.NotNil(t, got.Series)
				assert.Equal(t, "Minimal Show", got.Series.Title)
			},
		},
		{
			name: "distinct outputs for different IDs",
			series: &entity.DiscoveredSeries{
				Title:     "Same Show",
				Type:      entity.SeriesTypeSingle,
				SourceURL: "https://example.com",
			},
			ev: &entity.DiscoveredEvent{
				ListedVenueName: "Same Venue",
				LocalDate:       localDate,
			},
			artistID: "artist-A",
			seriesID: "series-A",
			eventID:  "event-A",
			venueID:  "venue-A",
			wantCheck: func(t *testing.T, got *entity.Concert) {
				t.Helper()
				require.Len(t, got.Performers, 1)
				assert.Equal(t, "artist-A", got.Performers[0].ID)
				assert.Equal(t, "event-A", got.ID)
				assert.Equal(t, "series-A", got.SeriesID)
				assert.Equal(t, "venue-A", got.VenueID)
			},
		},
		{
			name: "ListedVenueName is an independent copy",
			series: &entity.DiscoveredSeries{
				Title:     "Copy Test",
				Type:      entity.SeriesTypeSingle,
				SourceURL: "https://example.com",
			},
			ev: &entity.DiscoveredEvent{
				ListedVenueName: "Original Venue",
				LocalDate:       localDate,
			},
			artistID: "artist-3",
			seriesID: "series-3",
			eventID:  "event-3",
			venueID:  "venue-3",
			wantCheck: func(t *testing.T, got *entity.Concert) {
				t.Helper()
				require.NotNil(t, got.ListedVenueName)
				assert.Equal(t, "Original Venue", *got.ListedVenueName)
			},
		},
		{
			name: "tour series type is preserved on the embedded Series",
			series: &entity.DiscoveredSeries{
				Title:     "Summer Tour 2026",
				Type:      entity.SeriesTypeTour,
				SourceURL: "https://example.com/tour",
			},
			ev: &entity.DiscoveredEvent{
				ListedVenueName: "Zepp Osaka",
				LocalDate:       localDate,
			},
			artistID: "artist-T",
			seriesID: "series-T",
			eventID:  "event-T",
			venueID:  "venue-T",
			wantCheck: func(t *testing.T, got *entity.Concert) {
				t.Helper()
				require.NotNil(t, got.Series)
				assert.Equal(t, entity.SeriesTypeTour, got.Series.Type)
				assert.Equal(t, "Summer Tour 2026", got.Series.Title)
				assert.Equal(t, "https://example.com/tour", got.Series.SourceURL)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := tt.series.ToConcert(tt.ev, tt.artistID, tt.seriesID, tt.eventID, tt.venueID)
			require.NotNil(t, got)
			tt.wantCheck(t, got)
		})
	}
}

// makeSeries is a convenience helper that builds a []*entity.DiscoveredSeries
// from a slice of (title, events) pairs, using SeriesTypeSingle and an empty
// SourceURL. Tests that need specific types or URLs construct the struct inline.
func makeSeries(title string, evs ...*entity.DiscoveredEvent) *entity.DiscoveredSeries {
	return &entity.DiscoveredSeries{
		Title:  title,
		Type:   entity.SeriesTypeSingle,
		Events: evs,
	}
}

func makeEvent(date time.Time, venue string) *entity.DiscoveredEvent {
	return &entity.DiscoveredEvent{LocalDate: date, ListedVenueName: venue}
}

func TestFilterNewSeries(t *testing.T) {
	t.Parallel()

	date1 := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
	date2 := time.Date(2026, 3, 17, 0, 0, 0, 0, time.UTC)
	date3 := time.Date(2026, 3, 16, 0, 0, 0, 0, time.UTC)

	ev1 := makeEvent(date1, "Zepp Tokyo")
	ev2 := makeEvent(date2, "Zepp Osaka")
	ev3 := makeEvent(date3, "Zepp Nagoya")
	// ev1Drift duplicates ev1 on the (date, venue) dedup key (same date, same venue name).
	ev1Drift := &entity.DiscoveredEvent{LocalDate: date1, ListedVenueName: "Zepp Tokyo"}

	// Existing concerts carry ListedVenueName so the (date, venue) dedup key can
	// match them. The previous date-only key did not need it.
	zeppTokyo := "Zepp Tokyo"
	zeppOsaka := "Zepp Osaka"
	existing1 := &entity.Concert{LocalDate: date1, ListedVenueName: &zeppTokyo}
	existing2 := &entity.Concert{LocalDate: date2, ListedVenueName: &zeppOsaka}

	type args struct {
		series   []*entity.DiscoveredSeries
		existing []*entity.Concert
	}
	tests := []struct {
		name string
		args args
		// wantEventVenues is a flat list of expected ListedVenueName values across
		// all returned series, in the order they appear series-by-series, event-by-
		// event. nil means the function must return nil.
		wantEventVenues []string
		wantNil         bool
	}{
		{
			name: "return nil when series is nil",
			args: args{
				series:   nil,
				existing: []*entity.Concert{existing1},
			},
			wantNil: true,
		},
		{
			name: "return nil when series is empty",
			args: args{
				series:   []*entity.DiscoveredSeries{},
				existing: []*entity.Concert{existing1},
			},
			wantNil: true,
		},
		{
			name: "return all events when existing is empty",
			args: args{
				series:   []*entity.DiscoveredSeries{makeSeries("Live A", ev1, ev2, ev3)},
				existing: []*entity.Concert{},
			},
			wantEventVenues: []string{"Zepp Tokyo", "Zepp Osaka", "Zepp Nagoya"},
		},
		{
			name: "return nil when all events conflict with existing",
			args: args{
				series:   []*entity.DiscoveredSeries{makeSeries("Live AB", ev1, ev2)},
				existing: []*entity.Concert{existing1, existing2},
			},
			wantNil: true,
		},
		{
			name: "return only non-conflicting events within a series",
			args: args{
				series:   []*entity.DiscoveredSeries{makeSeries("Tour", ev1, ev2, ev3)},
				existing: []*entity.Concert{existing1},
			},
			wantEventVenues: []string{"Zepp Osaka", "Zepp Nagoya"},
		},
		{
			name: "deduplicate within-batch same-date-and-venue events across two series entries",
			args: args{
				series: []*entity.DiscoveredSeries{
					makeSeries("First", ev1),
					makeSeries("Dupe", ev1Drift),
				},
				existing: []*entity.Concert{},
			},
			// Only the first occurrence (ev1 in "First") survives.
			wantEventVenues: []string{"Zepp Tokyo"},
		},
		{
			name: "deduplicate within-batch same-date-and-venue events within one series",
			args: args{
				series: []*entity.DiscoveredSeries{
					makeSeries("Tour", ev1, ev1Drift),
				},
				existing: []*entity.Concert{},
			},
			wantEventVenues: []string{"Zepp Tokyo"},
		},
		{
			name: "return nil when within-batch same-venue duplicate conflicts with existing",
			args: args{
				series: []*entity.DiscoveredSeries{
					makeSeries("Tour", ev1, ev1Drift),
				},
				existing: []*entity.Concert{existing1},
			},
			wantNil: true,
		},
		{
			name: "same date at a different venue is NOT deduped (matches new natural key)",
			args: args{
				series: []*entity.DiscoveredSeries{
					makeSeries("Festival B", makeEvent(date1, "Tokyo Dome")),
				},
				existing: []*entity.Concert{existing1},
			},
			wantEventVenues: []string{"Tokyo Dome"},
		},
		{
			name: "venue-name drift against existing is deduped (admin-area prefix)",
			args: args{
				series: []*entity.DiscoveredSeries{
					makeSeries("Drifted", makeEvent(date1, "大阪・フェスティバルホール")),
				},
				existing: []*entity.Concert{
					{LocalDate: date1, ListedVenueName: func() *string { s := "フェスティバルホール"; return &s }()},
				},
			},
			wantNil: true,
		},
		{
			name: "genuinely different venue on the same date stays distinct",
			args: args{
				series: []*entity.DiscoveredSeries{
					makeSeries("Different", makeEvent(date1, "大阪城ホール")),
				},
				existing: []*entity.Concert{
					{LocalDate: date1, ListedVenueName: func() *string { s := "フェスティバルホール"; return &s }()},
				},
			},
			wantEventVenues: []string{"大阪城ホール"},
		},
		{
			name: "within-batch venue-name drift is deduped (performance prefix)",
			args: args{
				series: []*entity.DiscoveredSeries{
					makeSeries("Tour",
						makeEvent(date1, "フェスティバルホール"),
						makeEvent(date1, "大阪公演 ＠フェスティバルホール"),
					),
				},
				existing: []*entity.Concert{},
			},
			wantEventVenues: []string{"フェスティバルホール"},
		},
		{
			name: "preserve original order of events and series",
			args: args{
				series: []*entity.DiscoveredSeries{
					makeSeries("Tour", ev1, ev2, ev3),
				},
				existing: []*entity.Concert{},
			},
			wantEventVenues: []string{"Zepp Tokyo", "Zepp Osaka", "Zepp Nagoya"},
		},
		{
			name: "return all events when existing is nil",
			args: args{
				series:   []*entity.DiscoveredSeries{makeSeries("Live", ev1, ev2)},
				existing: nil,
			},
			wantEventVenues: []string{"Zepp Tokyo", "Zepp Osaka"},
		},
		{
			name: "blank listed-venue-name events are dropped entirely",
			args: args{
				series: []*entity.DiscoveredSeries{
					makeSeries("TBA Tour",
						makeEvent(date1, ""),
						makeEvent(date2, "Zepp Nagoya"),
					),
				},
				existing: []*entity.Concert{},
			},
			// The blank-venue event is dropped; only the real venue survives.
			wantEventVenues: []string{"Zepp Nagoya"},
		},
		{
			name: "series with only blank-venue events is dropped entirely",
			args: args{
				series: []*entity.DiscoveredSeries{
					makeSeries("All TBA", makeEvent(date1, "")),
				},
				existing: []*entity.Concert{},
			},
			wantNil: true,
		},
		{
			name: "unknown-start event dropped when anything is already known at that (date,venue)",
			args: args{
				series: []*entity.DiscoveredSeries{
					makeSeries("Tour", &entity.DiscoveredEvent{
						LocalDate:       date1,
						ListedVenueName: "Zepp Tokyo",
						// StartTime zero → unknown
					}),
				},
				existing: []*entity.Concert{existing1}, // existing1 has no StartTime → StartKey ""
			},
			// Both sides are unknown-start — the batch event is redundant (len(seen[k])>0).
			wantNil: true,
		},
		{
			name: "known-start event kept when only unknown-start existing row exists",
			args: args{
				series: []*entity.DiscoveredSeries{
					makeSeries("Tour", &entity.DiscoveredEvent{
						LocalDate:       date1,
						ListedVenueName: "Zepp Tokyo",
						StartTime:       time.Date(2026, 3, 15, 19, 0, 0, 0, time.UTC),
					}),
				},
				// existing1 has nil StartTime → StartKey "" (unknown).
				existing: []*entity.Concert{existing1},
			},
			// Known start must not be absorbed by an unknown-start existing row.
			wantEventVenues: []string{"Zepp Tokyo"},
		},
		{
			name: "two differing known start times on same date/venue kept as distinct shows",
			args: args{
				series: []*entity.DiscoveredSeries{
					makeSeries("2-show day",
						&entity.DiscoveredEvent{
							LocalDate:       date1,
							ListedVenueName: "Zepp Tokyo",
							StartTime:       time.Date(2026, 3, 15, 14, 0, 0, 0, time.UTC),
						},
						&entity.DiscoveredEvent{
							LocalDate:       date1,
							ListedVenueName: "Zepp Tokyo",
							StartTime:       time.Date(2026, 3, 15, 19, 0, 0, 0, time.UTC),
						},
					),
				},
				existing: []*entity.Concert{},
			},
			// Both matinee and evening shows survive.
			wantEventVenues: []string{"Zepp Tokyo", "Zepp Tokyo"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := entity.FilterNewSeries(tt.args.series, tt.args.existing)

			if tt.wantNil {
				assert.Nil(t, got)
				return
			}

			require.NotNil(t, got)
			var gotVenues []string
			for _, s := range got {
				for _, ev := range s.Events {
					gotVenues = append(gotVenues, ev.ListedVenueName)
				}
			}
			assert.Equal(t, tt.wantEventVenues, gotVenues)
		})
	}
}

func TestDiscoveredEvent_JSONSerialization(t *testing.T) {
	t.Parallel()

	localDate := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
	startTime := time.Date(2026, 3, 15, 19, 0, 0, 0, time.UTC)
	adminArea := "JP-13"

	tests := []struct {
		name           string
		ev             *entity.DiscoveredEvent
		wantKeys       []string
		wantAbsentKeys []string
	}{
		{
			name: "omit zero optional fields",
			ev: &entity.DiscoveredEvent{
				ListedVenueName: "Zepp Tokyo",
				AdminArea:       nil,
				LocalDate:       localDate,
				// StartTime and OpenTime are zero — omitzero should suppress them.
			},
			wantKeys:       []string{"listed_venue_name", "local_date"},
			wantAbsentKeys: []string{"admin_area", "start_time", "open_time"},
		},
		{
			name: "include all populated fields",
			ev: &entity.DiscoveredEvent{
				ListedVenueName: "Zepp Tokyo",
				AdminArea:       &adminArea,
				LocalDate:       localDate,
				StartTime:       startTime,
				OpenTime:        startTime,
			},
			wantKeys:       []string{"listed_venue_name", "admin_area", "local_date", "start_time", "open_time"},
			wantAbsentKeys: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			data, err := json.Marshal(tt.ev)
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

func TestDiscoveredSeries_JSONSerialization(t *testing.T) {
	t.Parallel()

	localDate := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name           string
		series         *entity.DiscoveredSeries
		wantKeys       []string
		wantAbsentKeys []string
	}{
		{
			name: "omit empty source_url",
			series: &entity.DiscoveredSeries{
				Title:  "Live Show",
				Type:   entity.SeriesTypeSingle,
				Events: []*entity.DiscoveredEvent{{ListedVenueName: "Zepp Tokyo", LocalDate: localDate}},
				// SourceURL is empty — omitempty should suppress it.
			},
			wantKeys:       []string{"title", "type", "events"},
			wantAbsentKeys: []string{"source_url"},
		},
		{
			name: "include source_url when present",
			series: &entity.DiscoveredSeries{
				Title:     "Summer Tour",
				Type:      entity.SeriesTypeTour,
				SourceURL: "https://example.com/tour",
				Events:    []*entity.DiscoveredEvent{{ListedVenueName: "Zepp Tokyo", LocalDate: localDate}},
			},
			wantKeys:       []string{"title", "type", "source_url", "events"},
			wantAbsentKeys: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			data, err := json.Marshal(tt.series)
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
