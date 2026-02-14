package usecase

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/pkg/cache"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
)

// ArtistUseCase defines the interface for artist-related business logic and orchestration.
type ArtistUseCase interface {
	// Create registers a new artist in the system, potentially normalizing their data from MusicBrainz.
	//
	// # Possible errors:
	//
	//   - InvalidArgument: the artist name or MBID is empty.
	//   - Internal: unexpected failure during creation or normalization.
	Create(ctx context.Context, artist *entity.Artist) (*entity.Artist, error)

	// List retrieves all artists registered in the system database.
	//
	// # Possible errors:
	//
	//   - Internal: database query failure.
	List(ctx context.Context) ([]*entity.Artist, error)

	// CreateOfficialSite associates a new verified link with an artist.
	//
	// # Possible errors:
	//
	//   - InvalidArgument: the URL is empty or the artist ID is missing.
	//   - Internal: database execution failure.
	CreateOfficialSite(ctx context.Context, site *entity.OfficialSite) error

	// GetOfficialSite retrieves the primary official website for an artist.
	//
	// # Possible errors:
	//
	//   - NotFound: the artist exists but has no official site.
	//   - Internal: query failure.
	GetOfficialSite(ctx context.Context, artistID string) (*entity.OfficialSite, error)

	// Search finds artists matching the query, prioritizing external discovery services.
	//
	// # Possible errors:
	//
	//   - InvalidArgument: the search query is empty.
	//   - NotFound: no matching artists found.
	Search(ctx context.Context, query string) ([]*entity.Artist, error)

	// Follow establishes a subscription between a user and an artist.
	//
	// # Possible errors:
	//
	//   - InvalidArgument: required identification (ID) or user context is missing.
	//   - NotFound: the artist does not exist.
	//   - Internal: unexpected failure during relationship establishment.
	Follow(ctx context.Context, userID string, artistID string) error

	// Unfollow terminates a subscription between a user and an artist.
	//
	// # Possible errors:
	//
	//   - InvalidArgument: missing user or artist identification.
	//   - Internal: execution failure.
	Unfollow(ctx context.Context, userID, artistID string) error

	// ListFollowed retrieves all artists currently followed by the specified user.
	//
	// # Possible errors:
	//
	//   - InvalidArgument: missing user identification.
	//   - Internal: query failure.
	ListFollowed(ctx context.Context, userID string) ([]*entity.Artist, error)

	// ListSimilar identifies artists with musical affinity to the target artist.
	//
	// # Possible errors:
	//
	//   - NotFound: target artist not found in local or external catalogs.
	//   - Internal: service failure.
	ListSimilar(ctx context.Context, artistID string) ([]*entity.Artist, error)

	// ListTop identifies trending or highly-rated artists, optionally filtered by country.
	//
	// # Possible errors:
	//
	//   - Unavailable: external chart service failure.
	ListTop(ctx context.Context, country string) ([]*entity.Artist, error)
}

// artistUseCase implements the ArtistUseCase interface.
type artistUseCase struct {
	artistRepo     entity.ArtistRepository
	artistSearcher entity.ArtistSearcher
	idManager      entity.ArtistIdentityManager
	cache          *cache.MemoryCache
	logger         *logging.Logger
}

// Compile-time interface compliance check
var _ ArtistUseCase = (*artistUseCase)(nil)

// NewArtistUseCase creates a new instance of the artist business logic handler.
func NewArtistUseCase(
	artistRepo entity.ArtistRepository,
	artistSearcher entity.ArtistSearcher,
	idManager entity.ArtistIdentityManager,
	cache *cache.MemoryCache,
	logger *logging.Logger,
) ArtistUseCase {
	return &artistUseCase{
		artistRepo:     artistRepo,
		artistSearcher: artistSearcher,
		idManager:      idManager,
		cache:          cache,
		logger:         logger,
	}
}

