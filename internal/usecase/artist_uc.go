package usecase

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/pkg/analyticslog"
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

	// Refresh reads the artist again from the music catalog by its MBID. It
	// adopts the catalog's name when it is not empty and differs from
	// currentName, and then stores whether the catalog marks the artist's
	// activity as ended. An MBID the catalog does not know changes nothing and
	// succeeds, since reading it again cannot succeed.
	//
	// # Possible errors:
	//
	//   - Unavailable: the catalog is down or rate-limited.
	//   - NotFound: the artist does not exist.
	//   - Internal: unexpected failure.
	Refresh(ctx context.Context, artistID, mbid, currentName string) error

	// RefreshDue runs one daily refresh: it refreshes, one at a time, every
	// artist in today's slot of a 30-day rotation (the days from 1970-01-01 to
	// today in Japan time, modulo 30), so every artist is read again once every
	// 30 days. A failed artist does not stop the run, except that the run stops
	// after 3 consecutive failures; it also stops when ctx is done.
	//
	// # Possible errors:
	//
	//   - Internal: listing the slot's artists failed.
	RefreshDue(ctx context.Context) (ArtistRefreshSummary, error)
}

// ArtistRefreshSummary reports what one daily RefreshDue run did.
type ArtistRefreshSummary struct {
	// Slot is the rotation slot the run refreshed.
	Slot int
	// Listed is the number of artists in the slot.
	Listed int
	// Attempted is the number of artists the run tried to refresh.
	Attempted int
	// Ended is the number of refreshed artists the catalog marks as ended.
	Ended int
	// Renamed is the number of refreshed artists whose name changed.
	Renamed int
	// Failed is the number of attempted artists whose refresh failed.
	Failed int
	// CircuitBroken reports whether the run stopped after consecutive failures.
	CircuitBroken bool
}

// ArtistCacheTTL is how long ArtistUseCase reuses a catalog result (Search,
// ListSimilar, ListTop) before consulting the catalog again. Charts and
// catalog matches do not change materially within hours.
const ArtistCacheTTL = 6 * time.Hour

// similarLookupLimit is the number of similar artists ListSimilar asks the
// catalog for on a cache miss, whatever limit the caller requested. One
// cached lookup per artist then serves every limit up to it.
const similarLookupLimit = 50

const (
	// artistRefreshSlots is the number of days in the refresh rotation: each
	// daily run reads one slot, so every artist is read once per cycle.
	artistRefreshSlots = 30
	// artistRefreshMaxConsecutiveFailures stops a run after this many artists
	// fail in a row, treating it as a systemic catalog failure.
	artistRefreshMaxConsecutiveFailures = 3
)

// artistUseCase implements the ArtistUseCase interface.
type artistUseCase struct {
	artistRepo     entity.ArtistRepository
	organizerRepo  entity.OrganizerRepository
	artistSearcher entity.ArtistSearcher
	idManager      entity.ArtistIdentityManager
	cache          entity.Cache
	clock          Clock
	logger         *logging.Logger
}

// Compile-time interface compliance check
var _ ArtistUseCase = (*artistUseCase)(nil)

