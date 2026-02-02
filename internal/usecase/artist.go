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
	// Create creates a new artist.
	//
	// # Possible errors
	//
	//  - InvalidArgument: If the artist name is empty.
	Create(ctx context.Context, artist *entity.Artist) (*entity.Artist, error)

	// List returns a list of all artists.
	List(ctx context.Context) ([]*entity.Artist, error)

	// CreateOfficialSite creates a new official site for an artist.
	//
	// # Possible errors
	//
	//  - InvalidArgument: If the URL is empty.
	CreateOfficialSite(ctx context.Context, site *entity.OfficialSite) error

	// GetOfficialSite retrieves the official site for an artist.
	//
	// # Possible errors
	//
	//  - NotFound: If the official site does not exist.
	GetOfficialSite(ctx context.Context, artistID string) (*entity.OfficialSite, error)
}

// artistUseCase implements the ArtistUseCase interface.
type artistUseCase struct {
	artistRepo entity.ArtistRepository
	logger     *logging.Logger
}

// Compile-time interface compliance check
var _ ArtistUseCase = (*artistUseCase)(nil)

// NewArtistUseCase creates a new artist use case.
// It requires an artist repository for data persistence and a logger for operations logging.
func NewArtistUseCase(artistRepo entity.ArtistRepository, logger *logging.Logger) ArtistUseCase {
	return &artistUseCase{
		artistRepo: artistRepo,
		logger:     logger,
	}
}

// Create creates a new artist.
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

// CreateOfficialSite adds an official site for an artist.
func (uc *artistUseCase) CreateOfficialSite(ctx context.Context, site *entity.OfficialSite) error {
	if site.URL == "" {
		return apperr.New(codes.InvalidArgument, "official site URL is required")
	}

	err := uc.artistRepo.CreateOfficialSite(ctx, site)
	if err != nil {
		return err
	}

	return nil
}

// GetOfficialSite retrieves the official site for an artist.
func (uc *artistUseCase) GetOfficialSite(ctx context.Context, artistID string) (*entity.OfficialSite, error) {
	if artistID == "" {
		return nil, apperr.New(codes.InvalidArgument, "artist ID is required")
	}

	site, err := uc.artistRepo.GetOfficialSite(ctx, artistID)
	if err != nil {
		return nil, err
	}

	return site, nil
}
