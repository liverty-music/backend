package usecase_test

import (
	"context"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// concertTestDeps holds all dependencies for ConcertUseCase tests.
type concertTestDeps struct {
	artistRepo    *mocks.MockArtistRepository
	concertRepo   *mocks.MockConcertRepository
	venueRepo     *mocks.MockVenueRepository
	searchLogRepo *mocks.MockSearchLogRepository
	searcher      *mocks.MockConcertSearcher
	uc            usecase.ConcertUseCase
}

func newConcertTestDeps(t *testing.T) *concertTestDeps {
	t.Helper()
	logger, _ := logging.New()
	d := &concertTestDeps{
		artistRepo:    mocks.NewMockArtistRepository(t),
		concertRepo:   mocks.NewMockConcertRepository(t),
		venueRepo:     mocks.NewMockVenueRepository(t),
		searchLogRepo: mocks.NewMockSearchLogRepository(t),
		searcher:      mocks.NewMockConcertSearcher(t),
	}
	d.uc = usecase.NewConcertUseCase(d.artistRepo, d.concertRepo, d.venueRepo, d.searchLogRepo, d.searcher, logger)
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
		name     string
		args     args
		setup    func(t *testing.T, d *concertTestDeps)
		want     int
		wantErr  error
		validate func(t *testing.T, result []*entity.Concert)
	}{
		{
			name:    "empty artist ID",
			args:    args{artistID: ""},
			wantErr: apperr.ErrInvalidArgument,
		},
		{
			name: "cache hit - recently searched returns empty",
			args: args{artistID: "artist-1"},
			setup: func(t *testing.T, d *concertTestDeps) {
				t.Helper()
				recentLog := &entity.SearchLog{
					ArtistID:   "artist-1",
					SearchTime: time.Now().Add(-1 * time.Hour),
				}
				d.searchLogRepo.EXPECT().GetByArtistID(ctx, "artist-1").Return(recentLog, nil).Once()
			},
			want:    0,
			wantErr: nil,
		},
		{
			name: "cache miss - no log exists, calls Gemini and upserts log",
			args: args{artistID: "artist-1"},
			setup: func(t *testing.T, d *concertTestDeps) {
				t.Helper()
				artistID := "artist-1"
				artist := &entity.Artist{ID: artistID, Name: "Test Artist"}
				site := &entity.OfficialSite{ArtistID: artistID, URL: "https://example.com"}
				scraped := []*entity.ScrapedConcert{
					{Title: "New Concert", VenueName: "Test Venue", LocalEventDate: time.Now().Add(24 * time.Hour), SourceURL: "https://example.com/concert"},
				}

				d.searchLogRepo.EXPECT().GetByArtistID(ctx, artistID).Return(nil, apperr.ErrNotFound).Once()
				d.artistRepo.EXPECT().Get(ctx, artistID).Return(artist, nil).Once()
				d.artistRepo.EXPECT().GetOfficialSite(ctx, artistID).Return(site, nil).Once()
				d.concertRepo.EXPECT().ListByArtist(ctx, artistID, true).Return(nil, nil).Once()
				d.searchLogRepo.EXPECT().Upsert(ctx, artistID).Return(nil).Once() // Before Gemini call
				d.searcher.EXPECT().Search(ctx, artist, site, mock.AnythingOfType("time.Time")).Return(scraped, nil).Once()
				d.venueRepo.EXPECT().GetByName(ctx, "Test Venue").Return(&entity.Venue{ID: "v1", Name: "Test Venue"}, nil).Once()
				d.concertRepo.EXPECT().Create(ctx, mock.MatchedBy(func(c *entity.Concert) bool {
					return c.ArtistID == artistID && c.Title == "New Concert"
				})).Return(nil).Once() // variadic: single concert bulk insert
			},
			want:    1,
			wantErr: nil,
			validate: func(t *testing.T, result []*entity.Concert) {
				t.Helper()
				assert.Equal(t, "New Concert", result[0].Title)
			},
		},
		{
			name: "cache miss - log expired (>24h), calls Gemini",
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
			want:    0,
			wantErr: nil,
		},
		{
			name: "success - new venue created",
			args: args{artistID: "artist-1"},
			setup: func(t *testing.T, d *concertTestDeps) {
				t.Helper()
				artistID := "artist-1"
				artist := &entity.Artist{ID: artistID, Name: "Test Artist"}
				site := &entity.OfficialSite{ArtistID: artistID, URL: "https://example.com"}
				scraped := []*entity.ScrapedConcert{
					{Title: "New Concert", VenueName: "New Venue", LocalEventDate: time.Now().Add(24 * time.Hour), SourceURL: "https://example.com/concert"},
				}

				d.searchLogRepo.EXPECT().GetByArtistID(ctx, artistID).Return(nil, apperr.ErrNotFound).Once()
				d.artistRepo.EXPECT().Get(ctx, artistID).Return(artist, nil).Once()
				d.artistRepo.EXPECT().GetOfficialSite(ctx, artistID).Return(site, nil).Once()
				d.concertRepo.EXPECT().ListByArtist(ctx, artistID, true).Return(nil, nil).Once()
				d.searcher.EXPECT().Search(ctx, artist, site, mock.AnythingOfType("time.Time")).Return(scraped, nil).Once()
				d.venueRepo.EXPECT().GetByName(ctx, "New Venue").Return(nil, apperr.ErrNotFound).Once()
				d.venueRepo.EXPECT().Create(ctx, mock.MatchedBy(func(v *entity.Venue) bool {
					return v.Name == "New Venue" && v.ID != ""
				})).Return(nil).Once()
				d.concertRepo.EXPECT().Create(ctx, mock.MatchedBy(func(c *entity.Concert) bool {
					return c.ArtistID == artistID && c.Title == "New Concert" && c.ID != ""
				})).Return(nil).Once() // variadic: single concert bulk insert
				d.searchLogRepo.EXPECT().Upsert(ctx, artistID).Return(nil).Once()
			},
			want:    1,
			wantErr: nil,
			validate: func(t *testing.T, result []*entity.Concert) {
				t.Helper()
				assert.Equal(t, "New Concert", result[0].Title)
				assert.NotEmpty(t, result[0].ID)
				assert.NotEmpty(t, result[0].VenueID)
			},
		},
		{
			name: "success - venue already exists",
			args: args{artistID: "artist-1"},
			setup: func(t *testing.T, d *concertTestDeps) {
				t.Helper()
				artistID := "artist-1"
				artist := &entity.Artist{ID: artistID, Name: "Test Artist"}
				site := &entity.OfficialSite{ArtistID: artistID, URL: "https://example.com"}
				scraped := []*entity.ScrapedConcert{
					{Title: "New Concert", VenueName: "Existing Venue", LocalEventDate: time.Now().Add(24 * time.Hour), SourceURL: "https://example.com/concert"},
				}

				d.searchLogRepo.EXPECT().GetByArtistID(ctx, artistID).Return(nil, apperr.ErrNotFound).Once()
				d.artistRepo.EXPECT().Get(ctx, artistID).Return(artist, nil).Once()
				d.artistRepo.EXPECT().GetOfficialSite(ctx, artistID).Return(site, nil).Once()
				d.concertRepo.EXPECT().ListByArtist(ctx, artistID, true).Return(nil, nil).Once()
				d.searcher.EXPECT().Search(ctx, artist, site, mock.AnythingOfType("time.Time")).Return(scraped, nil).Once()
				d.venueRepo.EXPECT().GetByName(ctx, "Existing Venue").Return(&entity.Venue{ID: "v-existing", Name: "Existing Venue"}, nil).Once()
				d.concertRepo.EXPECT().Create(ctx, mock.MatchedBy(func(c *entity.Concert) bool {
					return c.VenueID == "v-existing"
				})).Return(nil).Once() // variadic: single concert bulk insert
				d.searchLogRepo.EXPECT().Upsert(ctx, artistID).Return(nil).Once()
			},
			want:    1,
			wantErr: nil,
			validate: func(t *testing.T, result []*entity.Concert) {
				t.Helper()
				assert.Equal(t, "v-existing", result[0].VenueID)
			},
		},
		{
			name: "success - venue creation race condition",
			args: args{artistID: "artist-1"},
			setup: func(t *testing.T, d *concertTestDeps) {
				t.Helper()
				artistID := "artist-1"
				artist := &entity.Artist{ID: artistID, Name: "Test Artist"}
				site := &entity.OfficialSite{ArtistID: artistID, URL: "https://example.com"}
				scraped := []*entity.ScrapedConcert{
					{Title: "New Concert", VenueName: "Race Venue", LocalEventDate: time.Now().Add(24 * time.Hour), SourceURL: "https://example.com/concert"},
				}

				d.searchLogRepo.EXPECT().GetByArtistID(ctx, artistID).Return(nil, apperr.ErrNotFound).Once()
				d.artistRepo.EXPECT().Get(ctx, artistID).Return(artist, nil).Once()
				d.artistRepo.EXPECT().GetOfficialSite(ctx, artistID).Return(site, nil).Once()
				d.concertRepo.EXPECT().ListByArtist(ctx, artistID, true).Return(nil, nil).Once()
				d.searcher.EXPECT().Search(ctx, artist, site, mock.AnythingOfType("time.Time")).Return(scraped, nil).Once()
				d.venueRepo.EXPECT().GetByName(ctx, "Race Venue").Return(nil, apperr.ErrNotFound).Once()
				d.venueRepo.EXPECT().Create(ctx, mock.Anything).Return(apperr.New(codes.AlreadyExists, "already exists")).Once()
				d.venueRepo.EXPECT().GetByName(ctx, "Race Venue").Return(&entity.Venue{ID: "v-race", Name: "Race Venue"}, nil).Once()
				d.concertRepo.EXPECT().Create(ctx, mock.MatchedBy(func(c *entity.Concert) bool {
					return c.VenueID == "v-race"
				})).Return(nil).Once() // variadic: single concert bulk insert
				d.searchLogRepo.EXPECT().Upsert(ctx, artistID).Return(nil).Once()
			},
			want:    1,
			wantErr: nil,
			validate: func(t *testing.T, result []*entity.Concert) {
				t.Helper()
				assert.Equal(t, "v-race", result[0].VenueID)
			},
		},
		{
			name: "partial success - venue creation failure skips concert",
			args: args{artistID: "artist-1"},
			setup: func(t *testing.T, d *concertTestDeps) {
				t.Helper()
				artistID := "artist-1"
				scraped := []*entity.ScrapedConcert{
					{Title: "C1", VenueName: "V1", LocalEventDate: time.Now().Add(24 * time.Hour)},
				}

				d.searchLogRepo.EXPECT().GetByArtistID(ctx, artistID).Return(nil, apperr.ErrNotFound).Once()
				d.artistRepo.EXPECT().Get(ctx, artistID).Return(&entity.Artist{ID: artistID}, nil).Once()
				d.artistRepo.EXPECT().GetOfficialSite(ctx, artistID).Return(&entity.OfficialSite{}, nil).Once()
				d.concertRepo.EXPECT().ListByArtist(ctx, artistID, true).Return(nil, nil).Once()
				d.searchLogRepo.EXPECT().Upsert(ctx, artistID).Return(nil).Once()
				d.searcher.EXPECT().Search(ctx, mock.Anything, mock.Anything, mock.Anything).Return(scraped, nil).Once()
				d.venueRepo.EXPECT().GetByName(ctx, "V1").Return(nil, apperr.ErrNotFound).Once()
				d.venueRepo.EXPECT().Create(ctx, mock.Anything).Return(assert.AnError).Once()

			},
			want:    0,
			wantErr: nil,
		},
		{
			name: "failure - bulk concert creation failure returns error",
			args: args{artistID: "artist-1"},
			setup: func(t *testing.T, d *concertTestDeps) {
				t.Helper()
				artistID := "artist-1"
				scraped := []*entity.ScrapedConcert{
					{Title: "C1", VenueName: "V1", LocalEventDate: time.Now().Add(24 * time.Hour)},
				}

				d.searchLogRepo.EXPECT().GetByArtistID(ctx, artistID).Return(nil, apperr.ErrNotFound).Once()
				d.artistRepo.EXPECT().Get(ctx, artistID).Return(&entity.Artist{ID: artistID}, nil).Once()
				d.artistRepo.EXPECT().GetOfficialSite(ctx, artistID).Return(&entity.OfficialSite{}, nil).Once()
				d.concertRepo.EXPECT().ListByArtist(ctx, artistID, true).Return(nil, nil).Once()
				d.searchLogRepo.EXPECT().Upsert(ctx, artistID).Return(nil).Once()
				d.searcher.EXPECT().Search(ctx, mock.Anything, mock.Anything, mock.Anything).Return(scraped, nil).Once()
				d.venueRepo.EXPECT().GetByName(ctx, "V1").Return(&entity.Venue{ID: "v1"}, nil).Once()
				d.concertRepo.EXPECT().Create(ctx, mock.Anything).Return(assert.AnError).Once()
			},
			want:    0,
			wantErr: assert.AnError,
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
				d.searchLogRepo.EXPECT().Upsert(ctx, artistID).Return(nil).Once() // Before Gemini
				d.searcher.EXPECT().Search(ctx, mock.Anything, mock.Anything, mock.Anything).Return(nil, assert.AnError).Once()
				d.searchLogRepo.EXPECT().Delete(ctx, artistID).Return(nil).Once() // Clean up on failure
			},
			want:    0,
			wantErr: assert.AnError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := newConcertTestDeps(t)
			if tt.setup != nil {
				tt.setup(t, d)
			}

			got, err := d.uc.SearchNewConcerts(ctx, tt.args.artistID)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}

			assert.NoError(t, err)
			assert.Len(t, got, tt.want)
			if tt.validate != nil {
				tt.validate(t, got)
			}
		})
	}
}