// Create creates a new artist.
func (uc *artistUseCase) Create(ctx context.Context, artist *entity.Artist) (*entity.Artist, error) {
	if artist.Name == "" && artist.MBID == "" {
		return nil, apperr.New(codes.InvalidArgument, "artist name or MBID is required")
	}

	// Normalize artist name using MBID if provided
	if artist.MBID != "" {
		mbArtist, err := uc.idManager.GetArtist(ctx, artist.MBID)
		if err != nil {
			// Log warning but proceed with provided name if normalization fails
			uc.logger.Warn(ctx, "failed to normalize artist name from MBID", slog.String("mbid", artist.MBID), slog.Any("error", err))
		} else if artist.Name != mbArtist.Name {
			// Update name to canonical name from MusicBrainz
			artist.Name = mbArtist.Name
			artist.MBID = mbArtist.MBID // Ensure MBID is set from canonical source
		}
	}

	if artist.Name == "" {
		return nil, apperr.New(codes.InvalidArgument, "artist name is required")
	}

	if artist.ID == "" {
		artist.ID = entity.NewID()
	}

	err := uc.artistRepo.Create(ctx, artist)
	if err != nil {
		return nil, err
	}

	uc.logger.Info(ctx, "Artist created successfully", slog.String("artist_id", artist.ID), slog.String("mbid", artist.MBID))

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

// Search finds artists matching the query using the primary external discovery service.
// Results are cached to reduce external API calls.
func (uc *artistUseCase) Search(ctx context.Context, query string) ([]*entity.Artist, error) {
	if query == "" {
		return nil, apperr.New(codes.InvalidArgument, "search query is required")
	}

	// Check cache first
	cacheKey := fmt.Sprintf("search:%s", hashString(query))
	if cached := uc.cache.Get(cacheKey); cached != nil {
		if artists, ok := cached.([]*entity.Artist); ok {
			return artists, nil
		}
	}

	// Cache miss - fetch from external API
	artists, err := uc.artistSearcher.Search(ctx, query)
	if err != nil {
		return nil, apperr.Wrap(err, codes.Internal, "failed to search artists")
	}

	if len(artists) == 0 {
		return nil, apperr.New(codes.NotFound, "no artists found")
	}

	// Store in cache
	uc.cache.Set(cacheKey, artists)

	return artists, nil
}

// Follow establishes a follow relationship between a user and an artist.
func (uc *artistUseCase) Follow(ctx context.Context, userID string, artistID string) error {
	if userID == "" || artistID == "" {
		return apperr.New(codes.InvalidArgument, "user ID and artist ID are required")
	}

	err := uc.artistRepo.Follow(ctx, userID, artistID)
	if err != nil {
		// Treat "already following" as success  
		if errors.Is(err, apperr.ErrAlreadyExists) {
			return nil
		}
		return apperr.Wrap(err, codes.Internal, "failed to establish follow relationship")
	}

	uc.logger.Info(ctx, "User followed artist", slog.String("user_id", userID), slog.String("artist_id", artistID))
	return nil
}

// Unfollow removes a follow relationship.
func (uc *artistUseCase) Unfollow(ctx context.Context, userID, artistID string) error {
	if userID == "" || artistID == "" {
		return apperr.New(codes.InvalidArgument, "user ID and artist ID are required")
	}

	err := uc.artistRepo.Unfollow(ctx, userID, artistID)
	if err != nil {
		return err
	}

	uc.logger.Info(ctx, "Artist unfollowed", slog.String("user_id", userID), slog.String("artist_id", artistID))
	return nil
}

// ListFollowed retrieves the list of artists followed by a user.
func (uc *artistUseCase) ListFollowed(ctx context.Context, userID string) ([]*entity.Artist, error) {
	if userID == "" {
		return nil, apperr.New(codes.InvalidArgument, "user ID is required")
	}

	return uc.artistRepo.ListFollowed(ctx, userID)
}

// ListSimilar retrieves artists similar to a specified artist.
// Results are cached to reduce external API calls.
func (uc *artistUseCase) ListSimilar(ctx context.Context, artistID string) ([]*entity.Artist, error) {
	// Check cache first
	cacheKey := fmt.Sprintf("similar:%s", artistID)
	if cached := uc.cache.Get(cacheKey); cached != nil {
		if artists, ok := cached.([]*entity.Artist); ok {
			return artists, nil
		}
	}

	// Cache miss - fetch artist and get similar artists
	artist, err := uc.artistRepo.Get(ctx, artistID)
	if err != nil {
		return nil, err
	}

	artists, err := uc.artistSearcher.ListSimilar(ctx, artist)
	if err != nil {
		return nil, err
	}

	// Store in cache
	uc.cache.Set(cacheKey, artists)

	return artists, nil
}

// ListTop retrieves popular artists.
// Results are cached to reduce external API calls.
func (uc *artistUseCase) ListTop(ctx context.Context, country string) ([]*entity.Artist, error) {
	// Check cache first
	cacheKey := fmt.Sprintf("top:%s", country)
	if cached := uc.cache.Get(cacheKey); cached != nil {
		if artists, ok := cached.([]*entity.Artist); ok {
			return artists, nil
		}
	}

	// Cache miss - fetch from external API
	artists, err := uc.artistSearcher.ListTop(ctx, country)
	if err != nil {
		return nil, err
	}

	// Store in cache
	uc.cache.Set(cacheKey, artists)

	return artists, nil
}

// hashString creates a simple hash of a string for cache key consistency.
func hashString(s string) string {
	h := sha256.New()
	h.Write([]byte(s))
	return hex.EncodeToString(h.Sum(nil))
}
