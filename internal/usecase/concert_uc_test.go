package usecase_test

import (
	"context"
	"fmt"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/ThreeDotsLabs/watermill/pubsub/gochannel"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/infrastructure/messaging"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// concertTestDeps holds all dependencies for ConcertUseCase tests.
type concertTestDeps struct {
	artistRepo       *mocks.MockArtistRepository
	concertRepo      *mocks.MockConcertRepository
	venueRepo        *mocks.MockVenueRepository
	searchLogRepo    *mocks.MockSearchLogRepository
	searcher         *mocks.MockConcertSearcher
	centroidResolver usecase.CentroidResolver
	publisher        *gochannel.GoChannel
	uc               usecase.ConcertUseCase
}

// noopCentroidResolver is a test stub that always returns "not found".
// Tests that exercise centroid resolution should supply a custom resolver via
// usecase.NewConcertUseCase directly.
type noopCentroidResolver struct{}

func (noopCentroidResolver) ResolveCentroid(*entity.Home) (float64, float64, error) {
	return 0, 0, fmt.Errorf("centroid not found")
}

func newConcertTestDeps(t *testing.T) *concertTestDeps {
	t.Helper()
	logger := newTestLogger(t)
	pub := gochannel.NewGoChannel(gochannel.Config{OutputChannelBuffer: 64}, watermill.NopLogger{})
	d := &concertTestDeps{
		artistRepo:       mocks.NewMockArtistRepository(t),
		concertRepo:      mocks.NewMockConcertRepository(t),
		venueRepo:        mocks.NewMockVenueRepository(t),
		searchLogRepo:    mocks.NewMockSearchLogRepository(t),
		searcher:         mocks.NewMockConcertSearcher(t),
		centroidResolver: noopCentroidResolver{},
		publisher:        pub,
	}
	d.uc = usecase.NewConcertUseCase(d.artistRepo, d.concertRepo, d.venueRepo, d.searchLogRepo, d.searcher, d.centroidResolver, messaging.NewEventPublisher(pub), logger)
	t.Cleanup(func() { _ = pub.Close() })
	return d
}

func TestConcertUseCase_ListConcertsByArtist(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	type args struct {
		artistID string
	}

	tests := []struct {
		name    string
		args    args
		setup   func(t *testing.T, d *concertTestDeps)
		want    int
		wantErr error
	}{
		{
			name: "success",
			args: args{artistID: "a1"},
			setup: func(t *testing.T, d *concertTestDeps) {
				t.Helper()
				concerts := []*entity.Concert{
					{Event: entity.Event{ID: "c1", Title: "Concert 1"}, ArtistID: "a1"},
				}
				d.concertRepo.EXPECT().ListByArtist(ctx, "a1", false).Return(concerts, nil).Once()
			},
			want:    1,
			wantErr: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d := newConcertTestDeps(t)
			if tt.setup != nil {
				tt.setup(t, d)
			}

			got, err := d.uc.ListByArtist(ctx, tt.args.artistID)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}

			assert.NoError(t, err)
			assert.Len(t, got, tt.want)
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
		d := newConcertTestDeps(t)

		home := &entity.Home{Level1: "JP-13", Centroid: &entity.Coordinates{Latitude: 35.6762, Longitude: 139.6503}} // Tokyo

		concerts := []*entity.Concert{
			// Date 1: Tokyo venue (HOME), Saitama venue (NEARBY), Osaka venue (AWAY)
			{
				Event:    entity.Event{ID: "c1", LocalDate: date1, Venue: &entity.Venue{ID: "v1", AdminArea: strPtr("JP-13"), Coordinates: &entity.Coordinates{Latitude: tokyoLat, Longitude: tokyoLng}}},
				ArtistID: "a1",
			},
			{
				Event:    entity.Event{ID: "c2", LocalDate: date1, Venue: &entity.Venue{ID: "v2", AdminArea: strPtr("JP-11"), Coordinates: &entity.Coordinates{Latitude: saitamaLat, Longitude: saitamaLng}}},
				ArtistID: "a1",
			},
			{
				Event:    entity.Event{ID: "c3", LocalDate: date1, Venue: &entity.Venue{ID: "v3", AdminArea: strPtr("JP-27"), Coordinates: &entity.Coordinates{Latitude: osakaLat, Longitude: osakaLng}}},
				ArtistID: "a1",
			},
			// Date 2: No venue coordinates (AWAY)
			{
				Event:    entity.Event{ID: "c4", LocalDate: date2, Venue: &entity.Venue{ID: "v4", AdminArea: strPtr("JP-40")}},
				ArtistID: "a2",
			},
		}
		d.concertRepo.EXPECT().ListByFollower(ctx, "user-1").Return(concerts, nil).Once()

		groups, err := d.uc.ListByFollowerGrouped(ctx, "user-1", home)
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
		d := newConcertTestDeps(t)

		concerts := []*entity.Concert{
			{
				Event:    entity.Event{ID: "c1", LocalDate: date1, Venue: &entity.Venue{ID: "v1", AdminArea: strPtr("JP-13"), Coordinates: &entity.Coordinates{Latitude: tokyoLat, Longitude: tokyoLng}}},
				ArtistID: "a1",
			},
		}
		d.concertRepo.EXPECT().ListByFollower(ctx, "user-2").Return(concerts, nil).Once()

		groups, err := d.uc.ListByFollowerGrouped(ctx, "user-2", nil)
		assert.NoError(t, err)
		assert.Len(t, groups, 1)
		assert.Len(t, groups[0].Home, 0)
		assert.Len(t, groups[0].Nearby, 0)
		assert.Len(t, groups[0].Away, 1)
	})

	t.Run("empty concerts returns nil groups", func(t *testing.T) {
		t.Parallel()
		d := newConcertTestDeps(t)

		home := &entity.Home{Level1: "JP-13"}
		d.concertRepo.EXPECT().ListByFollower(ctx, "user-3").Return(nil, nil).Once()

		groups, err := d.uc.ListByFollowerGrouped(ctx, "user-3", home)
		assert.NoError(t, err)
		assert.Nil(t, groups)
	})
}

