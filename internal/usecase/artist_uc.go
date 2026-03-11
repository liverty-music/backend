package usecase

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/messaging"
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

	// ListSimilar identifies artists with musical affinity to the target artist.
	// When limit is greater than zero, the result is capped to that many entries;
	// otherwise the external service's default is used.
	//
	// # Possible errors:
	//
	//   - NotFound: target artist not found in local or external catalogs.
	//   - Internal: service failure.
	ListSimilar(ctx context.Context, artistID string, limit int32) ([]*entity.Artist, error)

	// ListTop identifies trending or highly-rated artists, optionally filtered by country or genre tag.
	// When limit is greater than zero, the result is capped to that many entries;
	// otherwise the external service's default is used.
	//
	// # Possible errors:
	//
	//   - Unavailable: external chart service failure.
	ListTop(ctx context.Context, country string, tag string, limit int32) ([]*entity.Artist, error)
}

// artistUseCase implements the ArtistUseCase interface.
type artistUseCase struct {
	artistRepo     entity.ArtistRepository
	artistSearcher entity.ArtistSearcher
	idManager      entity.ArtistIdentityManager
	publisher      message.Publisher
	cache          entity.Cache
	logger         *logging.Logger
}

// Compile-time interface compliance check
var _ ArtistUseCase = (*artistUseCase)(nil)

// NewArtistUseCase creates a new instance of the artist business logic handler.
func NewArtistUseCase(
	artistRepo entity.ArtistRepository,
	artistSearcher entity.ArtistSearcher,
	idManager entity.ArtistIdentityManager,
	publisher message.Publisher,
	cache entity.Cache,
	logger *logging.Logger,
) ArtistUseCase {
	return &artistUseCase{
		artistRepo:     artistRepo,
		artistSearcher: artistSearcher,
		idManager:      idManager,
		publisher:      publisher,
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

	// Filter out entries with empty MBID and dedup by MBID keeping first occurrence.
	filtered := filterAndDedupByMBID(artists)

	if len(filtered) == 0 {
		return nil, apperr.New(codes.NotFound, "no artists found")
	}

	// Persist artists to ensure stable database IDs.
	persisted, err := uc.persistArtists(ctx, filtered)
	if err != nil {
		return nil, err
	}

	// Store in cache
	uc.cache.Set(cacheKey, persisted)

	return persisted, nil
}

// ListSimilar retrieves artists similar to a specified artist.
// Results are cached to reduce external API calls.
// Fetched artists are auto-persisted to ensure valid database IDs.
func (uc *artistUseCase) ListSimilar(ctx context.Context, artistID string, limit int32) ([]*entity.Artist, error) {
	// Check cache first
	cacheKey := fmt.Sprintf("similar:%s:%d", artistID, limit)
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

	artists, err := uc.artistSearcher.ListSimilar(ctx, artist, limit)
	if err != nil {
		return nil, err
	}

	// Filter out entries with empty MBID.
	filtered := filterAndDedupByMBID(artists)

	// Persist via read-then-write helper.
	persisted, err := uc.persistArtists(ctx, filtered)
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
func (uc *artistUseCase) ListTop(ctx context.Context, country string, tag string, limit int32) ([]*entity.Artist, error) {
	// Check cache first
	cacheKey := fmt.Sprintf("top:%s:%s:%d", country, tag, limit)
	if cached := uc.cache.Get(cacheKey); cached != nil {
		if artists, ok := cached.([]*entity.Artist); ok {
			return artists, nil
		}
	}

	// Cache miss - fetch from external API
	artists, err := uc.artistSearcher.ListTop(ctx, country, tag, limit)
	if err != nil {
		return nil, err
	}

	// Filter out entries with empty MBID.
	filtered := filterAndDedupByMBID(artists)

	// Persist via read-then-write helper.
	persisted, err := uc.persistArtists(ctx, filtered)
	if err != nil {
		return nil, err
	}

	// Store in cache
	uc.cache.Set(cacheKey, persisted)

	return persisted, nil
}

// filterAndDedupByMBID removes entries with empty MBID and deduplicates by MBID
// keeping the first occurrence.
func filterAndDedupByMBID(artists []*entity.Artist) []*entity.Artist {
	seen := make(map[string]struct{})
	result := make([]*entity.Artist, 0, len(artists))
	for _, a := range artists {
		if a.MBID == "" {
			continue
		}
		if _, ok := seen[a.MBID]; ok {
			continue
		}
		seen[a.MBID] = struct{}{}
		result = append(result, a)
	}
	return result
}

// persistArtists looks up existing artists by MBID, creates only missing ones,
// and returns all artists in the original input order. All input artists must
// have a non-empty MBID (caller responsibility).
func (uc *artistUseCase) persistArtists(ctx context.Context, artists []*entity.Artist) ([]*entity.Artist, error) {
	if len(artists) == 0 {
		return []*entity.Artist{}, nil
	}

	// Step 1: Collect MBIDs.
	mbids := make([]string, len(artists))
	for i, a := range artists {
		mbids[i] = a.MBID
	}

	// Step 2: Read existing.
	existing, err := uc.artistRepo.ListByMBIDs(ctx, mbids)
	if err != nil {
		return nil, err
	}

	existingSet := make(map[string]*entity.Artist, len(existing))
	for _, a := range existing {
		existingSet[a.MBID] = a
	}

	// Step 3: Determine missing.
	var missing []*entity.Artist
	for _, a := range artists {
		if _, ok := existingSet[a.MBID]; !ok {
			missing = append(missing, a)
		}
	}

	// Step 4: Create missing.
	if len(missing) > 0 {
		created, err := uc.artistRepo.Create(ctx, missing...)
		if err != nil {
			return nil, err
		}
		for _, a := range created {
			existingSet[a.MBID] = a

			msg, err := messaging.NewEvent(entity.ArtistCreatedData{
				ArtistID:   a.ID,
				ArtistName: a.Name,
				MBID:       a.MBID,
			})
			if err != nil {
				uc.logger.Warn(ctx, "failed to create artist.created event",
					slog.String("artist_id", a.ID), slog.Any("error", err))
				continue
			}
			if err := uc.publisher.Publish(entity.SubjectArtistCreated, msg); err != nil {
				uc.logger.Warn(ctx, "failed to publish artist.created event",
					slog.String("artist_id", a.ID), slog.Any("error", err))
			}
		}
	}

	// Step 5: Merge preserving input order.
	result := make([]*entity.Artist, 0, len(artists))
	for _, a := range artists {
		if persisted, ok := existingSet[a.MBID]; ok {
			result = append(result, persisted)
		}
	}

	return result, nil
}

// hashString creates a simple hash of a string for cache key consistency.
func hashString(s string) string {
	h := sha256.New()
	h.Write([]byte(s))
	return hex.EncodeToString(h.Sum(nil))
}
