package usecase

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-logging/logging"
)

// ArtistNameResolutionUseCase defines the interface for resolving canonical
// artist names from an external identity service.
type ArtistNameResolutionUseCase interface {
	// ResolveCanonicalName looks up the canonical artist name and updates
	// the database record if it differs from currentName.
	ResolveCanonicalName(ctx context.Context, artistID, mbid, currentName string) error
}

// artistNameResolutionUseCase implements ArtistNameResolutionUseCase.
type artistNameResolutionUseCase struct {
	artistRepo entity.ArtistRepository
	idManager  entity.ArtistIdentityManager
	logger     *logging.Logger
}

// Compile-time interface compliance check.
var _ ArtistNameResolutionUseCase = (*artistNameResolutionUseCase)(nil)

// NewArtistNameResolutionUseCase creates a new artist name resolution use case.
func NewArtistNameResolutionUseCase(
	artistRepo entity.ArtistRepository,
	idManager entity.ArtistIdentityManager,
	logger *logging.Logger,
) ArtistNameResolutionUseCase {
	return &artistNameResolutionUseCase{
		artistRepo: artistRepo,
		idManager:  idManager,
		logger:     logger,
	}
}

// ResolveCanonicalName resolves the canonical artist name from MusicBrainz
// and updates the database record if it differs from currentName.
func (uc *artistNameResolutionUseCase) ResolveCanonicalName(ctx context.Context, artistID, mbid, currentName string) error {
	canonical, err := uc.idManager.GetArtist(ctx, mbid)
	if err != nil {
		return fmt.Errorf("resolve canonical name: %w", err)
	}

	if canonical.Name == "" {
		uc.logger.Warn(ctx, "MusicBrainz returned empty name, skipping update",
			slog.String("artist_id", artistID),
			slog.String("mbid", mbid),
		)
		return nil
	}

	if canonical.Name == currentName {
		return nil
	}

	if err := uc.artistRepo.UpdateName(ctx, artistID, canonical.Name); err != nil {
		return fmt.Errorf("update artist name: %w", err)
	}

	uc.logger.Info(ctx, "artist name updated to canonical",
		slog.String("artist_id", artistID),
		slog.String("old_name", currentName),
		slog.String("new_name", canonical.Name),
	)

	return nil
}