func TestConcertUseCase_SearchNewConcerts(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	type args struct {
		artistID string
	}

	tests := []struct {
		name    string
		args    args
		setup   func(t *testing.T, d *concertTestDeps)
		wantErr error
	}{
		{
			name: "cache hit - recently completed returns nil",
			args: args{artistID: "artist-1"},
			setup: func(t *testing.T, d *concertTestDeps) {
				t.Helper()
				recentLog := &entity.SearchLog{
					ArtistID:   "artist-1",
					SearchTime: time.Now().Add(-1 * time.Hour),
					Status:     entity.SearchLogStatusCompleted,
				}
				d.searchLogRepo.EXPECT().GetByArtistID(ctx, "artist-1").Return(recentLog, nil).Once()
			},
			wantErr: nil,
		},
		{
			name: "skip - already pending within timeout",
			args: args{artistID: "artist-1"},
			setup: func(t *testing.T, d *concertTestDeps) {
				t.Helper()
				pendingLog := &entity.SearchLog{
					ArtistID:   "artist-1",
					SearchTime: time.Now().Add(-1 * time.Minute),
					Status:     entity.SearchLogStatusPending,
				}
				d.searchLogRepo.EXPECT().GetByArtistID(ctx, "artist-1").Return(pendingLog, nil).Once()
			},
			wantErr: nil,
		},
		{
			name: "cache miss - discovers concerts and publishes event",
			args: args{artistID: "artist-1"},
			setup: func(t *testing.T, d *concertTestDeps) {
				t.Helper()
				artistID := "artist-1"
				artist := &entity.Artist{ID: artistID, Name: "Test Artist"}
				site := &entity.OfficialSite{ArtistID: artistID, URL: "https://example.com"}
				scraped := []*entity.ScrapedConcert{
					{Title: "New Concert", ListedVenueName: "Test Venue", LocalDate: time.Now().Add(24 * time.Hour), SourceURL: "https://example.com/concert"},
				}

				d.searchLogRepo.EXPECT().GetByArtistID(ctx, artistID).Return(nil, apperr.ErrNotFound).Once()
				d.searchLogRepo.EXPECT().Upsert(ctx, artistID, entity.SearchLogStatusPending).Return(nil).Once()
				d.artistRepo.EXPECT().Get(ctx, artistID).Return(artist, nil).Once()
				d.artistRepo.EXPECT().GetOfficialSite(ctx, artistID).Return(site, nil).Once()
				d.concertRepo.EXPECT().ListByArtist(ctx, artistID, true).Return(nil, nil).Once()
				d.searcher.EXPECT().Search(mock.Anything, artist, site, mock.AnythingOfType("time.Time")).Return(scraped, nil).Once()
				d.searchLogRepo.EXPECT().UpdateStatus(mock.Anything, artistID, entity.SearchLogStatusCompleted).Return(nil).Once()
			},
			wantErr: nil,
		},
		{
			name: "cache miss - log expired, calls Gemini, no new concerts after dedup",
			args: args{artistID: "artist-1"},
			setup: func(t *testing.T, d *concertTestDeps) {
				t.Helper()
				artistID := "artist-1"
				artist := &entity.Artist{ID: artistID, Name: "Test Artist"}
				site := &entity.OfficialSite{ArtistID: artistID, URL: "https://example.com"}
				expiredLog := &entity.SearchLog{
					ArtistID:   artistID,
					SearchTime: time.Now().Add(-25 * time.Hour),
					Status:     entity.SearchLogStatusCompleted,
				}

				d.searchLogRepo.EXPECT().GetByArtistID(ctx, artistID).Return(expiredLog, nil).Once()
				d.searchLogRepo.EXPECT().Upsert(ctx, artistID, entity.SearchLogStatusPending).Return(nil).Once()
				d.artistRepo.EXPECT().Get(ctx, artistID).Return(artist, nil).Once()
				d.artistRepo.EXPECT().GetOfficialSite(ctx, artistID).Return(site, nil).Once()
				d.concertRepo.EXPECT().ListByArtist(ctx, artistID, true).Return(nil, nil).Once()
				d.searcher.EXPECT().Search(mock.Anything, artist, site, mock.AnythingOfType("time.Time")).Return(nil, nil).Once()
				d.searchLogRepo.EXPECT().UpdateStatus(mock.Anything, artistID, entity.SearchLogStatusCompleted).Return(nil).Once()
			},
			wantErr: nil,
		},
		{
			name: "failure - Gemini search fails, marks search as failed",
			args: args{artistID: "artist-1"},
			setup: func(t *testing.T, d *concertTestDeps) {
				t.Helper()
				artistID := "artist-1"

				d.searchLogRepo.EXPECT().GetByArtistID(ctx, artistID).Return(nil, apperr.ErrNotFound).Once()
				d.searchLogRepo.EXPECT().Upsert(ctx, artistID, entity.SearchLogStatusPending).Return(nil).Once()
				d.artistRepo.EXPECT().Get(ctx, artistID).Return(&entity.Artist{ID: artistID}, nil).Once()
				d.artistRepo.EXPECT().GetOfficialSite(ctx, artistID).Return(&entity.OfficialSite{}, nil).Once()
				d.concertRepo.EXPECT().ListByArtist(ctx, artistID, true).Return(nil, nil).Once()
				d.searcher.EXPECT().Search(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, apperr.ErrInternal).Once()
				d.searchLogRepo.EXPECT().UpdateStatus(mock.Anything, artistID, entity.SearchLogStatusFailed).Return(nil).Once()
			},
			wantErr: apperr.ErrInternal,
		},
		{
			name: "success - no official site record, search continues with nil site",
			args: args{artistID: "artist-1"},
			setup: func(t *testing.T, d *concertTestDeps) {
				t.Helper()
				artistID := "artist-1"
				artist := &entity.Artist{ID: artistID, Name: "Test Artist"}
				scraped := []*entity.ScrapedConcert{
					{Title: "No-Site Concert", ListedVenueName: "Test Venue", LocalDate: time.Now().Add(24 * time.Hour), SourceURL: "https://example.com/concert"},
				}

				d.searchLogRepo.EXPECT().GetByArtistID(ctx, artistID).Return(nil, apperr.ErrNotFound).Once()
				d.searchLogRepo.EXPECT().Upsert(ctx, artistID, entity.SearchLogStatusPending).Return(nil).Once()
				d.artistRepo.EXPECT().Get(ctx, artistID).Return(artist, nil).Once()
				d.artistRepo.EXPECT().GetOfficialSite(ctx, artistID).Return(nil, apperr.ErrNotFound).Once()
				d.concertRepo.EXPECT().ListByArtist(ctx, artistID, true).Return(nil, nil).Once()
				d.searcher.EXPECT().Search(mock.Anything, artist, (*entity.OfficialSite)(nil), mock.AnythingOfType("time.Time")).Return(scraped, nil).Once()
				d.searchLogRepo.EXPECT().UpdateStatus(mock.Anything, artistID, entity.SearchLogStatusCompleted).Return(nil).Once()
			},
			wantErr: nil,
		},
		{
			name: "success - deduplicates against existing concerts (date-only key)",
			args: args{artistID: "artist-1"},
			setup: func(t *testing.T, d *concertTestDeps) {
				t.Helper()
				artistID := "artist-1"
				artist := &entity.Artist{ID: artistID, Name: "Test Artist"}
				concertDate := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
				existing := []*entity.Concert{
					{Event: entity.Event{ID: "c1", LocalDate: concertDate}, ArtistID: artistID},
				}
				scraped := []*entity.ScrapedConcert{
					{Title: "Existing Concert", ListedVenueName: "V1", LocalDate: concertDate},
				}

				d.searchLogRepo.EXPECT().GetByArtistID(ctx, artistID).Return(nil, apperr.ErrNotFound).Once()
				d.searchLogRepo.EXPECT().Upsert(ctx, artistID, entity.SearchLogStatusPending).Return(nil).Once()
				d.artistRepo.EXPECT().Get(ctx, artistID).Return(artist, nil).Once()
				d.artistRepo.EXPECT().GetOfficialSite(ctx, artistID).Return(nil, apperr.ErrNotFound).Once()
				d.concertRepo.EXPECT().ListByArtist(ctx, artistID, true).Return(existing, nil).Once()
				d.searcher.EXPECT().Search(mock.Anything, artist, (*entity.OfficialSite)(nil), mock.AnythingOfType("time.Time")).Return(scraped, nil).Once()
				d.searchLogRepo.EXPECT().UpdateStatus(mock.Anything, artistID, entity.SearchLogStatusCompleted).Return(nil).Once()
			},
			wantErr: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d := newConcertTestDeps(t)
			if tt.setup != nil {
				tt.setup(t, d)
			}

			// Subscribe to verify event publishing for non-error cases
			var msgs <-chan *entity.ConcertDiscoveredData
			_ = msgs // event verification is optional; main assertion is on error

			_, err := d.uc.SearchNewConcerts(ctx, tt.args.artistID)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}

			assert.NoError(t, err)
		})
	}
}

