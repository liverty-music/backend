package usecase_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const (
	refreshArtistID = "019a0000-0000-7000-8000-000000000001"
	refreshMBID     = "4a6b6e1c-1b4e-4e43-9e8b-2f6c3c1f0a01"
)

// officialSiteRefreshTestDeps holds all dependencies for OfficialSiteRefreshUseCase tests.
type officialSiteRefreshTestDeps struct {
	artistRepo   *mocks.MockArtistRepository
	followRepo   *mocks.MockFollowRepository
	siteResolver *mocks.MockOfficialSiteResolver
	uc           usecase.OfficialSiteRefreshUseCase
}

func newOfficialSiteRefreshTestDeps(t *testing.T) *officialSiteRefreshTestDeps {
	t.Helper()
	d := &officialSiteRefreshTestDeps{
		artistRepo:   mocks.NewMockArtistRepository(t),
		followRepo:   mocks.NewMockFollowRepository(t),
		siteResolver: mocks.NewMockOfficialSiteResolver(t),
	}
	d.uc = usecase.NewOfficialSiteRefreshUseCase(d.artistRepo, d.followRepo, d.siteResolver, newTestLogger(t))
	return d
}

// checkedNow matches a check time taken during the test, between start and now.
func checkedNow(start time.Time) any {
	return mock.MatchedBy(func(t time.Time) bool {
		return !t.Before(start) && !t.After(time.Now())
	})
}

