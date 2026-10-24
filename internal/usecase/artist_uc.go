package usecase

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
)

// ArtistUseCase defines the interface for artist-related business logic and orchestration.
type ArtistUseCase interface {
	// Create registers a new artist in the system, potentially normalizing their data from MusicBrainz.
	//
	// # Possible errors:
	//
	//   - Internal: unexpected failure during creation or normalization.
	Create(ctx context.Context, artist *entity.Artist) (*entity.Artist, error)

	// List retrieves all artists registered in the system database.
	//
	// # Possible errors:
	//
	//   - Internal: database query failure.
	List(ctx context.Context) ([]*entity.Artist, error)

	// Get returns one artist by its id.
	//
	// # Possible errors:
	//
	//   - InvalidArgument: the id is not a UUID.
	//   - NotFound: no artist has the id.
	//   - Internal: database query failure.
	Get(ctx context.Context, id string) (*entity.Artist, error)

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
	//   - NotFound: no matching artists found.
	//   - Unavailable: external discovery service failure.
	//   - ResourceExhausted: external discovery service rate limit exceeded.
	//   - DeadlineExceeded: external discovery service request timed out.
	//   - Internal: unexpected failure while persisting discovered artists.
	Search(ctx context.Context, query string) ([]*entity.Artist, error)

	// ListSimilar identifies artists with musical affinity to the target artist.
	// When limit is greater than zero, the result is capped to that many entries;
	// otherwise every artist of the 50-artist catalog lookup is returned.
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

	// ListByOrganizer returns the Artists the Organizer currently represents,
	// whatever its status. It checks no ownership: the admin names any
	// Organizer, and the organizer console passes the caller's own.
	//
	// # Possible errors
	//
	//   - NotFound: no Organizer has the id.
	ListByOrganizer(ctx context.Context, organizerID string) ([]*entity.Artist, error)
}

// ArtistCacheTTL is how long ArtistUseCase reuses a catalog result (Search,
// ListSimilar, ListTop) before consulting the catalog again. Charts and
// catalog matches do not change materially within hours.
const ArtistCacheTTL = 6 * time.Hour

// similarLookupLimit is the number of similar artists ListSimilar asks the
// catalog for on a cache miss, whatever limit the caller requested. One
// cached lookup per artist then serves every limit up to it.
const similarLookupLimit = 50

// artistUseCase implements the ArtistUseCase interface.
type artistUseCase struct {
	artistRepo     entity.ArtistRepository
	organizerRepo  entity.OrganizerRepository
	artistSearcher entity.ArtistSearcher
	idManager      entity.ArtistIdentityManager
	publisher      EventPublisher
	cache          entity.Cache
	logger         *logging.Logger
}

// Compile-time interface compliance check
var _ ArtistUseCase = (*artistUseCase)(nil)

// NewArtistUseCase creates a new instance of the artist business logic handler.
func NewArtistUseCase(
	artistRepo entity.ArtistRepository,
	organizerRepo entity.OrganizerRepository,
	artistSearcher entity.ArtistSearcher,
	idManager entity.ArtistIdentityManager,
	publisher EventPublisher,
	cache entity.Cache,
	logger *logging.Logger,
) ArtistUseCase {
	return &artistUseCase{
		artistRepo:     artistRepo,
		organizerRepo:  organizerRepo,
		artistSearcher: artistSearcher,
		idManager:      idManager,
		publisher:      publisher,
		cache:          cache,
		logger:         logger,
	}
}

