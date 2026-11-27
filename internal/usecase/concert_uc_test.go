package usecase_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/usecase"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Default skip windows used to construct the use case in tests; they mirror the
// production defaults so the timing-boundary cases reason about a 24h TTL.
const (
	testSearchCacheTTL  = 24 * time.Hour
	testDiscoveryWindow = 14 * 24 * time.Hour
)

// concertTestDeps holds all dependencies for ConcertUseCase tests.
type concertTestDeps struct {
	artistRepo           *mocks.MockArtistRepository
	concertRepo          *mocks.MockConcertRepository
	venueRepo            *mocks.MockVenueRepository
	seriesRepo           *mocks.MockSeriesRepository
	concertSearchRepo    *mocks.MockConcertSearchRepository
	concertCandidateRepo *mocks.MockConcertCandidateRepository
	rejectedConcertRepo  *mocks.MockRejectedConcertRepository
	searcher             *mocks.MockConcertSearcher
	areas                *mocks.MockAreaRepository
	workflows            *ucmocks.MockWorkflows
	analytics            *ucmocks.MockAnalyticsClient
	// starts lists the workflows uc started.
	starts func() []workflowStart
	// signals holds the analytics events uc captured.
	signals *capturedAnalytics
	uc      usecase.ConcertUseCase
}

// newAreas returns an AreaRepository mock. A test that resolves an area sets
// its own expectation.
func newAreas(t *testing.T) *mocks.MockAreaRepository {
	t.Helper()
	return mocks.NewMockAreaRepository(t)
}

// firstLevelArea is a first-level Area with a reference point, or none when
// point is nil.
func firstLevelArea(code string, point *entity.Coordinates) *entity.Area {
	a := &entity.Area{Code: code, Kind: entity.AreaKindSubdivision, CountryCode: code[:2], NameEN: code, ReferencePoint: point}
	if point != nil {
		src := entity.ReferencePointLabel
		a.ReferencePointSource = &src
	}
	return a
}

func newConcertTestDeps(t *testing.T) *concertTestDeps {
	t.Helper()
	logger := newTestLogger(t)
	d := &concertTestDeps{
		artistRepo:           mocks.NewMockArtistRepository(t),
		concertRepo:          mocks.NewMockConcertRepository(t),
		venueRepo:            mocks.NewMockVenueRepository(t),
		seriesRepo:           mocks.NewMockSeriesRepository(t),
		concertSearchRepo:    mocks.NewMockConcertSearchRepository(t),
		concertCandidateRepo: mocks.NewMockConcertCandidateRepository(t),
		rejectedConcertRepo:  mocks.NewMockRejectedConcertRepository(t),
		searcher:             mocks.NewMockConcertSearcher(t),
		areas:                newAreas(t),
		workflows:            ucmocks.NewMockWorkflows(t),
		analytics:            ucmocks.NewMockAnalyticsClient(t),
	}
	d.starts = captureStarts(d.workflows)
	d.signals = captureAnalytics(d.analytics, nil)
	uc := usecase.NewConcertUseCase(d.artistRepo, d.concertRepo, d.venueRepo, d.seriesRepo, nil, d.concertSearchRepo, d.concertCandidateRepo, d.rejectedConcertRepo, d.searcher, d.areas, d.workflows, anyConcertMetrics(t), d.analytics, testSearchCacheTTL, testDiscoveryWindow, time.Now, logger)
	d.uc = uc
	return d
}

