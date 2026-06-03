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
			name: "minimal concert with required fields only",
			args: &entity.Concert{
				Event: entity.Event{
					ID:        "event-id-1",
					VenueID:   "venue-id-1",
					LocalDate: localDate,
				},
				Series: &entity.Series{
					ID:    "series-id-1",
					Title: "Summer Live 2025",
					Type:  entity.SeriesTypeSingle,
				},
				Performers: []*entity.Artist{{ID: "artist-id-1", Name: "Sunny Day", MBID: "11111111-1111-1111-1111-111111111111"}},
			},
			want: &entityv1.Concert{
				Id:        &entityv1.EventId{Value: "event-id-1"},
				VenueId:   &entityv1.VenueId{Value: "venue-id-1"},
				LocalDate: &entityv1.LocalDate{Value: &date.Date{Year: 2025, Month: 6, Day: 15}},
				Series: &entityv1.Series{
					Id:    &entityv1.SeriesId{Value: "series-id-1"},
					Title: &entityv1.Title{Value: "Summer Live 2025"},
					Type:  entityv1.SeriesType_SERIES_TYPE_SINGLE,
				},
				Performers: []*entityv1.Artist{{
					Id:   &entityv1.ArtistId{Value: "artist-id-1"},
					Name: &entityv1.ArtistName{Value: "Sunny Day"},
					Mbid: &entityv1.Mbid{Value: "11111111-1111-1111-1111-111111111111"},
				}},
			},
		},
		{
			name: "concert with all optional fields",
			args: &entity.Concert{
				Event: entity.Event{
					ID:              "event-id-2",
					VenueID:         "venue-id-2",
					LocalDate:       localDate,
					StartTime:       &startTime,
					OpenTime:        &openTime,
					ListedVenueName: &listedVenueName,
				},
				Series: &entity.Series{
					ID:        "series-id-2",
					Title:     "Winter Tour",
					Type:      entity.SeriesTypeTour,
					SourceURL: "https://example.com/event",
					MerchURL:  "https://example.com/merch",
				},
				Performers: []*entity.Artist{{ID: "artist-id-2", Name: "Frostbite", MBID: "22222222-2222-2222-2222-222222222222"}},
			},
			want: func() *entityv1.Concert {
				p := &entityv1.Concert{
					Id:        &entityv1.EventId{Value: "event-id-2"},
					VenueId:   &entityv1.VenueId{Value: "venue-id-2"},
					LocalDate: &entityv1.LocalDate{Value: &date.Date{Year: 2025, Month: 6, Day: 15}},
					Series: &entityv1.Series{
						Id:        &entityv1.SeriesId{Value: "series-id-2"},
						Title:     &entityv1.Title{Value: "Winter Tour"},
						Type:      entityv1.SeriesType_SERIES_TYPE_TOUR,
						SourceUrl: &entityv1.Url{Value: "https://example.com/event"},
						MerchUrl:  &entityv1.Url{Value: "https://example.com/merch"},
					},
					Performers: []*entityv1.Artist{{
						Id:   &entityv1.ArtistId{Value: "artist-id-2"},
						Name: &entityv1.ArtistName{Value: "Frostbite"},
						Mbid: &entityv1.Mbid{Value: "22222222-2222-2222-2222-222222222222"},
					}},
					ListedVenueName: &entityv1.ListedVenueName{Value: listedVenueName},
					StartTime:       &entityv1.StartTime{Value: timestamppb.New(startTime)},
					OpenTime:        &entityv1.OpenTime{Value: timestamppb.New(openTime)},
				}
				return p
			}(),
		},
		{
			name: "concert with embedded venue",
			args: &entity.Concert{
				Event: entity.Event{
					ID:        "event-id-3",
					VenueID:   "venue-id-3",
					LocalDate: localDate,
					Venue: &entity.Venue{
						ID:        "venue-id-3",
						Name:      "Zepp Tokyo",
						AdminArea: &adminArea,
					},
				},
				Series: &entity.Series{
					ID:    "series-id-3",
					Title: "Rock Night",
					Type:  entity.SeriesTypeFestival,
				},
				Performers: []*entity.Artist{{ID: "artist-id-3", Name: "Loud", MBID: "33333333-3333-3333-3333-333333333333"}},
			},
			want: &entityv1.Concert{
				Id:      &entityv1.EventId{Value: "event-id-3"},
				VenueId: &entityv1.VenueId{Value: "venue-id-3"},
				LocalDate: &entityv1.LocalDate{
					Value: &date.Date{Year: 2025, Month: 6, Day: 15},
				},
				Series: &entityv1.Series{
					Id:    &entityv1.SeriesId{Value: "series-id-3"},
					Title: &entityv1.Title{Value: "Rock Night"},
					Type:  entityv1.SeriesType_SERIES_TYPE_FESTIVAL,
				},
				Performers: []*entityv1.Artist{{
					Id:   &entityv1.ArtistId{Value: "artist-id-3"},
					Name: &entityv1.ArtistName{Value: "Loud"},
					Mbid: &entityv1.Mbid{Value: "33333333-3333-3333-3333-333333333333"},
				}},
				Venue: &entityv1.Venue{
					Id:        &entityv1.VenueId{Value: "venue-id-3"},
					Name:      &entityv1.VenueName{Value: "Zepp Tokyo"},
					AdminArea: &entityv1.AdminArea{Value: adminArea},
				},
			},
		},
		{
			name: "concert with empty source URL omits source_url field",
			args: &entity.Concert{
				Event: entity.Event{
					ID:        "event-id-4",
					VenueID:   "venue-id-4",
					LocalDate: localDate,
				},
				Series: &entity.Series{
					ID:    "series-id-4",
					Title: "Acoustic Session",
					Type:  entity.SeriesTypeSingle,
				},
				Performers: []*entity.Artist{{ID: "artist-id-4", Name: "Quiet", MBID: "44444444-4444-4444-4444-444444444444"}},
			},
			want: &entityv1.Concert{
				Id:        &entityv1.EventId{Value: "event-id-4"},
				VenueId:   &entityv1.VenueId{Value: "venue-id-4"},
				LocalDate: &entityv1.LocalDate{Value: &date.Date{Year: 2025, Month: 6, Day: 15}},
				Series: &entityv1.Series{
					Id:    &entityv1.SeriesId{Value: "series-id-4"},
					Title: &entityv1.Title{Value: "Acoustic Session"},
					Type:  entityv1.SeriesType_SERIES_TYPE_SINGLE,
				},
				Performers: []*entityv1.Artist{{
					Id:   &entityv1.ArtistId{Value: "artist-id-4"},
					Name: &entityv1.ArtistName{Value: "Quiet"},
					Mbid: &entityv1.Mbid{Value: "44444444-4444-4444-4444-444444444444"},
				}},
			},
		},
		{
			name: "concert with multiple performers (festival lineup)",
			args: &entity.Concert{
				Event: entity.Event{
					ID:        "event-id-5",
					VenueID:   "venue-id-5",
					LocalDate: localDate,
				},
				Series: &entity.Series{
					ID:    "series-id-5",
					Title: "Mini Fest",
					Type:  entity.SeriesTypeFestival,
				},
				Performers: []*entity.Artist{
					{ID: "headliner", Name: "Top Bill", MBID: "55555555-5555-5555-5555-555555555555"},
					{ID: "support", Name: "Mid Card", MBID: "66666666-6666-6666-6666-666666666666"},
					{ID: "opener", Name: "Early Set", MBID: "77777777-7777-7777-7777-777777777777"},
				},
			},
			want: &entityv1.Concert{
				Id:        &entityv1.EventId{Value: "event-id-5"},
				VenueId:   &entityv1.VenueId{Value: "venue-id-5"},
				LocalDate: &entityv1.LocalDate{Value: &date.Date{Year: 2025, Month: 6, Day: 15}},
				Series: &entityv1.Series{
					Id:    &entityv1.SeriesId{Value: "series-id-5"},
					Title: &entityv1.Title{Value: "Mini Fest"},
					Type:  entityv1.SeriesType_SERIES_TYPE_FESTIVAL,
				},
				Performers: []*entityv1.Artist{
					{
						Id:   &entityv1.ArtistId{Value: "headliner"},
						Name: &entityv1.ArtistName{Value: "Top Bill"},
						Mbid: &entityv1.Mbid{Value: "55555555-5555-5555-5555-555555555555"},
					},
					{
						Id:   &entityv1.ArtistId{Value: "support"},
						Name: &entityv1.ArtistName{Value: "Mid Card"},
						Mbid: &entityv1.Mbid{Value: "66666666-6666-6666-6666-666666666666"},
					},
					{
						Id:   &entityv1.ArtistId{Value: "opener"},
						Name: &entityv1.ArtistName{Value: "Early Set"},
						Mbid: &entityv1.Mbid{Value: "77777777-7777-7777-7777-777777777777"},
					},
				},
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

func TestConcertsToProto(t *testing.T) {
	t.Parallel()

	localDate := time.Date(2025, 7, 1, 0, 0, 0, 0, time.UTC)

	concerts := []*entity.Concert{
		{
			Event:      entity.Event{ID: "event-1", VenueID: "venue-1", LocalDate: localDate},
			Series:     &entity.Series{ID: "series-1", Title: "Concert 1", Type: entity.SeriesTypeSingle},
			Performers: []*entity.Artist{{ID: "artist-1", Name: "First", MBID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"}},
		},
		{
			Event:      entity.Event{ID: "event-2", VenueID: "venue-2", LocalDate: localDate},
			Series:     &entity.Series{ID: "series-2", Title: "Concert 2", Type: entity.SeriesTypeSingle},
			Performers: []*entity.Artist{{ID: "artist-2", Name: "Second", MBID: "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"}},
		},
	}

	got := mapper.ConcertsToProto(concerts)

	require.Len(t, got, 2)
	assert.Equal(t, "event-1", got[0].GetId().GetValue())
	assert.Equal(t, "Concert 1", got[0].GetSeries().GetTitle().GetValue())
	require.Len(t, got[0].GetPerformers(), 1)
	assert.Equal(t, "artist-1", got[0].GetPerformers()[0].GetId().GetValue())
	assert.Equal(t, "event-2", got[1].GetId().GetValue())
	assert.Equal(t, "Concert 2", got[1].GetSeries().GetTitle().GetValue())
	require.Len(t, got[1].GetPerformers(), 1)
	assert.Equal(t, "artist-2", got[1].GetPerformers()[0].GetId().GetValue())
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

	groups := []*entity.ProximityGroup{
		{
			Date: date1,
			Home: []*entity.Concert{
				{
					Event:      entity.Event{ID: "home-1", VenueID: "v1", LocalDate: date1},
					Series:     &entity.Series{Title: "Home Concert"},
					Performers: []*entity.Artist{{ID: "artist-1"}},
				},
			},
			Nearby: []*entity.Concert{},
			Away:   []*entity.Concert{},
		},
		{
			Date: date2,
			Home: []*entity.Concert{},
			Nearby: []*entity.Concert{
				{
					Event:      entity.Event{ID: "nearby-1", VenueID: "v2", LocalDate: date2},
					Series:     &entity.Series{Title: "Nearby Concert"},
					Performers: []*entity.Artist{{ID: "artist-2"}},
				},
			},
			Away: []*entity.Concert{
				{
					Event:      entity.Event{ID: "away-1", VenueID: "v3", LocalDate: date2},
					Series:     &entity.Series{Title: "Away Concert"},
					Performers: []*entity.Artist{{ID: "artist-3"}},
				},
			},
		},
	}

	got := mapper.ProximityGroupsToProto(groups)

	require.Len(t, got, 2)

	// First group: home concert on date1
	assert.Equal(t, int32(2025), got[0].GetDate().GetValue().GetYear())
	assert.Equal(t, int32(8), got[0].GetDate().GetValue().GetMonth())
	assert.Equal(t, int32(10), got[0].GetDate().GetValue().GetDay())
	require.Len(t, got[0].GetHome(), 1)
	assert.Equal(t, "home-1", got[0].GetHome()[0].GetId().GetValue())
	assert.Empty(t, got[0].GetNearby())
	assert.Empty(t, got[0].GetAway())

	// Second group: nearby and away concerts on date2
	assert.Equal(t, int32(11), got[1].GetDate().GetValue().GetDay())
	assert.Empty(t, got[1].GetHome())
	require.Len(t, got[1].GetNearby(), 1)
	assert.Equal(t, "nearby-1", got[1].GetNearby()[0].GetId().GetValue())
	require.Len(t, got[1].GetAway(), 1)
	assert.Equal(t, "away-1", got[1].GetAway()[0].GetId().GetValue())
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