// NewArtistUseCase creates a new instance of the artist business logic handler.
// clock gives the day RefreshDue refreshes.
func NewArtistUseCase(
	artistRepo entity.ArtistRepository,
	organizerRepo entity.OrganizerRepository,
	artistSearcher entity.ArtistSearcher,
	idManager entity.ArtistIdentityManager,
	cache entity.Cache,
	clock Clock,
	logger *logging.Logger,
) ArtistUseCase {
	return &artistUseCase{
		artistRepo:     artistRepo,
		organizerRepo:  organizerRepo,
		artistSearcher: artistSearcher,
		idManager:      idManager,
		cache:          cache,
		clock:          clock,
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
// Each call leaves out the artists whose activity has ended and returns the
// first limit of the rest, or all of them when limit is 0.
// Fetched artists are auto-persisted to ensure valid database IDs.
func (uc *artistUseCase) ListSimilar(ctx context.Context, artistID string, limit int32) ([]*entity.Artist, error) {
	cacheKey := fmt.Sprintf("similar:%s", artistID)
	if cached := uc.cache.Get(cacheKey); cached != nil {
		if artists, ok := cached.([]*entity.Artist); ok {
			active, err := uc.withoutEndedNow(ctx, artists)
			if err != nil {
				return nil, err
			}
			return firstN(active, limit), nil
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
	// The cache keeps the ended artists: a later call reads their status again.
	uc.cache.Set(cacheKey, persisted)

	return firstN(withoutEnded(persisted), limit), nil
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

// ListTop retrieves popular artists, leaving out those whose activity has ended.
// Results are cached to reduce external API calls.
// Fetched artists are auto-persisted to ensure valid database IDs.
func (uc *artistUseCase) ListTop(ctx context.Context, country string, tag string, limit int32) ([]*entity.Artist, error) {
	// Check cache first
	cacheKey := fmt.Sprintf("top:%s:%s:%d", country, tag, limit)
	if cached := uc.cache.Get(cacheKey); cached != nil {
		if artists, ok := cached.([]*entity.Artist); ok {
			return uc.withoutEndedNow(ctx, artists)
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

	// Store in cache. The cache keeps the ended artists: a later call reads
	// their status again.
	uc.cache.Set(cacheKey, persisted)

	return withoutEnded(persisted), nil
}

// withoutEnded returns the artists whose activity has not ended, in order.
func withoutEnded(artists []*entity.Artist) []*entity.Artist {
	result := make([]*entity.Artist, 0, len(artists))
	for _, a := range artists {
		if !a.ActivityEnded {
			result = append(result, a)
		}
	}
	return result
}

// withoutEndedNow returns the artists of an earlier result whose activity has
// not ended by now, in order, reading their current status with one
// ListByMBIDs call. The earlier result's artists are returned as they were.
func (uc *artistUseCase) withoutEndedNow(ctx context.Context, artists []*entity.Artist) ([]*entity.Artist, error) {
	if len(artists) == 0 {
		return []*entity.Artist{}, nil
	}
	mbids := make([]string, len(artists))
	for i, a := range artists {
		mbids[i] = a.MBID
	}
	current, err := uc.artistRepo.ListByMBIDs(ctx, mbids)
	if err != nil {
		return nil, err
	}
	ended := make(map[string]bool, len(current))
	for _, a := range current {
		if a.ActivityEnded {
			ended[a.MBID] = true
		}
	}
	result := make([]*entity.Artist, 0, len(artists))
	for _, a := range artists {
		if !ended[a.MBID] {
			result = append(result, a)
		}
	}
	return result, nil
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
		// Artist.Create announces each artist it registered (ARTIST.created).
		for _, a := range created {
			existingSet[a.MBID] = a
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

// artistRefreshOutcome is what one Refresh changed.
type artistRefreshOutcome struct {
	renamed bool
	ended   bool
}

// Refresh implements [ArtistUseCase].
func (uc *artistUseCase) Refresh(ctx context.Context, artistID, mbid, currentName string) error {
	_, err := uc.refresh(ctx, artistID, mbid, currentName)
	return err
}

// refresh reads the artist from the catalog and stores its name and activity
// status, reporting what it found.
func (uc *artistUseCase) refresh(ctx context.Context, artistID, mbid, currentName string) (artistRefreshOutcome, error) {
	var outcome artistRefreshOutcome

	catalog, err := uc.idManager.GetArtist(ctx, mbid)
	switch {
	case errors.Is(err, apperr.ErrNotFound):
		uc.logger.Warn(ctx, "catalog has no artist for the MBID; leaving the artist unchanged", analyticslog.Attrs(analyticslog.TopicCatalogSync,
			slog.String("artist_id", artistID),
			slog.String("mbid", mbid),
		)...)
		return outcome, nil
	case err != nil:
		return outcome, fmt.Errorf("get artist %s from the catalog: %w", artistID, err)
	}

	switch {
	case catalog.Name == "":
		uc.logger.Warn(ctx, "catalog returned an empty name; keeping the current name",
			slog.String("artist_id", artistID),
			slog.String("mbid", mbid),
		)
	case catalog.Name != currentName:
		if err := uc.artistRepo.UpdateName(ctx, artistID, catalog.Name); err != nil {
			return outcome, fmt.Errorf("update name of artist %s: %w", artistID, err)
		}
		outcome.renamed = true
		uc.logger.Info(ctx, "artist name updated to canonical", analyticslog.Attrs(analyticslog.TopicCatalogSync,
			slog.String("artist_id", artistID),
			slog.String("old_name", currentName),
			slog.String("new_name", catalog.Name),
		)...)
	}

	if err := uc.artistRepo.UpdateActivityEnded(ctx, artistID, catalog.ActivityEnded); err != nil {
		return outcome, fmt.Errorf("update activity status of artist %s: %w", artistID, err)
	}
	outcome.ended = catalog.ActivityEnded
	return outcome, nil
}

// artistRefreshSlot returns the rotation slot of the day of now in Japan time:
// the whole days from 1970-01-01 to that day, modulo artistRefreshSlots.
func artistRefreshSlot(now time.Time) int {
	days := entity.JapanDate(now).Unix() / (24 * 60 * 60)
	return int(days % artistRefreshSlots)
}

// RefreshDue implements [ArtistUseCase].
func (uc *artistUseCase) RefreshDue(ctx context.Context) (ArtistRefreshSummary, error) {
	summary := ArtistRefreshSummary{Slot: artistRefreshSlot(uc.clock())}

	artists, err := uc.artistRepo.ListRefreshDue(ctx, artistRefreshSlots, summary.Slot)
	if err != nil {
		return summary, fmt.Errorf("list artists of refresh slot %d: %w", summary.Slot, err)
	}
	summary.Listed = len(artists)

	var consecutiveFailures int
	for _, artist := range artists {
		if ctx.Err() != nil {
			break
		}

		summary.Attempted++
		outcome, err := uc.refresh(ctx, artist.ID, artist.MBID, artist.Name)
		if err != nil {
			summary.Failed++
			consecutiveFailures++
			uc.logger.Error(ctx, "failed to refresh artist", err,
				slog.String("artist_id", artist.ID),
				slog.String("artist_name", artist.Name),
			)
			if consecutiveFailures >= artistRefreshMaxConsecutiveFailures {
				summary.CircuitBroken = true
				uc.logger.Error(ctx, "circuit breaker activated: stopping after consecutive failures", nil,
					slog.Int("consecutive_failures", consecutiveFailures),
				)
				break
			}
			continue
		}
		consecutiveFailures = 0
		if outcome.renamed {
			summary.Renamed++
		}
		if outcome.ended {
			summary.Ended++
		}
	}

	return summary, nil
}