func TestConcertUseCase_ListConcertsByArtist(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	lastMonth := time.Now().AddDate(0, -1, 0)
	nextMonth := time.Now().AddDate(0, 1, 0)
	past := &entity.Concert{ID: "c-past", LocalDate: lastMonth, Series: &entity.Series{Title: "Last Month"}, Artists: []*entity.Artist{{ID: "a1"}}}
	future := &entity.Concert{ID: "c-future", LocalDate: nextMonth, Series: &entity.Series{Title: "Next Month"}, Artists: []*entity.Artist{{ID: "a1"}}}

	tests := []struct {
		name     string
		artistID string
		setup    func(d *concertTestDeps)
		want     []string
		wantErr  error
	}{
		{
			// @spec components/usecase/concert/list-by-artist "Past concerts included"
			name:     "return last month's and next month's concerts, last month's first",
			artistID: "a1",
			setup: func(d *concertTestDeps) {
				// Concert.ListByArtist lists every date, oldest first, with no
				// date cut; the usecase passes it through.
				d.concertRepo.EXPECT().ListByArtist(ctx, "a1").Return([]*entity.Concert{past, future}, nil).Once()
			},
			want: []string{"c-past", "c-future"},
		},
		{
			// @spec components/usecase/concert/list-by-artist "No concerts"
			name:     "return an empty list for an artist with no concert",
			artistID: "a1",
			setup: func(d *concertTestDeps) {
				d.concertRepo.EXPECT().ListByArtist(ctx, "a1").Return(nil, nil).Once()
			},
			want: nil,
		},
		{
			// @spec components/usecase/concert/list-by-artist "Unknown artist"
			name:     "return an empty list, not NotFound, for an artist id no artist has",
			artistID: "artist-unknown",
			setup: func(d *concertTestDeps) {
				// Concert.ListByArtist yields an empty list for an unknown
				// Artist (entity/concert/list-by-artist).
				d.concertRepo.EXPECT().ListByArtist(ctx, "artist-unknown").Return(nil, nil).Once()
			},
			want: nil,
		},
		{
			// @spec components/usecase/concert/list-by-artist "Missing artist"
			name:     "fail with InvalidArgument when no artist id is given",
			artistID: "",
			setup: func(d *concertTestDeps) {
				d.concertRepo.EXPECT().ListByArtist(ctx, "").
					Return(nil, apperr.New(codes.InvalidArgument, "artist id must not be empty")).Once()
			},
			wantErr: apperr.ErrInvalidArgument,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d := newConcertTestDeps(t)
			tt.setup(d)

			got, err := d.uc.ListByArtist(ctx, tt.artistID)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			ids := make([]string, 0, len(got))
			for _, c := range got {
				ids = append(ids, c.ID)
			}
			if tt.want == nil {
				assert.Empty(t, got)
			} else {
				assert.Equal(t, tt.want, ids)
			}
		})
	}
}

