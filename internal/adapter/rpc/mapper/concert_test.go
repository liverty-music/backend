package mapper_test

import (
	"testing"
	"time"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	concertv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/concert/v1"
	"github.com/liverty-music/backend/internal/adapter/rpc/mapper"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/type/date"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestConcertToProto(t *testing.T) {
	t.Parallel()

	adminArea := "Tokyo"
	listedVenueName := "Budokan"

	startTime := time.Date(2025, 6, 15, 18, 0, 0, 0, time.UTC)
	openTime := time.Date(2025, 6, 15, 17, 0, 0, 0, time.UTC)
	localDate := time.Date(2025, 6, 15, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		args *entity.Concert
		want *entityv1.Concert
	}{
		{
			name: "nil concert returns nil",
			args: nil,
			want: nil,
		},
		{
			// @spec components/entity/concert "Concert id is its event id"
			name: "carry the event, whose id is the concert's id, and the artist id",
			args: &entity.Concert{
				ID: "event-id-1", SeriesID: "series-id-1", VenueID: "venue-id-1", LocalDate: localDate,
				Series:  &entity.Series{ID: "series-id-1", Title: "Summer Live 2025", Type: entity.SeriesTypeSingle},
				Artists: []*entity.Artist{{ID: "artist-id-1", Name: "Sunny Day"}},
			},
			want: &entityv1.Concert{
				Event: &entityv1.Event{
					Id:        &entityv1.EventId{Value: "event-id-1"},
					LocalDate: &entityv1.LocalDate{Value: &date.Date{Year: 2025, Month: 6, Day: 15}},
					SeriesId:  &entityv1.SeriesId{Value: "series-id-1"},
				},
				ArtistIds: []*entityv1.ArtistId{{Value: "artist-id-1"}},
			},
		},
		{
			name: "carry every optional event field and the venue",
			args: &entity.Concert{
				ID: "event-id-2", SeriesID: "series-id-2", VenueID: "venue-id-2", LocalDate: localDate,
				StartTime: &startTime, OpenTime: &openTime, ListedVenueName: &listedVenueName,
				Venue:   &entity.Venue{ID: "venue-id-2", Name: "Nippon Budokan", AdminArea: &adminArea},
				Series:  &entity.Series{ID: "series-id-2", Title: "Winter Tour", Type: entity.SeriesTypeTour},
				Artists: []*entity.Artist{{ID: "artist-id-2", Name: "Frostbite"}},
			},
			want: &entityv1.Concert{
				Event: &entityv1.Event{
					Id: &entityv1.EventId{Value: "event-id-2"},
					Venue: &entityv1.Venue{
						Id:        &entityv1.VenueId{Value: "venue-id-2"},
						Name:      &entityv1.VenueName{Value: "Nippon Budokan"},
						AdminArea: &entityv1.AdminArea{Value: adminArea},
					},
					LocalDate:       &entityv1.LocalDate{Value: &date.Date{Year: 2025, Month: 6, Day: 15}},
					StartTime:       &entityv1.StartTime{Value: timestamppb.New(startTime)},
					OpenTime:        &entityv1.OpenTime{Value: timestamppb.New(openTime)},
					SeriesId:        &entityv1.SeriesId{Value: "series-id-2"},
					ListedVenueName: &entityv1.ListedVenueName{Value: listedVenueName},
				},
				ArtistIds: []*entityv1.ArtistId{{Value: "artist-id-2"}},
			},
		},
		{
			// @spec components/entity/concert "Co-headlined concert"
			name: "list both co-headlining artists, each once",
			args: &entity.Concert{
				ID: "event-id-5", SeriesID: "series-id-5", LocalDate: localDate,
				Series: &entity.Series{ID: "series-id-5", Title: "Double Bill", Type: entity.SeriesTypeSingle},
				Artists: []*entity.Artist{
					{ID: "headliner-a", Name: "Top Bill"},
					{ID: "headliner-b", Name: "Co Bill"},
				},
			},
			want: &entityv1.Concert{
				Event: &entityv1.Event{
					Id:        &entityv1.EventId{Value: "event-id-5"},
					LocalDate: &entityv1.LocalDate{Value: &date.Date{Year: 2025, Month: 6, Day: 15}},
					SeriesId:  &entityv1.SeriesId{Value: "series-id-5"},
				},
				ArtistIds: []*entityv1.ArtistId{{Value: "headliner-a"}, {Value: "headliner-b"}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := mapper.ConcertToProto(tt.args)

			if tt.want == nil {
				assert.Nil(t, got)
				return
			}

			require.NotNil(t, got)
			assert.Equal(t, tt.want.String(), got.String())
		})
	}
}

func TestReferencedSeriesAndArtists(t *testing.T) {
	t.Parallel()

	localDate := time.Date(2025, 7, 1, 0, 0, 0, 0, time.UTC)
	concert := func(eventID, seriesID, title string, artists ...*entity.Artist) *entity.Concert {
		return &entity.Concert{
			ID: eventID, SeriesID: seriesID, LocalDate: localDate,
			Series:  &entity.Series{ID: seriesID, Title: title, Type: entity.SeriesTypeTour},
			Artists: artists,
		}
	}
	a := &entity.Artist{ID: "artist-a", Name: "A"}
	b := &entity.Artist{ID: "artist-b", Name: "B"}

	t.Run("return each series and artist once, in first-seen order", func(t *testing.T) {
		t.Parallel()
		concerts := []*entity.Concert{
			concert("event-1", "series-2", "Tour Two", b),
			concert("event-2", "series-1", "Tour One", a, b),
			concert("event-3", "series-2", "Tour Two", b),
		}

		series := mapper.NewMediaURLBuilder("").ReferencedSeries(concerts)
		artists := mapper.ReferencedArtists(concerts)

		require.Len(t, series, 2)
		assert.Equal(t, "series-2", series[0].GetId().GetValue())
		assert.Equal(t, "series-1", series[1].GetId().GetValue())
		require.Len(t, artists, 2)
		assert.Equal(t, "artist-b", artists[0].GetId().GetValue())
		assert.Equal(t, "artist-a", artists[1].GetId().GetValue())
	})

	// @spec components/entity/concert "Title comes from the series"
	t.Run("give the concert the title of the series its event belongs to", func(t *testing.T) {
		t.Parallel()
		c := concert("event-1", "series-1", "ARENA TOUR 2026", a)

		proto := mapper.ConcertToProto(c)
		series := mapper.NewMediaURLBuilder("").ReferencedSeries([]*entity.Concert{c})

		require.Len(t, series, 1)
		assert.Equal(t, proto.GetEvent().GetSeriesId().GetValue(), series[0].GetId().GetValue())
		assert.Equal(t, "ARENA TOUR 2026", series[0].GetTitle().GetValue())
	})

	t.Run("return empty lists for no concerts", func(t *testing.T) {
		t.Parallel()
		assert.Empty(t, mapper.NewMediaURLBuilder("").ReferencedSeries(nil))
		assert.Empty(t, mapper.ReferencedArtists(nil))
	})
}

func TestConcertsToProto_empty(t *testing.T) {
	t.Parallel()

	got := mapper.ConcertsToProto([]*entity.Concert{})
	assert.Empty(t, got)
}

func TestProximityGroupsToProto(t *testing.T) {
	t.Parallel()

	date1 := time.Date(2025, 8, 10, 0, 0, 0, 0, time.UTC)
	date2 := time.Date(2025, 8, 11, 0, 0, 0, 0, time.UTC)
	concert := func(id string, d time.Time) *entity.Concert {
		return &entity.Concert{
			ID: id, LocalDate: d,
			Series:  &entity.Series{ID: "series-" + id},
			Artists: []*entity.Artist{{ID: "artist-" + id}},
		}
	}

	groups := []*entity.ProximityGroup{
		{Date: date1, Home: []*entity.Concert{concert("home-1", date1)}, Nearby: []*entity.Concert{}, Away: []*entity.Concert{}},
		{
			Date:   date2,
			Home:   []*entity.Concert{},
			Nearby: []*entity.Concert{concert("nearby-1", date2)},
			Away:   []*entity.Concert{concert("away-1", date2)},
		},
	}

	got := mapper.ProximityGroupsToProto(groups)

	require.Len(t, got, 2)

	// First group: home concert on date1
	assert.Equal(t, int32(2025), got[0].GetDate().GetValue().GetYear())
	assert.Equal(t, int32(8), got[0].GetDate().GetValue().GetMonth())
	assert.Equal(t, int32(10), got[0].GetDate().GetValue().GetDay())
	require.Len(t, got[0].GetHome(), 1)
	assert.Equal(t, "home-1", got[0].GetHome()[0].GetEvent().GetId().GetValue())
	assert.Empty(t, got[0].GetNearby())
	assert.Empty(t, got[0].GetAway())

	// Second group: nearby and away concerts on date2
	assert.Equal(t, int32(11), got[1].GetDate().GetValue().GetDay())
	assert.Empty(t, got[1].GetHome())
	require.Len(t, got[1].GetNearby(), 1)
	assert.Equal(t, "nearby-1", got[1].GetNearby()[0].GetEvent().GetId().GetValue())
	require.Len(t, got[1].GetAway(), 1)
	assert.Equal(t, "away-1", got[1].GetAway()[0].GetEvent().GetId().GetValue())

	// ConcertsOfGroups flattens in group order, then home, nearby, away.
	flat := mapper.ConcertsOfGroups(groups)
	require.Len(t, flat, 3)
	assert.Equal(t, []string{"home-1", "nearby-1", "away-1"}, []string{flat[0].ID, flat[1].ID, flat[2].ID})
}

func TestProximityGroupsToProto_empty(t *testing.T) {
	t.Parallel()

	got := mapper.ProximityGroupsToProto([]*entity.ProximityGroup{})
	assert.Empty(t, got)
}

func TestVenueToProto(t *testing.T) {
	t.Parallel()

	adminArea := "Osaka"

	tests := []struct {
		name string
		args *entity.Venue
		want *entityv1.Venue
	}{
		{
			name: "nil venue returns nil",
			args: nil,
			want: nil,
		},
		{
			name: "venue without admin area",
			args: &entity.Venue{
				ID:   "venue-id-1",
				Name: "Zepp Namba",
			},
			want: &entityv1.Venue{
				Id:   &entityv1.VenueId{Value: "venue-id-1"},
				Name: &entityv1.VenueName{Value: "Zepp Namba"},
			},
		},
		{
			// @spec components/adapter/fan/api/rpc/concert "Venue without coordinates"
			name: "venue with known coordinates returns its id, name and admin area only",
			args: &entity.Venue{
				ID:          "venue-id-3",
				Name:        "Zepp Haneda",
				AdminArea:   &adminArea,
				Coordinates: &entity.Coordinates{Latitude: 35.55, Longitude: 139.75},
			},
			want: &entityv1.Venue{
				Id:        &entityv1.VenueId{Value: "venue-id-3"},
				Name:      &entityv1.VenueName{Value: "Zepp Haneda"},
				AdminArea: &entityv1.AdminArea{Value: adminArea},
			},
		},
		{
			name: "venue with admin area",
			args: &entity.Venue{
				ID:        "venue-id-2",
				Name:      "Zepp Osaka Bayside",
				AdminArea: &adminArea,
			},
			want: &entityv1.Venue{
				Id:        &entityv1.VenueId{Value: "venue-id-2"},
				Name:      &entityv1.VenueName{Value: "Zepp Osaka Bayside"},
				AdminArea: &entityv1.AdminArea{Value: adminArea},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := mapper.VenueToProto(tt.args)

			if tt.want == nil {
				assert.Nil(t, got)
				return
			}

			require.NotNil(t, got)
			assert.Equal(t, tt.want.String(), got.String())
		})
	}
}

func TestTimeToDate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args time.Time
		want *date.Date
	}{
		{
			name: "converts standard date",
			args: time.Date(2025, 3, 28, 0, 0, 0, 0, time.UTC),
			want: &date.Date{Year: 2025, Month: 3, Day: 28},
		},
		{
			name: "converts date with non-zero time components (time components are ignored)",
			args: time.Date(2024, 12, 31, 23, 59, 59, 0, time.UTC),
			want: &date.Date{Year: 2024, Month: 12, Day: 31},
		},
		{
			name: "converts zero time to zero date",
			args: time.Time{},
			want: &date.Date{Year: 1, Month: 1, Day: 1},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := mapper.TimeToDate(tt.args)

			require.NotNil(t, got)
			assert.Equal(t, tt.want.GetYear(), got.GetYear())
			assert.Equal(t, tt.want.GetMonth(), got.GetMonth())
			assert.Equal(t, tt.want.GetDay(), got.GetDay())
		})
	}
}

// Ensure concertv1 import is used to satisfy the compiler when the package
// is referenced through ProximityGroupsToProto's return type.
var _ []*concertv1.ProximityGroup
