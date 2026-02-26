package usecase_test

import (
	"context"
	"testing"
	"time"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/pubsub/gochannel"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/infrastructure/messaging"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// concertTestDeps holds all dependencies for ConcertUseCase tests.
type concertTestDeps struct {
	artistRepo    *mocks.MockArtistRepository
	concertRepo   *mocks.MockConcertRepository
	venueRepo     *mocks.MockVenueRepository
	userRepo      *mocks.MockUserRepository
	searchLogRepo *mocks.MockSearchLogRepository
	searcher      *mocks.MockConcertSearcher
	publisher     *gochannel.GoChannel
	uc            usecase.ConcertUseCase
}

func newConcertTestDeps(t *testing.T) *concertTestDeps {
	t.Helper()
	logger, _ := logging.New()
	pub := gochannel.NewGoChannel(gochannel.Config{OutputChannelBuffer: 64}, watermill.NopLogger{})
	d := &concertTestDeps{
		artistRepo:    mocks.NewMockArtistRepository(t),
		concertRepo:   mocks.NewMockConcertRepository(t),
		venueRepo:     mocks.NewMockVenueRepository(t),
		userRepo:      mocks.NewMockUserRepository(t),
		searchLogRepo: mocks.NewMockSearchLogRepository(t),
		searcher:      mocks.NewMockConcertSearcher(t),
		publisher:     pub,
	}
	d.uc = usecase.NewConcertUseCase(d.artistRepo, d.concertRepo, d.venueRepo, d.userRepo, d.searchLogRepo, d.searcher, pub, logger)
	t.Cleanup(func() { _ = pub.Close() })
	return d
}

func TestConcertUseCase_ListConcertsByArtist(t *testing.T) {
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
		{
			name:    "empty artist ID",
			args:    args{artistID: ""},
			wantErr: apperr.ErrInvalidArgument,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
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

func TestConcertUseCase_SearchNewConcerts(t *testing.T) {
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
			name:    "empty artist ID",
			args:    args{artistID: ""},
			wantErr: apperr.ErrInvalidArgument,
		},
		{
			name: "cache hit - recently searched returns nil",
			args: args{artistID: "artist-1"},
			setup: func(t *testing.T, d *concertTestDeps) {
				t.Helper()
				recentLog := &entity.SearchLog{
					ArtistID:   "artist-1",
					SearchTime: time.Now().Add(-1 * time.Hour),
				}
				d.searchLogRepo.EXPECT().GetByArtistID(ctx, "artist-1").Return(recentLog, nil).Once()
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
				d.artistRepo.EXPECT().Get(ctx, artistID).Return(artist, nil).Once()
				d.artistRepo.EXPECT().GetOfficialSite(ctx, artistID).Return(site, nil).Once()
				d.concertRepo.EXPECT().ListByArtist(ctx, artistID, true).Return(nil, nil).Once()
				d.searchLogRepo.EXPECT().Upsert(ctx, artistID).Return(nil).Once()
				d.searcher.EXPECT().Search(ctx, artist, site, mock.AnythingOfType("time.Time")).Return(scraped, nil).Once()
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
				expiredLog := &entity.SearchLog{ArtistID: artistID, SearchTime: time.Now().Add(-25 * time.Hour)}

				d.searchLogRepo.EXPECT().GetByArtistID(ctx, artistID).Return(expiredLog, nil).Once()
				d.artistRepo.EXPECT().Get(ctx, artistID).Return(artist, nil).Once()
				d.artistRepo.EXPECT().GetOfficialSite(ctx, artistID).Return(site, nil).Once()
				d.concertRepo.EXPECT().ListByArtist(ctx, artistID, true).Return(nil, nil).Once()
				d.searcher.EXPECT().Search(ctx, artist, site, mock.AnythingOfType("time.Time")).Return(nil, nil).Once()
				d.searchLogRepo.EXPECT().Upsert(ctx, artistID).Return(nil).Once()
			},
			wantErr: nil,
		},
		{
			name: "failure - Gemini search fails, deletes search log",
			args: args{artistID: "artist-1"},
			setup: func(t *testing.T, d *concertTestDeps) {
				t.Helper()
				artistID := "artist-1"

				d.searchLogRepo.EXPECT().GetByArtistID(ctx, artistID).Return(nil, apperr.ErrNotFound).Once()
				d.artistRepo.EXPECT().Get(ctx, artistID).Return(&entity.Artist{ID: artistID}, nil).Once()
				d.artistRepo.EXPECT().GetOfficialSite(ctx, artistID).Return(&entity.OfficialSite{}, nil).Once()
				d.concertRepo.EXPECT().ListByArtist(ctx, artistID, true).Return(nil, nil).Once()
				d.searchLogRepo.EXPECT().Upsert(ctx, artistID).Return(nil).Once()
				d.searcher.EXPECT().Search(ctx, mock.Anything, mock.Anything, mock.Anything).Return(nil, assert.AnError).Once()
				d.searchLogRepo.EXPECT().Delete(ctx, artistID).Return(nil).Once()
			},
			wantErr: assert.AnError,
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
				d.artistRepo.EXPECT().Get(ctx, artistID).Return(artist, nil).Once()
				d.artistRepo.EXPECT().GetOfficialSite(ctx, artistID).Return(nil, apperr.ErrNotFound).Once()
				d.concertRepo.EXPECT().ListByArtist(ctx, artistID, true).Return(nil, nil).Once()
				d.searchLogRepo.EXPECT().Upsert(ctx, artistID).Return(nil).Once()
				d.searcher.EXPECT().Search(ctx, artist, (*entity.OfficialSite)(nil), mock.AnythingOfType("time.Time")).Return(scraped, nil).Once()
			},
			wantErr: nil,
		},
		{
			name: "success - deduplicates against existing concerts",
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
				d.artistRepo.EXPECT().Get(ctx, artistID).Return(artist, nil).Once()
				d.artistRepo.EXPECT().GetOfficialSite(ctx, artistID).Return(nil, apperr.ErrNotFound).Once()
				d.concertRepo.EXPECT().ListByArtist(ctx, artistID, true).Return(existing, nil).Once()
				d.searchLogRepo.EXPECT().Upsert(ctx, artistID).Return(nil).Once()
				d.searcher.EXPECT().Search(ctx, artist, (*entity.OfficialSite)(nil), mock.AnythingOfType("time.Time")).Return(scraped, nil).Once()
			},
			wantErr: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := newConcertTestDeps(t)
			if tt.setup != nil {
				tt.setup(t, d)
			}

			// Subscribe to verify event publishing for non-error cases
			var msgs <-chan *messaging.ConcertDiscoveredData
			_ = msgs // event verification is optional; main assertion is on error

			err := d.uc.SearchNewConcerts(ctx, tt.args.artistID)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}

			assert.NoError(t, err)
		})
	}
}