func TestConcertUseCase_ListByFollowerGrouped(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	date1 := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	date2 := time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC)

	tokyoLat := 35.6894
	tokyoLng := 139.6917
	saitamaLat := 35.8569
	saitamaLng := 139.6489
	osakaLat := 34.6863
	osakaLng := 135.5200

	t.Run("classifies concerts into home/nearby/away by date", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/concert/list-followed "Upcoming by default"
		// No from-date reaches the repository, which then counts each Concert's
		// date against the current date at its own Venue.
		d := newConcertTestDeps(t)

		home := &entity.Home{Level1: "JP-13", ReferencePoint: &entity.Coordinates{Latitude: 35.6762, Longitude: 139.6503}} // Tokyo

		concerts := []*entity.Concert{
			// Date 1: Tokyo venue (HOME), Saitama venue (NEARBY), Osaka venue (AWAY)
			{
				ID: "c1", LocalDate: date1, Venue: &entity.Venue{ID: "v1", AdminArea: new("JP-13"), Coordinates: &entity.Coordinates{Latitude: tokyoLat, Longitude: tokyoLng}},
				Artists: []*entity.Artist{{ID: "a1"}},
			},
			{
				ID: "c2", LocalDate: date1, Venue: &entity.Venue{ID: "v2", AdminArea: new("JP-11"), Coordinates: &entity.Coordinates{Latitude: saitamaLat, Longitude: saitamaLng}},
				Artists: []*entity.Artist{{ID: "a1"}},
			},
			{
				ID: "c3", LocalDate: date1, Venue: &entity.Venue{ID: "v3", AdminArea: new("JP-27"), Coordinates: &entity.Coordinates{Latitude: osakaLat, Longitude: osakaLng}},
				Artists: []*entity.Artist{{ID: "a1"}},
			},
			// Date 2: No venue coordinates (AWAY)
			{
				ID: "c4", LocalDate: date2, Venue: &entity.Venue{ID: "v4", AdminArea: new("JP-40")},
				Artists: []*entity.Artist{{ID: "a2"}},
			},
		}
		d.concertRepo.EXPECT().ListByFollower(ctx, "user-1", (*time.Time)(nil)).Return(concerts, nil).Once()

		groups, err := d.uc.ListFollowed(ctx, "user-1", home, nil)
		assert.NoError(t, err)
		assert.Len(t, groups, 2)

		// Date 1
		assert.Equal(t, date1, groups[0].Date)
		assert.Len(t, groups[0].Home, 1)
		assert.Equal(t, "c1", groups[0].Home[0].ID)
		assert.Len(t, groups[0].Nearby, 1)
		assert.Equal(t, "c2", groups[0].Nearby[0].ID)
		assert.Len(t, groups[0].Away, 1)
		assert.Equal(t, "c3", groups[0].Away[0].ID)

		// Date 2
		assert.Equal(t, date2, groups[1].Date)
		assert.Len(t, groups[1].Home, 0)
		assert.Len(t, groups[1].Nearby, 0)
		assert.Len(t, groups[1].Away, 1)
		assert.Equal(t, "c4", groups[1].Away[0].ID)
	})

	t.Run("no home set puts everything in away", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/concert/list-followed "No home area"
		d := newConcertTestDeps(t)

		concerts := []*entity.Concert{
			{ID: "c1", LocalDate: date1, Venue: &entity.Venue{ID: "v1", AdminArea: new("JP-13"), Coordinates: &entity.Coordinates{Latitude: tokyoLat, Longitude: tokyoLng}}},
		}
		d.concertRepo.EXPECT().ListByFollower(ctx, "user-2", (*time.Time)(nil)).Return(concerts, nil).Once()

		groups, err := d.uc.ListFollowed(ctx, "user-2", nil, nil)
		assert.NoError(t, err)
		assert.Len(t, groups, 1)
		assert.Len(t, groups[0].Home, 0)
		assert.Len(t, groups[0].Nearby, 0)
		assert.Len(t, groups[0].Away, 1)
	})

	t.Run("empty concerts returns nil groups", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/concert/list-followed "Follows nothing"
		d := newConcertTestDeps(t)

		home := &entity.Home{Level1: "JP-13"}
		d.concertRepo.EXPECT().ListByFollower(ctx, "user-3", (*time.Time)(nil)).Return(nil, nil).Once()

		groups, err := d.uc.ListFollowed(ctx, "user-3", home, nil)
		assert.NoError(t, err)
		assert.Nil(t, groups)
	})

	t.Run("threads an explicit past from through to the repository", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/concert/list-followed "From a past date"
		d := newConcertTestDeps(t)

		home := &entity.Home{Level1: "JP-13"}
		past := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
		d.concertRepo.EXPECT().ListByFollower(ctx, "user-4", &past).Return(nil, nil).Once()

		groups, err := d.uc.ListFollowed(ctx, "user-4", home, &past)
		assert.NoError(t, err)
		assert.Nil(t, groups)
	})
}