// TestSearchNewConcerts_TimingBoundaries verifies the cache TTL and pending timeout
// boundaries using deterministic fake-clock time via testing/synctest. Each sub-test
// runs inside a synctest.Test bubble so that time.Now() in production code uses virtual
// time that advances only when explicitly slept.
func TestSearchNewConcerts_TimingBoundaries(t *testing.T) {
	t.Parallel()

	artistID := "artist-1"
	artist := &entity.Artist{ID: artistID, Name: "Test Artist"}
	scraped := []*entity.ScrapedConcert{
		{Title: "New Concert", ListedVenueName: "Test Venue", LocalDate: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC), SourceURL: "https://example.com"},
	}

	t.Run("recently completed search is skipped (age < searchCacheTTL)", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			ctx := context.Background()
			d := newConcertTestDeps(t)

			// SearchTime is "now" at bubble start; 1 hour later is still fresh (TTL = 24h).
			searchedAt := time.Now()
			d.searchLogRepo.EXPECT().GetByArtistID(ctx, artistID).Return(&entity.SearchLog{
				ArtistID:   artistID,
				SearchTime: searchedAt,
				Status:     entity.SearchLogStatusCompleted,
			}, nil).Once()

			time.Sleep(1 * time.Hour) // advance fake clock — still within 24h TTL

			got, err := d.uc.SearchNewConcerts(ctx, artistID)
			assert.NoError(t, err)
			assert.Nil(t, got)
		})
	})

	t.Run("stale completed search triggers re-search (age > searchCacheTTL)", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			ctx := context.Background()
			d := newConcertTestDeps(t)

			// SearchTime is "now" at bubble start; advance 25h to exceed 24h TTL.
			searchedAt := time.Now()
			d.searchLogRepo.EXPECT().GetByArtistID(ctx, artistID).Return(&entity.SearchLog{
				ArtistID:   artistID,
				SearchTime: searchedAt,
				Status:     entity.SearchLogStatusCompleted,
			}, nil).Once()

			time.Sleep(25 * time.Hour) // advance fake clock past TTL

			d.searchLogRepo.EXPECT().Upsert(ctx, artistID, entity.SearchLogStatusPending).Return(nil).Once()
			d.artistRepo.EXPECT().Get(ctx, artistID).Return(artist, nil).Once()
			d.artistRepo.EXPECT().GetOfficialSite(ctx, artistID).Return(nil, apperr.ErrNotFound).Once()
			d.concertRepo.EXPECT().ListByArtist(ctx, artistID, true).Return(nil, nil).Once()
			d.searcher.EXPECT().Search(mock.Anything, artist, (*entity.OfficialSite)(nil), mock.AnythingOfType("time.Time")).Return(scraped, nil).Once()
			d.searchLogRepo.EXPECT().UpdateStatus(mock.Anything, artistID, entity.SearchLogStatusCompleted).Return(nil).Once()

			sub, err := d.publisher.Subscribe(ctx, entity.SubjectConcertDiscovered)
			assert.NoError(t, err)

			_, err = d.uc.SearchNewConcerts(ctx, artistID)
			assert.NoError(t, err)

			got := receivePublishedConcerts(t, ctx, sub)
			assert.Equal(t, 1, got, "stale cache should trigger re-search and publish")
		})
	})

	t.Run("pending search within timeout is skipped (age < pendingTimeout)", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			ctx := context.Background()
			d := newConcertTestDeps(t)

			// SearchTime is "now"; advance 1 minute — still within 3-minute pendingTimeout.
			searchedAt := time.Now()
			d.searchLogRepo.EXPECT().GetByArtistID(ctx, artistID).Return(&entity.SearchLog{
				ArtistID:   artistID,
				SearchTime: searchedAt,
				Status:     entity.SearchLogStatusPending,
			}, nil).Once()

			time.Sleep(1 * time.Minute)

			got, err := d.uc.SearchNewConcerts(ctx, artistID)
			assert.NoError(t, err)
			assert.Nil(t, got)
		})
	})

	t.Run("stale pending search is retried (age > pendingTimeout)", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			ctx := context.Background()
			d := newConcertTestDeps(t)

			// SearchTime is "now"; advance 4 minutes to exceed 3-minute pendingTimeout.
			searchedAt := time.Now()
			d.searchLogRepo.EXPECT().GetByArtistID(ctx, artistID).Return(&entity.SearchLog{
				ArtistID:   artistID,
				SearchTime: searchedAt,
				Status:     entity.SearchLogStatusPending,
			}, nil).Once()

			time.Sleep(4 * time.Minute) // advance fake clock past pendingTimeout

			d.searchLogRepo.EXPECT().Upsert(ctx, artistID, entity.SearchLogStatusPending).Return(nil).Once()
			d.artistRepo.EXPECT().Get(ctx, artistID).Return(artist, nil).Once()
			d.artistRepo.EXPECT().GetOfficialSite(ctx, artistID).Return(nil, apperr.ErrNotFound).Once()
			d.concertRepo.EXPECT().ListByArtist(ctx, artistID, true).Return(nil, nil).Once()
			d.searcher.EXPECT().Search(mock.Anything, artist, (*entity.OfficialSite)(nil), mock.AnythingOfType("time.Time")).Return(scraped, nil).Once()
			d.searchLogRepo.EXPECT().UpdateStatus(mock.Anything, artistID, entity.SearchLogStatusCompleted).Return(nil).Once()

			sub, err := d.publisher.Subscribe(ctx, entity.SubjectConcertDiscovered)
			assert.NoError(t, err)

			_, err = d.uc.SearchNewConcerts(ctx, artistID)
			assert.NoError(t, err)

			got := receivePublishedConcerts(t, ctx, sub)
			assert.Equal(t, 1, got, "stale pending should trigger re-search and publish")
		})
	})
}

