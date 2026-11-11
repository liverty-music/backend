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
	"github.com/stretchr/testify/assert"
)

// artistImageSyncTestDeps holds all dependencies for ArtistImageSyncUseCase tests.
type artistImageSyncTestDeps struct {
	artistRepo    *mocks.MockArtistRepository
	imageResolver *mocks.MockArtistImageResolver
	uc            usecase.ArtistImageSyncUseCase
}

func newArtistImageSyncTestDeps(t *testing.T) *artistImageSyncTestDeps {
	t.Helper()
	d := &artistImageSyncTestDeps{
		artistRepo:    mocks.NewMockArtistRepository(t),
		imageResolver: mocks.NewMockArtistImageResolver(t),
	}
	d.uc = usecase.NewArtistImageSyncUseCase(d.artistRepo, d.imageResolver, func() time.Time { return refreshNow }, newTestLogger(t))
	return d
}

// sampleFanart returns a Fanart fixture with one artist_background image.
func sampleFanart() *entity.Fanart {
	return &entity.Fanart{
		ArtistBackground: []entity.FanartImage{
			{ID: "background-1", URL: "https://assets.fanart.tv/fanart/music/background.jpg", Likes: 10},
		},
	}
}

func TestArtistImageSyncUseCase_SyncArtistImage(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	const (
		artistID = "artist-1"
		mbid     = "5b11f448-2d57-455b-8292-629df8357062"
	)

	tests := []struct {
		name    string
		mbid    string
		setup   func(t *testing.T, d *artistImageSyncTestDeps)
		wantErr error
	}{
		{
			// @spec components/usecase/artist/sync-artist-image "Images found"
			name: "images found are recorded with the current check time",
			mbid: mbid,
			setup: func(t *testing.T, d *artistImageSyncTestDeps) {
				t.Helper()
				fanart := sampleFanart()

				d.imageResolver.EXPECT().
					ResolveImages(ctx, mbid).
					Return(fanart, nil).
					Once()

				d.artistRepo.EXPECT().
					UpdateFanart(ctx, artistID, fanart, refreshNow).
					Return(nil).
					Once()
			},
		},
		{
			// @spec components/usecase/artist/sync-artist-image "Empty MBID"
			name: "empty MBID makes no lookup, records nothing and does not fail",
			mbid: "",
			setup: func(_ *testing.T, _ *artistImageSyncTestDeps) {
				// The mocks fail the test if ResolveImages or UpdateFanart is called.
			},
		},
		{
			// @spec components/usecase/artist/sync-artist-image "No images"
			name: "no images records no fanart with the current check time and does not fail",
			mbid: mbid,
			setup: func(t *testing.T, d *artistImageSyncTestDeps) {
				t.Helper()
				d.imageResolver.EXPECT().
					ResolveImages(ctx, mbid).
					Return(nil, nil).
					Once()

				d.artistRepo.EXPECT().
					UpdateFanart(ctx, artistID, (*entity.Fanart)(nil), refreshNow).
					Return(nil).
					Once()
			},
		},
		{
			name: "propagates artist repository error",
			mbid: mbid,
			setup: func(t *testing.T, d *artistImageSyncTestDeps) {
				t.Helper()
				d.imageResolver.EXPECT().
					ResolveImages(ctx, mbid).
					Return(nil, nil).
					Once()

				d.artistRepo.EXPECT().
					UpdateFanart(ctx, artistID, (*entity.Fanart)(nil), refreshNow).
					Return(apperr.New(codes.Internal, "db error")).
					Once()
			},
			wantErr: apperr.ErrInternal,
		},
		{
			name: "propagates image resolver error without calling repository",
			mbid: mbid,
			setup: func(t *testing.T, d *artistImageSyncTestDeps) {
				t.Helper()
				d.imageResolver.EXPECT().
					ResolveImages(ctx, mbid).
					Return(nil, apperr.New(codes.Unavailable, "fanart.tv unavailable")).
					Once()
			},
			wantErr: apperr.ErrUnavailable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d := newArtistImageSyncTestDeps(t)
			tt.setup(t, d)

			err := d.uc.SyncArtistImage(ctx, artistID, tt.mbid)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}
			assert.NoError(t, err)
		})
	}
}