func TestConcertUseCase_ListByArtists(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	date1 := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	fukuoka := &entity.Coordinates{Latitude: 33.6064, Longitude: 130.4181}
	// Saga city, about 50 km from Fukuoka's reference point, in JP-41.
	saga := &entity.Coordinates{Latitude: 33.2635, Longitude: 130.3009}
	tokyo := &entity.Coordinates{Latitude: 35.6894, Longitude: 139.6917}

	concertsAt := func(venues ...*entity.Venue) []*entity.Concert {
		out := make([]*entity.Concert, 0, len(venues))
		for i, v := range venues {
			out = append(out, &entity.Concert{ID: fmt.Sprintf("c%d", i), LocalDate: date1, Venue: v, Series: &entity.Series{Title: "Tour"}})
		}
		return out
	}

	t.Run("Home given by prefecture only", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/concert/list-by-artists "Home given by prefecture only"
		d := newConcertTestDeps(t)
		d.areas.EXPECT().Resolve(ctx, "JP-40", (*string)(nil)).Return(firstLevelArea("JP-40", fukuoka), nil).Once()
		d.concertRepo.EXPECT().ListByArtists(ctx, []string{"a1"}).Return(concertsAt(
			&entity.Venue{AdminArea: new("JP-41"), Coordinates: saga},
			&entity.Venue{AdminArea: new("JP-13"), Coordinates: tokyo},
		), nil).Once()

		groups, err := d.uc.ListByArtists(ctx, []string{"a1"}, &entity.Home{CountryCode: "JP", Level1: "JP-40"})
		require.NoError(t, err)
		require.Len(t, groups, 1)
		assert.Len(t, groups[0].Nearby, 1, "Saga is within 200 km of the Area's reference point")
		assert.Len(t, groups[0].Away, 1)
	})
	t.Run("Overseas home", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/concert/list-by-artists "Overseas home"
		d := newConcertTestDeps(t)
		// Sacramento, and Reno (US-NV) about 180 km away.
		d.areas.EXPECT().Resolve(ctx, "US-CA", (*string)(nil)).Return(firstLevelArea("US-CA", &entity.Coordinates{Latitude: 38.5816, Longitude: -121.4944}), nil).Once()
		d.concertRepo.EXPECT().ListByArtists(ctx, []string{"a1"}).Return(concertsAt(
			&entity.Venue{AdminArea: new("US-NV"), Coordinates: &entity.Coordinates{Latitude: 39.5296, Longitude: -119.8138}},
		), nil).Once()

		groups, err := d.uc.ListByArtists(ctx, []string{"a1"}, &entity.Home{CountryCode: "US", Level1: "US-CA"})
		require.NoError(t, err)
		require.Len(t, groups, 1)
		assert.Len(t, groups[0].Nearby, 1)
	})
	t.Run("Area without a reference point", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/concert/list-by-artists "Area without a reference point"
		d := newConcertTestDeps(t)
		d.areas.EXPECT().Resolve(ctx, "HK", (*string)(nil)).Return(&entity.Area{Code: "HK", Kind: entity.AreaKindCountry, CountryCode: "HK", NameEN: "Hong Kong"}, nil).Once()
		d.concertRepo.EXPECT().ListByArtists(ctx, []string{"a1"}).Return(concertsAt(
			&entity.Venue{AdminArea: new("HK")},
			&entity.Venue{AdminArea: new("MO"), Coordinates: &entity.Coordinates{Latitude: 22.19, Longitude: 113.54}},
		), nil).Once()

		groups, err := d.uc.ListByArtists(ctx, []string{"a1"}, &entity.Home{CountryCode: "HK", Level1: "HK"})
		require.NoError(t, err)
		require.Len(t, groups, 1)
		assert.Len(t, groups[0].Home, 1)
		assert.Len(t, groups[0].Away, 1, "nothing is NEARBY without a reference point")
	})
	t.Run("Code that is no first-level Area", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/concert/list-by-artists "Code that is no first-level Area"
		d := newConcertTestDeps(t)
		d.areas.EXPECT().Resolve(ctx, "JP-99", (*string)(nil)).Return(nil, apperr.New(codes.NotFound, "no area")).Once()
		d.concertRepo.EXPECT().ListByArtists(ctx, []string{"a1"}).Return(concertsAt(
			&entity.Venue{AdminArea: new("JP-41"), Coordinates: saga},
		), nil).Once()

		groups, err := d.uc.ListByArtists(ctx, []string{"a1"}, &entity.Home{CountryCode: "JP", Level1: "JP-99"})
		require.NoError(t, err)
		require.Len(t, groups, 1)
		assert.Len(t, groups[0].Away, 1)
	})
	t.Run("No home area", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/concert/list-by-artists "No home area"
		d := newConcertTestDeps(t)
		d.concertRepo.EXPECT().ListByArtists(ctx, []string{"a1"}).Return(concertsAt(&entity.Venue{}), nil).Once()

		groups, err := d.uc.ListByArtists(ctx, []string{"a1"}, nil)
		require.NoError(t, err)
		require.Len(t, groups, 1)
		assert.Len(t, groups[0].Away, 1)
		assert.Empty(t, groups[0].Home)
		assert.Empty(t, groups[0].Nearby)
	})
}