// strPtr returns a pointer to the given string. Test helper.
func strPtr(s string) *string { return &s }

// receivePublishedConcerts reads from a concert.discovered subscription and
// returns the number of new concerts in the published event, or 0 if nothing
// was published within the timeout. Must be called inside a synctest.Test
// bubble so that time.After uses virtual time and resolves instantly.
func receivePublishedConcerts(t *testing.T, ctx context.Context, sub <-chan *message.Message) int {
	t.Helper()
	select {
	case msg := <-sub:
		msg.Ack()
		var data entity.ConcertDiscoveredData
		err := messaging.ParseCloudEventData(msg, &data)
		assert.NoError(t, err)
		return len(data.Concerts)
	case <-time.After(200 * time.Millisecond):
		return 0
	}
}

// TestSearchNewConcerts_Deduplication verifies that executeSearch correctly
// deduplicates scraped concerts against existing DB records. The dedup key
// is date-only (local_event_date) — an artist cannot perform at two venues
// simultaneously on the same day.
func TestSearchNewConcerts_Deduplication(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	concertDate := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

	startUTC := time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC)

	type testCase struct {
		name            string
		existing        []*entity.Concert
		scraped         []*entity.ScrapedConcert
		wantNewConcerts int // 0 = all deduped (no publish), >0 = event published with N concerts
	}

	tests := []testCase{
		// ── Cases where dedup SHOULD filter (wantNewConcerts = 0) ──

		{
			name: "same date, same start_at — deduped",
			existing: []*entity.Concert{
				{Event: entity.Event{ID: "c1", LocalDate: concertDate, StartTime: &startUTC, ListedVenueName: strPtr("Zepp Tokyo")}, ArtistID: "artist-1"},
			},
			scraped: []*entity.ScrapedConcert{
				{Title: "Concert A", ListedVenueName: "Zepp Tokyo", LocalDate: concertDate, StartTime: &startUTC, SourceURL: "https://example.com"},
			},
			wantNewConcerts: 0,
		},
		{
			name: "same date, scraped nil start_at — deduped",
			existing: []*entity.Concert{
				{Event: entity.Event{ID: "c1", LocalDate: concertDate, StartTime: &startUTC, ListedVenueName: strPtr("Zepp Tokyo")}, ArtistID: "artist-1"},
			},
			scraped: []*entity.ScrapedConcert{
				{Title: "Concert A", ListedVenueName: "Zepp Tokyo", LocalDate: concertDate, StartTime: nil, SourceURL: "https://example.com"},
			},
			wantNewConcerts: 0,
		},
		{
			name: "same date, existing nil start_at, scraped has start_at — deduped",
			existing: []*entity.Concert{
				{Event: entity.Event{ID: "c1", LocalDate: concertDate, StartTime: nil, ListedVenueName: strPtr("Zepp Tokyo")}, ArtistID: "artist-1"},
			},
			scraped: []*entity.ScrapedConcert{
				{Title: "Concert A", ListedVenueName: "Zepp Tokyo", LocalDate: concertDate, StartTime: &startUTC, SourceURL: "https://example.com"},
			},
			wantNewConcerts: 0,
		},
		{
			name: "both nil start_at, same date — deduped",
			existing: []*entity.Concert{
				{Event: entity.Event{ID: "c1", LocalDate: concertDate, StartTime: nil, ListedVenueName: strPtr("Zepp Tokyo")}, ArtistID: "artist-1"},
			},
			scraped: []*entity.ScrapedConcert{
				{Title: "Concert A", ListedVenueName: "Zepp Tokyo", LocalDate: concertDate, StartTime: nil, SourceURL: "https://example.com"},
			},
			wantNewConcerts: 0,
		},
		{
			name:     "within-batch dedup: two scraped concerts with same date",
			existing: nil,
			scraped: []*entity.ScrapedConcert{
				{Title: "Concert A", ListedVenueName: "Zepp Tokyo", LocalDate: concertDate, StartTime: &startUTC, SourceURL: "https://example.com/a"},
				{Title: "Concert A (dup)", ListedVenueName: "Zepp Tokyo", LocalDate: concertDate, StartTime: nil, SourceURL: "https://example.com/b"},
			},
			wantNewConcerts: 1, // second is intra-batch duplicate
		},
		{
			name: "same date, different venue — deduped (venue not in key)",
			existing: []*entity.Concert{
				{Event: entity.Event{ID: "c1", LocalDate: concertDate, StartTime: nil, ListedVenueName: strPtr("Zepp Tokyo")}, ArtistID: "artist-1"},
			},
			scraped: []*entity.ScrapedConcert{
				{Title: "Festival B", ListedVenueName: "Tokyo Dome", LocalDate: concertDate, StartTime: nil, SourceURL: "https://example.com"},
			},
			wantNewConcerts: 0,
		},

		// ── Cases where dedup should NOT filter (wantNewConcerts > 0) ──

		{
			name: "different date — distinct concerts",
			existing: []*entity.Concert{
				{Event: entity.Event{ID: "c1", LocalDate: concertDate, StartTime: &startUTC, ListedVenueName: strPtr("Zepp Tokyo")}, ArtistID: "artist-1"},
			},
			scraped: []*entity.ScrapedConcert{
				{Title: "Concert Day 2", ListedVenueName: "Zepp Tokyo", LocalDate: concertDate.AddDate(0, 0, 1), StartTime: &startUTC, SourceURL: "https://example.com"},
			},
			wantNewConcerts: 1,
		},
		{
			name: "mixed batch: one matches existing date, one is genuinely new date",
			existing: []*entity.Concert{
				{Event: entity.Event{ID: "c1", LocalDate: concertDate, StartTime: &startUTC, ListedVenueName: strPtr("Zepp Tokyo")}, ArtistID: "artist-1"},
			},
			scraped: []*entity.ScrapedConcert{
				{Title: "Existing Concert", ListedVenueName: "Zepp Tokyo", LocalDate: concertDate, StartTime: nil, SourceURL: "https://example.com/old"},
				{Title: "New Concert", ListedVenueName: "Tokyo Dome", LocalDate: concertDate.AddDate(0, 0, 7), StartTime: nil, SourceURL: "https://example.com/new"},
			},
			wantNewConcerts: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				d := newConcertTestDeps(t)
				artistID := "artist-1"
				artist := &entity.Artist{ID: artistID, Name: "Test Artist"}

				// Subscribe BEFORE calling SearchNewConcerts so the GoChannel buffers the message.
				sub, err := d.publisher.Subscribe(ctx, entity.SubjectConcertDiscovered)
				assert.NoError(t, err)

				// Common mock setup: no cache, artist found, no official site.
				d.searchLogRepo.EXPECT().GetByArtistID(ctx, artistID).Return(nil, apperr.ErrNotFound).Once()
				d.searchLogRepo.EXPECT().Upsert(ctx, artistID, entity.SearchLogStatusPending).Return(nil).Once()
				d.artistRepo.EXPECT().Get(ctx, artistID).Return(artist, nil).Once()
				d.artistRepo.EXPECT().GetOfficialSite(ctx, artistID).Return(nil, apperr.ErrNotFound).Once()
				d.concertRepo.EXPECT().ListByArtist(ctx, artistID, true).Return(tt.existing, nil).Once()
				d.searcher.EXPECT().Search(mock.Anything, artist, (*entity.OfficialSite)(nil), mock.AnythingOfType("time.Time")).Return(tt.scraped, nil).Once()
				d.searchLogRepo.EXPECT().UpdateStatus(mock.Anything, artistID, entity.SearchLogStatusCompleted).Return(nil).Once()

				_, err = d.uc.SearchNewConcerts(ctx, artistID)
				assert.NoError(t, err)

				got := receivePublishedConcerts(t, ctx, sub)
				assert.Equal(t, tt.wantNewConcerts, got,
					"expected %d new concerts after dedup, got %d", tt.wantNewConcerts, got)
			})
		})
	}
}

