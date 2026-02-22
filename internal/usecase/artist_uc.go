package usecase

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
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

	// SetPassionLevel updates the enthusiasm tier for a followed artist.
	//
	// # Possible errors:
	//
	//   - InvalidArgument: missing user or artist identification, or invalid level.
	//   - NotFound: the user is not following the specified artist.
	SetPassionLevel(ctx context.Context, userID, artistID string, level entity.PassionLevel) error

	// ListFollowed retrieves all artists currently followed by the specified user,
	// enriched with per-user passion level metadata.
	//
	// # Possible errors:
	//
	//   - InvalidArgument: missing user identification.
	//   - Internal: query failure.
	ListFollowed(ctx context.Context, userID string) ([]*entity.FollowedArtist, error)

	// ListSimilar identifies artists with musical affinity to the target artist.
	//
	// # Possible errors:
	//
	//   - NotFound: target artist not found in local or external catalogs.
	//   - Internal: service failure.
	ListSimilar(ctx context.Context, artistID string) ([]*entity.Artist, error)

	// ListTop identifies trending or highly-rated artists, optionally filtered by country or genre tag.
	//
	// # Possible errors:
	//
	//   - Unavailable: external chart service failure.
	ListTop(ctx context.Context, country string, tag string) ([]*entity.Artist, error)
}

// artistUseCase implements the ArtistUseCase interface.
type artistUseCase struct {
	artistRepo     entity.ArtistRepository
	userRepo       entity.UserRepository
	artistSearcher entity.ArtistSearcher
	idManager      entity.ArtistIdentityManager
	siteResolver   entity.OfficialSiteResolver
	cache          *cache.MemoryCache
	logger         *logging.Logger
}

// Compile-time interface compliance check
var _ ArtistUseCase = (*artistUseCase)(nil)

