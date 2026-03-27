package usecase_test

import (
	"context"
	"image"
	"image/color"
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// artistImageSyncTestDeps holds all dependencies for ArtistImageSyncUseCase tests.
type artistImageSyncTestDeps struct {
	artistRepo    *mocks.MockArtistRepository
	imageResolver *mocks.MockArtistImageResolver
	logoFetcher   *mocks.MockLogoImageFetcher
	uc            usecase.ArtistImageSyncUseCase
}

func newArtistImageSyncTestDeps(t *testing.T) *artistImageSyncTestDeps {
	t.Helper()
	d := &artistImageSyncTestDeps{
		artistRepo:    mocks.NewMockArtistRepository(t),
		imageResolver: mocks.NewMockArtistImageResolver(t),
		logoFetcher:   mocks.NewMockLogoImageFetcher(t),
	}
	d.uc = usecase.NewArtistImageSyncUseCase(d.artistRepo, d.imageResolver, d.logoFetcher, newTestLogger(t))
	return d
}

// opaqueImage returns a small RGBA image filled with opaque white pixels
// so that entity.AnalyzeLogo can find non-transparent pixels to analyze.
func opaqueImage(w, h int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	white := color.RGBA{R: 255, G: 255, B: 255, A: 255}
	for y := range h {
		for x := range w {
			img.Set(x, y, white)
		}
	}
	return img
}

// sampleFanart returns a Fanart fixture with one logo image.
func sampleFanart() *entity.Fanart {
	return &entity.Fanart{
		HDMusicLogo: []entity.FanartImage{
			{ID: "logo-1", URL: "https://assets.fanart.tv/fanart/music/logo.png", Likes: 10},
		},
		ArtistThumb: []entity.FanartImage{
			{ID: "thumb-1", URL: "https://assets.fanart.tv/fanart/music/thumb.jpg", Likes: 5},
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
			name: "full sync with valid MBID resolves images and profiles logo color",
			mbid: mbid,
			setup: func(t *testing.T, d *artistImageSyncTestDeps) {
				t.Helper()
				fanart := sampleFanart()

				d.imageResolver.EXPECT().
					ResolveImages(ctx, mbid).
					Return(fanart, nil).
					Once()

				// Logo fetcher is called for color profiling.
				d.logoFetcher.EXPECT().
					FetchImage(ctx, mock.AnythingOfType("string")).
					Return(opaqueImage(10, 10), nil).
					Once()

				d.artistRepo.EXPECT().
					UpdateFanart(ctx, artistID, fanart, mock.AnythingOfType("time.Time")).
					Return(nil).
					Once()
			},
		},
		{
			name: "returns early without error when MBID is empty",
			mbid: "",
			setup: func(_ *testing.T, _ *artistImageSyncTestDeps) {
				// Neither imageResolver nor artistRepo should be called.
			},
		},
		{
			name: "no images found updates fanart with nil and does not error",
			mbid: mbid,
			setup: func(t *testing.T, d *artistImageSyncTestDeps) {
				t.Helper()
				d.imageResolver.EXPECT().
					ResolveImages(ctx, mbid).
					Return(nil, nil).
					Once()

				d.artistRepo.EXPECT().
					UpdateFanart(ctx, artistID, (*entity.Fanart)(nil), mock.AnythingOfType("time.Time")).
					Return(nil).
					Once()
			},
		},
		{
			name: "logo fetch failure updates fanart without logo color and does not error",
			mbid: mbid,
			setup: func(t *testing.T, d *artistImageSyncTestDeps) {
				t.Helper()
				fanart := sampleFanart()

				d.imageResolver.EXPECT().
					ResolveImages(ctx, mbid).
					Return(fanart, nil).
					Once()

				d.logoFetcher.EXPECT().
					FetchImage(ctx, mock.AnythingOfType("string")).
					Return(nil, apperr.New(codes.Unavailable, "fetch failed")).
					Once()

				d.artistRepo.EXPECT().
					UpdateFanart(ctx, artistID, mock.MatchedBy(func(f *entity.Fanart) bool {
						return f != nil && f.LogoColorProfile == nil
					}), mock.AnythingOfType("time.Time")).
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
					UpdateFanart(ctx, artistID, (*entity.Fanart)(nil), mock.AnythingOfType("time.Time")).
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

func TestArtistImageSyncUseCase_ProfileLogoColor(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	const artistID = "artist-1"

	t.Run("sets LogoColorProfile when logo download and analysis succeed", func(t *testing.T) {
		t.Parallel()
		d := newArtistImageSyncTestDeps(t)

		fanart := sampleFanart()
		img := opaqueImage(100, 100)

		d.logoFetcher.EXPECT().
			FetchImage(ctx, mock.AnythingOfType("string")).
			Return(img, nil).
			Once()

		usecase.ExportedProfileLogoColor(d.uc, ctx, fanart, artistID)

		assert.NotNil(t, fanart.LogoColorProfile)
	})

	t.Run("leaves LogoColorProfile nil when logo download fails", func(t *testing.T) {
		t.Parallel()
		d := newArtistImageSyncTestDeps(t)

		fanart := sampleFanart()

		d.logoFetcher.EXPECT().
			FetchImage(ctx, mock.AnythingOfType("string")).
			Return(nil, apperr.New(codes.Unavailable, "network error")).
			Once()

		usecase.ExportedProfileLogoColor(d.uc, ctx, fanart, artistID)

		assert.Nil(t, fanart.LogoColorProfile)
	})

	t.Run("leaves LogoColorProfile nil when fanart has no logos", func(t *testing.T) {
		t.Parallel()
		d := newArtistImageSyncTestDeps(t)

		fanart := &entity.Fanart{
			ArtistThumb: []entity.FanartImage{{ID: "t1", URL: "https://example.com/thumb.jpg"}},
		}

		// No FetchImage call expected — no logo URL available.
		usecase.ExportedProfileLogoColor(d.uc, ctx, fanart, artistID)

		assert.Nil(t, fanart.LogoColorProfile)
	})
}
