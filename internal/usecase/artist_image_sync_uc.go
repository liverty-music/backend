package usecase

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-logging/logging"
)

// ArtistImageSyncUseCase defines the interface for syncing artist image data
// from an external provider (fanart.tv).
type ArtistImageSyncUseCase interface {
	// SyncArtistImage fetches image data for the given artist and persists
	// the result. When no images are found, the sync timestamp is still
	// updated to avoid re-fetching on every run.
	SyncArtistImage(ctx context.Context, artistID, mbid string) error
}

// artistImageSyncUseCase implements ArtistImageSyncUseCase.
type artistImageSyncUseCase struct {
	artistRepo    entity.ArtistRepository
	imageResolver entity.ArtistImageResolver
	logoFetcher   entity.LogoImageFetcher
	logger        *logging.Logger
}

// Compile-time interface compliance check.
var _ ArtistImageSyncUseCase = (*artistImageSyncUseCase)(nil)

// NewArtistImageSyncUseCase creates a new artist image sync use case.
func NewArtistImageSyncUseCase(
	artistRepo entity.ArtistRepository,
	imageResolver entity.ArtistImageResolver,
	logoFetcher entity.LogoImageFetcher,
	logger *logging.Logger,
) ArtistImageSyncUseCase {
	return &artistImageSyncUseCase{
		artistRepo:    artistRepo,
		imageResolver: imageResolver,
		logoFetcher:   logoFetcher,
		logger:        logger,
	}
}

// SyncArtistImage resolves images from the external provider, analyzes the
// best logo for color profiling, and updates the artist record.
// When mbid is empty the artist has no MusicBrainz identity and image sync
// is skipped. When ResolveImages returns nil (no images found), fanart_synced_at
// is still updated to record that the artist was checked.
func (uc *artistImageSyncUseCase) SyncArtistImage(ctx context.Context, artistID, mbid string) error {
	if mbid == "" {
		uc.logger.Info(ctx, "skipping image sync: artist has no MusicBrainz ID",
			slog.String("artist_id", artistID),
		)
		return nil
	}

	fanart, err := uc.imageResolver.ResolveImages(ctx, mbid)
	if err != nil {
		return fmt.Errorf("resolve images for artist %s: %w", artistID, err)
	}

	if fanart != nil {
		uc.profileLogoColor(ctx, fanart, artistID)
	}

	now := time.Now()
	if err := uc.artistRepo.UpdateFanart(ctx, artistID, fanart, now); err != nil {
		return fmt.Errorf("update fanart for artist %s: %w", artistID, err)
	}

	if fanart == nil {
		uc.logger.Info(ctx, "no fanart.tv images found for artist",
			slog.String("artist_id", artistID),
			slog.String("mbid", mbid),
		)
	} else {
		uc.logger.Info(ctx, "artist fanart synced",
			slog.String("artist_id", artistID),
			slog.String("mbid", mbid),
			slog.Int("thumbs", len(fanart.ArtistThumb)),
			slog.Int("logos", len(fanart.HDMusicLogo)),
			slog.Bool("has_color_profile", fanart.LogoColorProfile != nil),
		)
	}

	return nil
}

// profileLogoColor downloads the best logo image and analyzes its color.
// Failures are non-fatal: a warning is logged and LogoColorProfile remains nil.
func (uc *artistImageSyncUseCase) profileLogoColor(ctx context.Context, fanart *entity.Fanart, artistID string) {
	logoURL := entity.BestLogoURL(fanart)
	if logoURL == "" {
		uc.logger.Info(ctx, "no logo available for color profiling",
			slog.String("artist_id", artistID),
		)
		return
	}

	img, err := uc.logoFetcher.FetchImage(ctx, logoURL)
	if err != nil {
		uc.logger.Warn(ctx, "failed to fetch logo image for color profiling",
			slog.String("artist_id", artistID),
			slog.String("logo_url", logoURL),
			slog.String("error", err.Error()),
		)
		return
	}
	if img == nil {
		uc.logger.Warn(ctx, "logo image not found at URL",
			slog.String("artist_id", artistID),
			slog.String("logo_url", logoURL),
		)
		return
	}

	fanart.LogoColorProfile = entity.AnalyzeLogo(img)
}