// Create creates a new artist.
func (uc *artistUseCase) Create(ctx context.Context, artist *entity.Artist) (*entity.Artist, error) {
	// Normalize artist name using MBID
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

// GetOfficialSite retrieves the official site for an artist.
func (uc *artistUseCase) GetOfficialSite(ctx context.Context, artistID string) (*entity.OfficialSite, error) {
	site, err := uc.artistRepo.GetOfficialSite(ctx, artistID)
	if err != nil {
		return nil, err
	}

	return site, nil
}

// Search finds artists matching the query using the primary external discovery service.
// Results are cached to reduce external API calls.
func (uc *artistUseCase) Search(ctx context.Context, query string) ([]*entity.Artist, error) {
	// Check cache first
	cacheKey := fmt.Sprintf("search:%s", hashString(query))
	if cached := uc.cache.Get(cacheKey); cached != nil {
		if artists, ok := cached.([]*entity.Artist); ok {
			return artists, nil
		}
	}

	// Cache miss - fetch from external API. Propagate the port's error as-is
	// (mirroring ListSimilar/ListTop) so its code (NotFound, Unavailable,
	// ResourceExhausted, DeadlineExceeded, ...) survives to the caller instead
	// of being collapsed into Internal.
	artists, err := uc.artistSearcher.Search(ctx, query)
	if err != nil {
		return nil, err
	}

	// Filter out entries with empty MBID and dedup by MBID keeping first occurrence.
	filtered := entity.FilterArtistsByMBID(artists)

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
// The catalog is asked for similarLookupLimit artists once per artist and the
// result is cached under the artist id alone, so calls with any limit share it.
// Each call returns the first limit artists, or all of them when limit is 0.
// Fetched artists are auto-persisted to ensure valid database IDs.
func (uc *artistUseCase) ListSimilar(ctx context.Context, artistID string, limit int32) ([]*entity.Artist, error) {
	cacheKey := fmt.Sprintf("similar:%s", artistID)
	if cached := uc.cache.Get(cacheKey); cached != nil {
		if artists, ok := cached.([]*entity.Artist); ok {
			return firstN(artists, limit), nil
		}
	}

	// Cache miss - fetch artist and get similar artists
	artist, err := uc.artistRepo.Get(ctx, artistID)
	if err != nil {
		return nil, err
	}

	artists, err := uc.artistSearcher.ListSimilar(ctx, artist, similarLookupLimit)
	if err != nil {
		return nil, err
	}

	// Filter out entries with empty MBID.
	filtered := entity.FilterArtistsByMBID(artists)

	// Persist via read-then-write helper.
	persisted, err := uc.persistArtists(ctx, filtered)
	if err != nil {
		return nil, err
	}

	// Cache the full lookup so a later call with a larger limit is served too.
	uc.cache.Set(cacheKey, persisted)

	return firstN(persisted, limit), nil
}

// firstN returns the first n artists, or all of them when n is 0 or exceeds
// the length. The returned slice has its capacity clipped so a caller's append
// cannot write into the cached backing array.
func firstN(artists []*entity.Artist, n int32) []*entity.Artist {
	if n <= 0 || int(n) >= len(artists) {
		return artists[:len(artists):len(artists)]
	}
	return artists[:n:n]
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
	filtered := entity.FilterArtistsByMBID(artists)

	// Persist via read-then-write helper.
	persisted, err := uc.persistArtists(ctx, filtered)
	if err != nil {
		return nil, err
	}

	// Store in cache
	uc.cache.Set(cacheKey, persisted)

	return persisted, nil
}

// persistArtists looks up existing artists by MBID, creates only missing ones,
// and returns all artists in the original input order. All input artists must
// have a non-empty MBID (caller responsibility).
func (uc *artistUseCase) persistArtists(ctx context.Context, artists []*entity.Artist) ([]*entity.Artist, error) {
	if len(artists) == 0 {
		return []*entity.Artist{}, nil
	}

	ctx, span := otel.Tracer("usecase/artist").Start(ctx, "PersistArtists")
	defer span.End()
	span.SetAttributes(attribute.Int("persist.input_count", len(artists)))

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

			if err := uc.publisher.PublishEvent(ctx, entity.SubjectArtistCreated, entity.ArtistCreatedData{
				ArtistID:   a.ID,
				ArtistName: a.Name,
				MBID:       a.MBID,
			}); err != nil {
				uc.logger.Warn(ctx, "failed to publish artist.created event",
					slog.String("artist_id", a.ID), slog.Any("error", err))
			}
		}
	}

	span.SetAttributes(attribute.Int("persist.created_count", len(missing)))

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

// Get implements [ArtistUseCase].
func (uc *artistUseCase) Get(ctx context.Context, id string) (*entity.Artist, error) {
	return uc.artistRepo.Get(ctx, id)
}

// ListByOrganizer implements [ArtistUseCase].
func (uc *artistUseCase) ListByOrganizer(ctx context.Context, organizerID string) ([]*entity.Artist, error) {
	if _, err := uc.organizerRepo.Get(ctx, organizerID); err != nil {
		return nil, err
	}
	return uc.organizerRepo.ListArtists(ctx, organizerID)
}
