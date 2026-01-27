package usecase

import (
	"context"
	"log/slog"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
)

// ArtistUseCase defines the interface for artist-related business logic.
type ArtistUseCase interface {
	Create(ctx context.Context, artist *entity.Artist) (*entity.Artist, error)
	List(ctx context.Context) ([]*entity.Artist, error)
	AddMedia(ctx context.Context, media *entity.Media) error
	DeleteMedia(ctx context.Context, mediaID string) error
}

// artistUseCase implements the ArtistUseCase interface.
type artistUseCase struct {
	artistRepo entity.ArtistRepository
	logger     *logging.Logger
}

// Compile-time interface compliance check
var _ ArtistUseCase = (*artistUseCase)(nil)

// NewArtistUseCase creates a new artist use case.
func NewArtistUseCase(artistRepo entity.ArtistRepository, logger *logging.Logger) ArtistUseCase {
	return &artistUseCase{
		artistRepo: artistRepo,
		logger:     logger,
	}
}

// Create creates a new artist and its media.
func (uc *artistUseCase) Create(ctx context.Context, artist *entity.Artist) (*entity.Artist, error) {
	if artist.Name == "" {
		return nil, apperr.New(codes.InvalidArgument, "artist name is required")
	}

	err := uc.artistRepo.Create(ctx, artist)
	if err != nil {
		return nil, err
	}

	uc.logger.Info(ctx, "Artist created successfully", slog.String("artist_id", artist.ID))

	return artist, nil
}

// List returns all artists.
func (uc *artistUseCase) List(ctx context.Context) ([]*entity.Artist, error) {
	artists, err := uc.artistRepo.List(ctx)
	if err != nil {
		return nil, err
	}

	return artists, nil
}

// AddMedia adds a media link to an artist.
func (uc *artistUseCase) AddMedia(ctx context.Context, media *entity.Media) error {
	if media.URL == "" {
		return apperr.New(codes.InvalidArgument, "media URL is required")
	}

	err := uc.artistRepo.AddMedia(ctx, media)
	if err != nil {
		return err
	}

	return nil
}

// DeleteMedia removes a media link.
func (uc *artistUseCase) DeleteMedia(ctx context.Context, mediaID string) error {
	if mediaID == "" {
		return apperr.New(codes.InvalidArgument, "media ID is required")
	}

	err := uc.artistRepo.DeleteMedia(ctx, mediaID)
	if err != nil {
		return err
	}

	return nil
}