func TestOfficialSiteRefreshUseCase_RefreshOfficialSite(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	// @spec components/usecase/artist/refresh-official-site "Catalog resolves to a different site"
	t.Run("Catalog resolves to a different site", func(t *testing.T) {
		t.Parallel()
		d := newOfficialSiteRefreshTestDeps(t)
		start := time.Now()

		d.siteResolver.EXPECT().ResolveOfficialSiteURL(ctx, refreshMBID).Return("https://www.hitsujibungaku.info/", nil).Once()
		d.artistRepo.EXPECT().GetOfficialSite(ctx, refreshArtistID).
			Return(&entity.OfficialSite{ID: "site-1", ArtistID: refreshArtistID, URL: "http://hitsujibungaku.jimdo.com/"}, nil).Once()
		d.artistRepo.EXPECT().UpdateOfficialSiteURL(ctx, refreshArtistID, "https://www.hitsujibungaku.info/").Return(nil).Once()
		d.artistRepo.EXPECT().MarkOfficialSiteChecked(ctx, refreshArtistID, checkedNow(start)).Return(nil).Once()

		err := d.uc.RefreshOfficialSite(ctx, refreshArtistID, refreshMBID)

		assert.NoError(t, err)
	})

	// @spec components/usecase/artist/refresh-official-site "Catalog resolves to the stored site"
	t.Run("Catalog resolves to the stored site", func(t *testing.T) {
		t.Parallel()
		d := newOfficialSiteRefreshTestDeps(t)
		start := time.Now()

		d.siteResolver.EXPECT().ResolveOfficialSiteURL(ctx, refreshMBID).Return("https://yorushika.com/", nil).Once()
		d.artistRepo.EXPECT().GetOfficialSite(ctx, refreshArtistID).
			Return(&entity.OfficialSite{ID: "site-1", ArtistID: refreshArtistID, URL: "https://yorushika.com/"}, nil).Once()
		d.artistRepo.EXPECT().MarkOfficialSiteChecked(ctx, refreshArtistID, checkedNow(start)).Return(nil).Once()

		err := d.uc.RefreshOfficialSite(ctx, refreshArtistID, refreshMBID)

		assert.NoError(t, err)
		d.artistRepo.AssertNotCalled(t, "UpdateOfficialSiteURL", mock.Anything, mock.Anything, mock.Anything)
		d.artistRepo.AssertNotCalled(t, "CreateOfficialSite", mock.Anything, mock.Anything)
	})

	// @spec components/usecase/artist/refresh-official-site "Artist without a site gets one"
	t.Run("Artist without a site gets one", func(t *testing.T) {
		t.Parallel()
		d := newOfficialSiteRefreshTestDeps(t)
		start := time.Now()

		d.siteResolver.EXPECT().ResolveOfficialSiteURL(ctx, refreshMBID).Return("https://illit-official.jp/", nil).Once()
		d.artistRepo.EXPECT().GetOfficialSite(ctx, refreshArtistID).
			Return(nil, apperr.New(codes.NotFound, "official site not found")).Once()
		d.artistRepo.EXPECT().CreateOfficialSite(ctx, mock.MatchedBy(func(s *entity.OfficialSite) bool {
			return s.ID != "" && s.ArtistID == refreshArtistID && s.URL == "https://illit-official.jp/"
		})).Return(nil).Once()
		d.artistRepo.EXPECT().MarkOfficialSiteChecked(ctx, refreshArtistID, checkedNow(start)).Return(nil).Once()

		err := d.uc.RefreshOfficialSite(ctx, refreshArtistID, refreshMBID)

		assert.NoError(t, err)
	})

	// @spec components/usecase/artist/refresh-official-site "Catalog lists no active homepage"
	t.Run("Catalog lists no active homepage", func(t *testing.T) {
		t.Parallel()
		d := newOfficialSiteRefreshTestDeps(t)
		start := time.Now()

		d.siteResolver.EXPECT().ResolveOfficialSiteURL(ctx, refreshMBID).Return("", nil).Once()
		d.artistRepo.EXPECT().MarkOfficialSiteChecked(ctx, refreshArtistID, checkedNow(start)).Return(nil).Once()

		err := d.uc.RefreshOfficialSite(ctx, refreshArtistID, refreshMBID)

		assert.NoError(t, err)
		d.artistRepo.AssertNotCalled(t, "UpdateOfficialSiteURL", mock.Anything, mock.Anything, mock.Anything)
		d.artistRepo.AssertNotCalled(t, "CreateOfficialSite", mock.Anything, mock.Anything)
	})

	// @spec components/usecase/artist/refresh-official-site "No site anywhere"
	t.Run("No site anywhere", func(t *testing.T) {
		t.Parallel()
		d := newOfficialSiteRefreshTestDeps(t)
		start := time.Now()

		d.siteResolver.EXPECT().ResolveOfficialSiteURL(ctx, refreshMBID).Return("", nil).Once()
		d.artistRepo.EXPECT().MarkOfficialSiteChecked(ctx, refreshArtistID, checkedNow(start)).Return(nil).Once()

		err := d.uc.RefreshOfficialSite(ctx, refreshArtistID, refreshMBID)

		assert.NoError(t, err)
		d.artistRepo.AssertNotCalled(t, "CreateOfficialSite", mock.Anything, mock.Anything)
	})

	// @spec components/usecase/artist/refresh-official-site "Catalog unreachable"
	t.Run("Catalog unreachable", func(t *testing.T) {
		t.Parallel()
		d := newOfficialSiteRefreshTestDeps(t)

		d.siteResolver.EXPECT().ResolveOfficialSiteURL(ctx, refreshMBID).
			Return("", apperr.New(codes.Unavailable, "musicbrainz url-rels request failed")).Once()

		err := d.uc.RefreshOfficialSite(ctx, refreshArtistID, refreshMBID)

		assert.ErrorIs(t, err, apperr.ErrUnavailable)
		d.artistRepo.AssertNotCalled(t, "UpdateOfficialSiteURL", mock.Anything, mock.Anything, mock.Anything)
		d.artistRepo.AssertNotCalled(t, "CreateOfficialSite", mock.Anything, mock.Anything)
		d.artistRepo.AssertNotCalled(t, "MarkOfficialSiteChecked", mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("a GetOfficialSite failure other than NotFound records no check", func(t *testing.T) {
		t.Parallel()
		d := newOfficialSiteRefreshTestDeps(t)

		d.siteResolver.EXPECT().ResolveOfficialSiteURL(ctx, refreshMBID).Return("https://example.com/", nil).Once()
		d.artistRepo.EXPECT().GetOfficialSite(ctx, refreshArtistID).
			Return(nil, apperr.New(codes.Internal, "db down")).Once()

		err := d.uc.RefreshOfficialSite(ctx, refreshArtistID, refreshMBID)

		assert.ErrorIs(t, err, apperr.ErrInternal)
		d.artistRepo.AssertNotCalled(t, "MarkOfficialSiteChecked", mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("an UpdateOfficialSiteURL failure records no check", func(t *testing.T) {
		t.Parallel()
		d := newOfficialSiteRefreshTestDeps(t)

		d.siteResolver.EXPECT().ResolveOfficialSiteURL(ctx, refreshMBID).Return("https://new.example.com/", nil).Once()
		d.artistRepo.EXPECT().GetOfficialSite(ctx, refreshArtistID).
			Return(&entity.OfficialSite{ID: "site-1", ArtistID: refreshArtistID, URL: "https://old.example.com/"}, nil).Once()
		d.artistRepo.EXPECT().UpdateOfficialSiteURL(ctx, refreshArtistID, "https://new.example.com/").
			Return(apperr.New(codes.InvalidArgument, "invalid official site url")).Once()

		err := d.uc.RefreshOfficialSite(ctx, refreshArtistID, refreshMBID)

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
		d.artistRepo.AssertNotCalled(t, "MarkOfficialSiteChecked", mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("a CreateOfficialSite failure records no check", func(t *testing.T) {
		t.Parallel()
		d := newOfficialSiteRefreshTestDeps(t)

		d.siteResolver.EXPECT().ResolveOfficialSiteURL(ctx, refreshMBID).Return("https://new.example.com/", nil).Once()
		d.artistRepo.EXPECT().GetOfficialSite(ctx, refreshArtistID).
			Return(nil, apperr.New(codes.NotFound, "official site not found")).Once()
		d.artistRepo.EXPECT().CreateOfficialSite(ctx, mock.Anything).
			Return(apperr.New(codes.AlreadyExists, "official site exists")).Once()

		err := d.uc.RefreshOfficialSite(ctx, refreshArtistID, refreshMBID)

		assert.ErrorIs(t, err, apperr.ErrAlreadyExists)
		d.artistRepo.AssertNotCalled(t, "MarkOfficialSiteChecked", mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("a MarkOfficialSiteChecked failure is returned", func(t *testing.T) {
		t.Parallel()
		d := newOfficialSiteRefreshTestDeps(t)

		d.siteResolver.EXPECT().ResolveOfficialSiteURL(ctx, refreshMBID).Return("", nil).Once()
		d.artistRepo.EXPECT().MarkOfficialSiteChecked(ctx, refreshArtistID, mock.Anything).
			Return(apperr.New(codes.NotFound, "artist not found")).Once()

		err := d.uc.RefreshOfficialSite(ctx, refreshArtistID, refreshMBID)

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})
}

func TestOfficialSiteRefreshBatchSize(t *testing.T) {
	t.Parallel()

	tests := []struct {
		followed int
		want     int
	}{
		{followed: 133, want: 19},
		{followed: 0, want: 0},
		{followed: 1, want: 1},
		{followed: 7, want: 1},
		{followed: 8, want: 2},
		{followed: 140, want: 20},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%d followed", tt.followed), func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, usecase.ExportedOfficialSiteRefreshBatchSize(tt.followed))
		})
	}
}

// followedArtists returns n distinct followed artists.
func followedArtists(n int) []*entity.Artist {
	artists := make([]*entity.Artist, n)
	for i := range n {
		artists[i] = &entity.Artist{
			ID:   fmt.Sprintf("artist-%03d", i),
			Name: fmt.Sprintf("Artist %03d", i),
			MBID: fmt.Sprintf("00000000-0000-0000-0000-000000000%03d", i),
		}
	}
	return artists
}

func TestOfficialSiteRefreshUseCase_RefreshDueOfficialSites(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	const age = 7 * 24 * time.Hour

	// @spec components/usecase/artist/refresh-official-site "Batch size"
	t.Run("Batch size", func(t *testing.T) {
		t.Parallel()
		d := newOfficialSiteRefreshTestDeps(t)
		// 40 artists are due; ListStaleOfficialSite returns the first 19 of
		// them because the run asks for ceil(133 / 7) = 19.
		due := followedArtists(19)

		d.followRepo.EXPECT().ListAll(ctx).Return(followedArtists(133), nil).Once()
		d.artistRepo.EXPECT().ListStaleOfficialSite(ctx, age, 19).Return(due, nil).Once()
		var refreshed []string
		d.siteResolver.EXPECT().ResolveOfficialSiteURL(ctx, mock.Anything).
			RunAndReturn(func(_ context.Context, mbid string) (string, error) {
				refreshed = append(refreshed, mbid)
				return "", nil
			}).Times(19)
		d.artistRepo.EXPECT().MarkOfficialSiteChecked(ctx, mock.Anything, mock.Anything).Return(nil).Times(19)

		got, err := d.uc.RefreshDueOfficialSites(ctx)

		require.NoError(t, err)
		assert.Equal(t, &usecase.OfficialSiteRefreshSummary{
			Followed: 133, BatchSize: 19, Due: 19, Attempted: 19, Failed: 0,
		}, got)
		wantOrder := make([]string, len(due))
		for i, a := range due {
			wantOrder[i] = a.MBID
		}
		assert.Equal(t, wantOrder, refreshed)
	})

	// @spec components/usecase/artist/refresh-official-site "Nobody followed"
	t.Run("Nobody followed", func(t *testing.T) {
		t.Parallel()
		d := newOfficialSiteRefreshTestDeps(t)

		d.followRepo.EXPECT().ListAll(ctx).Return([]*entity.Artist{}, nil).Once()

		got, err := d.uc.RefreshDueOfficialSites(ctx)

		require.NoError(t, err)
		assert.Equal(t, &usecase.OfficialSiteRefreshSummary{}, got)
		d.artistRepo.AssertNotCalled(t, "ListStaleOfficialSite", mock.Anything, mock.Anything, mock.Anything)
		d.siteResolver.AssertNotCalled(t, "ResolveOfficialSiteURL", mock.Anything, mock.Anything)
	})

	// @spec components/usecase/artist/refresh-official-site "Consecutive failures"
	t.Run("Consecutive failures", func(t *testing.T) {
		t.Parallel()
		d := newOfficialSiteRefreshTestDeps(t)
		due := followedArtists(5)

		d.followRepo.EXPECT().ListAll(ctx).Return(followedArtists(35), nil).Once()
		d.artistRepo.EXPECT().ListStaleOfficialSite(ctx, age, 5).Return(due, nil).Once()
		d.siteResolver.EXPECT().ResolveOfficialSiteURL(ctx, mock.Anything).
			Return("", apperr.New(codes.Unavailable, "musicbrainz url-rels request failed")).Times(3)

		got, err := d.uc.RefreshDueOfficialSites(ctx)

		require.NoError(t, err)
		assert.Equal(t, &usecase.OfficialSiteRefreshSummary{
			Followed: 35, BatchSize: 5, Due: 5, Attempted: 3, Failed: 3, CircuitBroken: true,
		}, got)
		// No check is recorded, so every artist stays due for the next run.
		d.artistRepo.AssertNotCalled(t, "MarkOfficialSiteChecked", mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("a single failure does not stop the run", func(t *testing.T) {
		t.Parallel()
		d := newOfficialSiteRefreshTestDeps(t)
		due := followedArtists(3)

		d.followRepo.EXPECT().ListAll(ctx).Return(followedArtists(21), nil).Once()
		d.artistRepo.EXPECT().ListStaleOfficialSite(ctx, age, 3).Return(due, nil).Once()
		d.siteResolver.EXPECT().ResolveOfficialSiteURL(ctx, due[0].MBID).
			Return("", apperr.New(codes.Unavailable, "musicbrainz url-rels request failed")).Once()
		d.siteResolver.EXPECT().ResolveOfficialSiteURL(ctx, due[1].MBID).Return("", nil).Once()
		d.siteResolver.EXPECT().ResolveOfficialSiteURL(ctx, due[2].MBID).Return("", nil).Once()
		d.artistRepo.EXPECT().MarkOfficialSiteChecked(ctx, mock.Anything, mock.Anything).Return(nil).Times(2)

		got, err := d.uc.RefreshDueOfficialSites(ctx)

		require.NoError(t, err)
		assert.Equal(t, 3, got.Attempted)
		assert.Equal(t, 1, got.Failed)
		assert.False(t, got.CircuitBroken)
	})

	t.Run("stops when asked to shut down", func(t *testing.T) {
		t.Parallel()
		d := newOfficialSiteRefreshTestDeps(t)
		runCtx, cancel := context.WithCancel(ctx)
		due := followedArtists(3)

		d.followRepo.EXPECT().ListAll(runCtx).Return(followedArtists(21), nil).Once()
		d.artistRepo.EXPECT().ListStaleOfficialSite(runCtx, age, 3).Return(due, nil).Once()
		d.siteResolver.EXPECT().ResolveOfficialSiteURL(runCtx, due[0].MBID).
			RunAndReturn(func(context.Context, string) (string, error) {
				cancel()
				return "", nil
			}).Once()
		d.artistRepo.EXPECT().MarkOfficialSiteChecked(runCtx, due[0].ID, mock.Anything).Return(nil).Once()

		got, err := d.uc.RefreshDueOfficialSites(runCtx)

		require.NoError(t, err)
		assert.Equal(t, 1, got.Attempted)
	})

	t.Run("a ListAll failure is returned", func(t *testing.T) {
		t.Parallel()
		d := newOfficialSiteRefreshTestDeps(t)

		d.followRepo.EXPECT().ListAll(ctx).Return(nil, apperr.New(codes.Internal, "db down")).Once()

		got, err := d.uc.RefreshDueOfficialSites(ctx)

		assert.ErrorIs(t, err, apperr.ErrInternal)
		assert.Nil(t, got)
	})
}