// NewArtistUseCase creates a new instance of the artist business logic handler.
func NewArtistUseCase(
	artistRepo entity.ArtistRepository,
	userRepo entity.UserRepository,
	artistSearcher entity.ArtistSearcher,
	idManager entity.ArtistIdentityManager,
	siteResolver entity.OfficialSiteResolver,
	cache *cache.MemoryCache,
	logger *logging.Logger,
) ArtistUseCase {
	return &artistUseCase{
		artistRepo:     artistRepo,
		userRepo:       userRepo,
		artistSearcher: artistSearcher,
		idManager:      idManager,
		siteResolver:   siteResolver,
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

	created, err := uc.artistRepo.Create(ctx, artist)
	if err != nil {
		return nil, err
	}

	if len(created) == 0 {
		return nil, apperr.New(codes.Internal, "artist creation returned no results")
	}

	uc.logger.Info(ctx, "Artist created successfully", slog.String("artist_id", created[0].ID), slog.String("mbid", created[0].MBID))

	return created[0], nil
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

// resolveUserID maps an external identity (Zitadel sub claim) to the internal user UUID.
func (uc *artistUseCase) resolveUserID(ctx context.Context, externalID string) (string, error) {
	user, err := uc.userRepo.GetByExternalID(ctx, externalID)
	if err != nil {
		return "", fmt.Errorf("resolve user by external ID: %w", err)
	}
	return user.ID, nil
}

// Follow establishes a follow relationship between a user and an artist.
// After the follow is persisted, it asynchronously resolves and stores the
// artist's official site URL if one is not already recorded.
func (uc *artistUseCase) Follow(ctx context.Context, userID string, artistID string) error {
	if userID == "" || artistID == "" {
		return apperr.New(codes.InvalidArgument, "user ID and artist ID are required")
	}

	internalUserID, err := uc.resolveUserID(ctx, userID)
	if err != nil {
		return err
	}

	err = uc.artistRepo.Follow(ctx, internalUserID, artistID)
	if err != nil {
		// Treat "already following" as success
		if errors.Is(err, apperr.ErrAlreadyExists) {
			return nil
		}
		return apperr.Wrap(err, codes.Internal, "failed to establish follow relationship")
	}

	uc.logger.Info(ctx, "User followed artist", slog.String("user_id", internalUserID), slog.String("artist_id", artistID))

	bgCtx := context.WithoutCancel(ctx)
	go uc.resolveAndPersistOfficialSite(bgCtx, artistID)

	return nil
}

// resolveAndPersistOfficialSite fetches the official site URL from MusicBrainz
// and persists it for the given artist. It is intended to run in a background
// goroutine; all errors are logged and swallowed.
func (uc *artistUseCase) resolveAndPersistOfficialSite(ctx context.Context, artistID string) {
	// Skip if a record already exists.
	_, err := uc.artistRepo.GetOfficialSite(ctx, artistID)
	if err == nil {
		return
	}
	if !errors.Is(err, apperr.ErrNotFound) {
		uc.logger.Warn(ctx, "failed to check official site before resolution", slog.String("artist_id", artistID), slog.Any("error", err))
		return
	}

	artist, err := uc.artistRepo.Get(ctx, artistID)
	if err != nil {
		uc.logger.Warn(ctx, "failed to get artist for official site resolution", slog.String("artist_id", artistID), slog.Any("error", err))
		return
	}

	if artist.MBID == "" {
		return
	}

	url, err := uc.siteResolver.ResolveOfficialSiteURL(ctx, artist.MBID)
	if err != nil {
		uc.logger.Warn(ctx, "failed to resolve official site URL", slog.String("artist_id", artistID), slog.String("mbid", artist.MBID), slog.Any("error", err))
		return
	}
	if url == "" {
		return
	}

	site := &entity.OfficialSite{
		ID:       func() string { id, _ := uuid.NewV7(); return id.String() }(),
		ArtistID: artistID,
		URL:      url,
	}
	if err := uc.artistRepo.CreateOfficialSite(ctx, site); err != nil {
		if !errors.Is(err, apperr.ErrAlreadyExists) {
			uc.logger.Warn(ctx, "failed to persist official site", slog.String("artist_id", artistID), slog.String("url", url), slog.Any("error", err))
		}
		return
	}

	uc.logger.Info(ctx, "official site resolved and persisted", slog.String("artist_id", artistID), slog.String("url", url))
}

// Unfollow removes a follow relationship.
func (uc *artistUseCase) Unfollow(ctx context.Context, userID, artistID string) error {
	if userID == "" || artistID == "" {
		return apperr.New(codes.InvalidArgument, "user ID and artist ID are required")
	}

	internalUserID, err := uc.resolveUserID(ctx, userID)
	if err != nil {
		return err
	}

	err = uc.artistRepo.Unfollow(ctx, internalUserID, artistID)
	if err != nil {
		return err
	}

	uc.logger.Info(ctx, "Artist unfollowed", slog.String("user_id", internalUserID), slog.String("artist_id", artistID))
	return nil
}

// SetPassionLevel updates the enthusiasm tier for a followed artist.
func (uc *artistUseCase) SetPassionLevel(ctx context.Context, userID, artistID string, level entity.PassionLevel) error {
	if userID == "" || artistID == "" {
		return apperr.New(codes.InvalidArgument, "user ID and artist ID are required")
	}

	err := uc.artistRepo.SetPassionLevel(ctx, userID, artistID, level)
	if err != nil {
		return err
	}

	uc.logger.Info(ctx, "Passion level updated",
		slog.String("user_id", userID),
		slog.String("artist_id", artistID),
		slog.String("level", string(level)),
	)
	return nil
}

// ListFollowed retrieves the list of artists followed by a user, including passion level.
func (uc *artistUseCase) ListFollowed(ctx context.Context, userID string) ([]*entity.FollowedArtist, error) {
	if userID == "" {
		return nil, apperr.New(codes.InvalidArgument, "user ID is required")
	}

	internalUserID, err := uc.resolveUserID(ctx, userID)
	if err != nil {
		return nil, err
	}

	return uc.artistRepo.ListFollowed(ctx, internalUserID)
}

// ListSimilar retrieves artists similar to a specified artist.
// Results are cached to reduce external API calls.
// Fetched artists are auto-persisted to ensure valid database IDs.
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

	// Auto-persist fetched artists to ensure valid database IDs
	persisted, err := uc.artistRepo.Create(ctx, artists...)
	if err != nil {
		return nil, err
	}

	// Store in cache
	uc.cache.Set(cacheKey, persisted)

	return persisted, nil
}

// ListTop retrieves popular artists.
// Results are cached to reduce external API calls.
// Fetched artists are auto-persisted to ensure valid database IDs.
func (uc *artistUseCase) ListTop(ctx context.Context, country string, tag string) ([]*entity.Artist, error) {
	// Check cache first
	cacheKey := fmt.Sprintf("top:%s:%s", country, tag)
	if cached := uc.cache.Get(cacheKey); cached != nil {
		if artists, ok := cached.([]*entity.Artist); ok {
			return artists, nil
		}
	}

	// Cache miss - fetch from external API
	artists, err := uc.artistSearcher.ListTop(ctx, country, tag)
	if err != nil {
		return nil, err
	}

	// Auto-persist fetched artists to ensure valid database IDs
	persisted, err := uc.artistRepo.Create(ctx, artists...)
	if err != nil {
		return nil, err
	}

	// Store in cache
	uc.cache.Set(cacheKey, persisted)

	return persisted, nil
}

// hashString creates a simple hash of a string for cache key consistency.
func hashString(s string) string {
	h := sha256.New()
	h.Write([]byte(s))
	return hex.EncodeToString(h.Sum(nil))
}