func TestConcertUseCase_ListWithProximity(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	date1 := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

	t.Run("returns grouped concerts by proximity", func(t *testing.T) {
		t.Parallel()
		d := newConcertTestDeps(t)

		home := &entity.Home{
			CountryCode: "JP",
			Level1:      "JP-40",
			Centroid:    &entity.Coordinates{Latitude: 33.5904, Longitude: 130.4017},
		}

		concerts := []*entity.Concert{
			{
				Event: entity.Event{
					ID: "c1", Title: "Fukuoka Concert", LocalDate: date1,
					Venue: &entity.Venue{AdminArea: strPtr("JP-40")},
				},
				ArtistID: "a1",
			},
			{
				Event: entity.Event{
					ID: "c2", Title: "Tokyo Concert", LocalDate: date1,
					Venue: &entity.Venue{
						Coordinates: &entity.Coordinates{Latitude: 35.6894, Longitude: 139.6917},
					},
				},
				ArtistID: "a2",
			},
		}

		d.concertRepo.EXPECT().ListByArtists(ctx, []string{"a1", "a2"}).Return(concerts, nil).Once()

		groups, err := d.uc.ListWithProximity(ctx, []string{"a1", "a2"}, home)
		assert.NoError(t, err)
		assert.Len(t, groups, 1, "same date should produce 1 group")
		assert.Len(t, groups[0].Home, 1, "JP-40 venue should be HOME")
		assert.Len(t, groups[0].Away, 1, "Tokyo venue should be AWAY from Fukuoka")
	})

	t.Run("nil home classifies all as away", func(t *testing.T) {
		t.Parallel()
		d := newConcertTestDeps(t)

		concerts := []*entity.Concert{
			{
				Event:    entity.Event{ID: "c1", Title: "Concert", LocalDate: date1, Venue: &entity.Venue{}},
				ArtistID: "a1",
			},
		}
		d.concertRepo.EXPECT().ListByArtists(ctx, []string{"a1"}).Return(concerts, nil).Once()

		groups, err := d.uc.ListWithProximity(ctx, []string{"a1"}, nil)
		assert.NoError(t, err)
		assert.Len(t, groups, 1)
		assert.Len(t, groups[0].Away, 1)
		assert.Empty(t, groups[0].Home)
		assert.Empty(t, groups[0].Nearby)
	})
}