func TestConcertUseCase_ListByLocation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	from := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC)
	fukuokaPoint := &entity.Coordinates{Latitude: 33.6064, Longitude: 130.4181}

	t.Run("Prefecture code", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/concert/list-by-location "Prefecture code"
		d := newConcertTestDeps(t)
		area := firstLevelArea("JP-40", fukuokaPoint)
		d.areas.EXPECT().Resolve(ctx, "JP-40", (*string)(nil)).Return(area, nil).Once()

		date1 := time.Date(2026, 6, 5, 0, 0, 0, 0, time.UTC)
		date2 := time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC)
		concerts := []*entity.Concert{
			// @spec components/usecase/concert/list-by-location "Same admin area far away"
			{ID: "home1", LocalDate: date1, Venue: &entity.Venue{AdminArea: new("JP-40"), Coordinates: &entity.Coordinates{Latitude: 33.0, Longitude: 133.4}}},
			// @spec components/usecase/concert/list-by-location "Other admin area within 200 km"
			{ID: "near1", LocalDate: date1, Venue: &entity.Venue{AdminArea: new("JP-43"), Coordinates: &entity.Coordinates{Latitude: 32.8032, Longitude: 130.7079}}},
			// @spec components/usecase/concert/list-by-location "Beyond 200 km in another admin area"
			// @spec components/usecase/concert/list-by-location "Date with only far concerts"
			{ID: "away1", LocalDate: date2, Venue: &entity.Venue{AdminArea: new("JP-13"), Coordinates: &entity.Coordinates{Latitude: 35.6894, Longitude: 139.6917}}},
		}
		d.concertRepo.EXPECT().ListByLocation(ctx, entity.LocationOf(area), from, to).Return(concerts, nil).Once()

		groups, err := d.uc.ListByLocation(ctx, "JP-40", from, to)
		require.NoError(t, err)
		require.Len(t, groups, 1, "the AWAY-only date is dropped")
		assert.Len(t, groups[0].Home, 1, "a venue of the Area 300 km away is HOME")
		assert.Len(t, groups[0].Nearby, 1)
		assert.Empty(t, groups[0].Away)
	})
	t.Run("Territory code", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/concert/list-by-location "Territory code"
		// @spec components/usecase/concert/list-by-location "Area without a reference point"
		d := newConcertTestDeps(t)
		area := &entity.Area{Code: "HK", Kind: entity.AreaKindCountry, CountryCode: "HK", NameEN: "Hong Kong"}
		d.areas.EXPECT().Resolve(ctx, "HK", (*string)(nil)).Return(area, nil).Once()
		date1 := time.Date(2026, 6, 5, 0, 0, 0, 0, time.UTC)
		d.concertRepo.EXPECT().ListByLocation(ctx, &entity.GeoLocation{AdminArea: "HK"}, from, to).Return([]*entity.Concert{
			{ID: "hk", LocalDate: date1, Venue: &entity.Venue{AdminArea: new("HK")}},
			{ID: "mo", LocalDate: date1, Venue: &entity.Venue{AdminArea: new("MO"), Coordinates: &entity.Coordinates{Latitude: 22.19, Longitude: 113.54}}},
		}, nil).Once()

		groups, err := d.uc.ListByLocation(ctx, "HK", from, to)
		require.NoError(t, err)
		require.Len(t, groups, 1)
		require.Len(t, groups[0].Home, 1)
		assert.Equal(t, "hk", groups[0].Home[0].ID)
		assert.Empty(t, groups[0].Nearby, "nothing is NEARBY without a reference point")
	})
	t.Run("Nothing nearby", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/concert/list-by-location "Nothing nearby"
		d := newConcertTestDeps(t)
		area := firstLevelArea("JP-40", fukuokaPoint)
		d.areas.EXPECT().Resolve(ctx, "JP-40", (*string)(nil)).Return(area, nil).Once()
		d.concertRepo.EXPECT().ListByLocation(ctx, entity.LocationOf(area), from, to).Return(nil, nil).Once()

		groups, err := d.uc.ListByLocation(ctx, "JP-40", from, to)
		require.NoError(t, err)
		assert.Empty(t, groups)
	})
	for _, tc := range []struct {
		name, area string
		resolved   *entity.Area
	}{
		// @spec components/usecase/concert/list-by-location "Well-formed code that is no Area"
		{name: "Well-formed code that is no Area", area: "JP-99"},
		// @spec components/usecase/concert/list-by-location "Lower-level code"
		{name: "Lower-level code", area: "FR-33", resolved: firstLevelArea("FR-NAQ", nil)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d := newConcertTestDeps(t)
			if tc.resolved != nil {
				d.areas.EXPECT().Resolve(ctx, tc.area, (*string)(nil)).Return(tc.resolved, nil).Once()
			} else {
				d.areas.EXPECT().Resolve(ctx, tc.area, (*string)(nil)).Return(nil, apperr.New(codes.NotFound, "no area")).Once()
			}

			groups, err := d.uc.ListByLocation(ctx, tc.area, from, to)
			assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
			assert.Nil(t, groups)
		})
	}
	t.Run("rejects a date range exceeding 30 days", func(t *testing.T) {
		t.Parallel()
		d := newConcertTestDeps(t)

		groups, err := d.uc.ListByLocation(ctx, "JP-40", from, from.AddDate(0, 0, 31))
		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
		assert.Nil(t, groups)
	})
}
