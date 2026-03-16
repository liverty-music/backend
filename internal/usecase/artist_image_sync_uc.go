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
	logger        *logging.Logger
}

// Compile-time interface compliance check.
var _ ArtistImageSyncUseCase = (*artistImageSyncUseCase)(nil)

// NewArtistImageSyncUseCase creates a new artist image sync use case.
func NewArtistImageSyncUseCase(
	artistRepo entity.ArtistRepository,
	imageResolver entity.ArtistImageResolver,
	logger *logging.Logger,
) ArtistImageSyncUseCase {
	return &artistImageSyncUseCase{
		artistRepo:    artistRepo,
		imageResolver: imageResolver,
		logger:        logger,
	}
}

// SyncArtistImage resolves images from the external provider and updates
// the artist record. When ResolveImages returns nil (no images found),
// fanart_synced_at is still updated to record that the artist was checked.
func (uc *artistImageSyncUseCase) SyncArtistImage(ctx context.Context, artistID, mbid string) error {
	fanart, err := uc.imageResolver.ResolveImages(ctx, mbid)
	if err != nil {
		return fmt.Errorf("resolve images for artist %s: %w", artistID, err)
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
		)
	}

	return nil
}
