package usecase

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/liverty-music/backend/internal/entity"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
)

// ConcertUseCase defines the interface for concert-related business logic.
type ConcertUseCase interface {
	// ListByArtist returns all concerts for a specific artist.
	//
	// # Possible errors
	//
	//  - NotFound: If the artist does not exist.
	//  - Internal: database query failure.
	ListByArtist(ctx context.Context, artistID string) ([]*entity.Concert, error)

	// ListByFollower returns all concerts for artists followed by the given user.
	//
	// # Possible errors
	//
	//  - NotFound: If the user does not exist.
	ListByFollower(ctx context.Context, userID string) ([]*entity.Concert, error)

	// ListByFollowerGrouped returns concerts for followed artists, grouped by date
	// and classified into home/nearby/away lanes based on proximity to the user's home.
	//
	// # Possible errors
	//
	//  - NotFound: If the user does not exist.
	ListByFollowerGrouped(ctx context.Context, userID string, home *entity.Home) ([]*entity.ProximityGroup, error)

	// ListWithProximity returns concerts for the specified artists, grouped by date
	// and classified by proximity to the given home.
	//
	// # Possible errors
	//
	//  - Internal: database query failure.
	ListWithProximity(ctx context.Context, artistIDs []string, home *entity.Home) ([]*entity.ProximityGroup, error)

	// SearchNewConcerts discovers new concerts for the given artist synchronously.
	// It returns the newly discovered concerts after deduplication against
	// already-known upcoming events.
	//
	// # Possible errors
	//
	//  - NotFound: If the artist does not exist.
	//  - Internal: search or database failure.
	SearchNewConcerts(ctx context.Context, artistID string) ([]*entity.Concert, error)
}

// concertUseCase implements the ConcertUseCase interface.
type concertUseCase struct {
	artistRepo       entity.ArtistRepository
	concertRepo      entity.ConcertRepository
	venueRepo        entity.VenueRepository
	searchLogRepo    entity.SearchLogRepository
	concertSearcher  entity.ConcertSearcher
	centroidResolver CentroidResolver
	publisher        EventPublisher
	metrics          ConcertMetrics
	// searchCacheTTL is how long a completed search is reused before a repeat
	// external call is allowed. Configured per environment (prod runs longer).
	searchCacheTTL time.Duration
	// discoveryWindow is how long after a successful discovery the external
	// search is skipped, since announcements arrive in batches then go quiet.
	discoveryWindow time.Duration
	logger          *logging.Logger
}

// pendingTimeout is the maximum age of a pending search log before it is
// considered stale and treated as failed (self-healing for crashed workers).
const pendingTimeout = 3 * time.Minute

// statusUpdateTimeout is the context timeout for search log status updates.
// Uses a fresh context.Background() to ensure updates succeed even when the
// parent context (Gemini API call) has expired.
const statusUpdateTimeout = 5 * time.Second

// Compile-time interface compliance check
var _ ConcertUseCase = (*concertUseCase)(nil)

// NewConcertUseCase creates a new concert use case.
// It orchestrates concert searching, retrieval, and event publishing.
func NewConcertUseCase(
	artistRepo entity.ArtistRepository,
	concertRepo entity.ConcertRepository,
	venueRepo entity.VenueRepository,
	searchLogRepo entity.SearchLogRepository,
	concertSearcher entity.ConcertSearcher,
	centroidResolver CentroidResolver,
	publisher EventPublisher,
	metrics ConcertMetrics,
	searchCacheTTL time.Duration,
	discoveryWindow time.Duration,
	logger *logging.Logger,
) ConcertUseCase {
	return &concertUseCase{
		artistRepo:       artistRepo,
		concertRepo:      concertRepo,
		venueRepo:        venueRepo,
		searchLogRepo:    searchLogRepo,
		concertSearcher:  concertSearcher,
		centroidResolver: centroidResolver,
		publisher:        publisher,
		metrics:          metrics,
		searchCacheTTL:   searchCacheTTL,
		discoveryWindow:  discoveryWindow,
		logger:           logger,
	}
}

// ListByArtist returns all concerts for a specific artist.
func (uc *concertUseCase) ListByArtist(ctx context.Context, artistID string) ([]*entity.Concert, error) {
	concerts, err := uc.concertRepo.ListByArtist(ctx, artistID, false)
	if err != nil {
		return nil, err
	}

	return concerts, nil
}

// ListByFollower returns all concerts for artists followed by the given user.
func (uc *concertUseCase) ListByFollower(ctx context.Context, userID string) ([]*entity.Concert, error) {
	return uc.concertRepo.ListByFollower(ctx, userID)
}

// ListByFollowerGrouped returns concerts for followed artists, grouped by date
// and classified into home/nearby/away lanes based on proximity to the user's home.
func (uc *concertUseCase) ListByFollowerGrouped(ctx context.Context, userID string, home *entity.Home) ([]*entity.ProximityGroup, error) {
	concerts, err := uc.concertRepo.ListByFollower(ctx, userID)
	if err != nil {
		return nil, err
	}

	return entity.GroupByDateAndProximity(concerts, home), nil
}

// ListWithProximity returns concerts for the specified artists, grouped by date
// and classified by proximity to the given home.
func (uc *concertUseCase) ListWithProximity(ctx context.Context, artistIDs []string, home *entity.Home) ([]*entity.ProximityGroup, error) {
	if home != nil && home.Centroid == nil {
		if lat, lng, err := uc.centroidResolver.ResolveCentroid(home); err == nil {
			home.Centroid = &entity.Coordinates{Latitude: lat, Longitude: lng}
		}
	}

	concerts, err := uc.concertRepo.ListByArtists(ctx, artistIDs)
	if err != nil {
		return nil, fmt.Errorf("list concerts by artists: %w", err)
	}

	return entity.GroupByDateAndProximity(concerts, home), nil
}

// SearchNewConcerts discovers new concerts for the given artist synchronously.
// It returns the newly discovered concerts after deduplication against
// already-known upcoming events.
func (uc *concertUseCase) SearchNewConcerts(ctx context.Context, artistID string) ([]*entity.Concert, error) {
	// Check search log — skip if recently completed or currently pending.
	searchLog, err := uc.searchLogRepo.GetByArtistID(ctx, artistID)
	if err != nil && !errors.Is(err, apperr.ErrNotFound) {
		return nil, fmt.Errorf("failed to get search log: %w", err)
	}
	if searchLog != nil {
		now := time.Now()
		if searchLog.IsFresh(now, uc.searchCacheTTL) {
			uc.logger.Debug(ctx, "skipping external search, recently searched",
				slog.String("artist_id", artistID),
				slog.Time("search_time", searchLog.SearchTime),
			)
			return nil, nil
		}
		// Skip even when the last search is stale if a new concert was found
		// recently: announcements arrive in batches then go quiet, so a repeat
		// search would just re-find the same events and dedup to nothing.
		if searchLog.WasRecentlyDiscovered(now, uc.discoveryWindow) {
			uc.logger.Debug(ctx, "skipping external search, recently discovered new concert",
				slog.String("artist_id", artistID),
				slog.Time("last_found_at", searchLog.LastFoundTime),
			)
			return nil, nil
		}
		if searchLog.IsPending(now, pendingTimeout) {
			uc.logger.Debug(ctx, "skipping external search, already pending",
				slog.String("artist_id", artistID),
			)
			return nil, nil
		}
	}

	// Mark as pending. If this fails, abort — without a pending row,
	// downstream UpdateStatus calls silently no-op (0 rows affected).
	if err := uc.searchLogRepo.Upsert(ctx, artistID, entity.SearchLogStatusPending); err != nil {
		return nil, fmt.Errorf("failed to mark search as pending: %w", err)
	}

	return uc.executeSearch(ctx, artistID)
}

// executeSearch performs the actual Gemini search, deduplication, and event publishing.
// It returns the newly discovered concerts and updates the search log status on exit.
//
// Deduplication uses `(local_event_date, listed_venue_name)` matching via
// `entity.ScrapedConcerts.FilterNew`, aligned with the DB-level natural key
// `UNIQUE (series_id, local_event_date, venue_id)` enforced by the events
// table (added in migration `20260523145447_add_series_hierarchy`). The pre-
// v0.41.0 per-artist constraint `(artist_id, local_event_date)` was dropped
// in that migration alongside the singular events.artist_id column.
//
// The application-level FilterNew check on `(date, listed_venue_name)` avoids
// unnecessary publish/UPSERT round-trips for re-scrapes; the DB natural key
// is the source of truth and uses the resolved `venue_id` instead of the raw
// listed name, so the application key is a best-effort upstream filter.
func (uc *concertUseCase) executeSearch(ctx context.Context, artistID string) (_ []*entity.Concert, err error) {
	defer func() {
		if err != nil {
			uc.markSearchFailed(ctx, artistID)
			uc.metrics.RecordConcertSearch(ctx, "error")
		} else {
			uc.markSearchCompleted(ctx, artistID)
			uc.metrics.RecordConcertSearch(ctx, "success")
		}
	}()

	// Get Artist
	artist, err := uc.artistRepo.Get(ctx, artistID)
	if err != nil {
		return nil, fmt.Errorf("failed to get artist: %w", err)
	}

	// Guard before the Gemini call: an artist row with empty Name or MBID
	// is a data-integrity problem the discovery pipeline can't recover
	// from — both fields are required for the proto response (ArtistName
	// min_len=1, Mbid uuid format) AND for the consumer-side performer
	// link to validate. Fail fast here so we don't burn a Gemini quota
	// unit (and re-burn it on every subsequent retry — markSearchFailed
	// sets the log to "failed", which the IsFresh/IsPending skip check
	// doesn't catch, so the next CronJob tick re-enters executeSearch
	// unconditionally). An admin fix to the artist row is the only valid
	// recovery; surfacing Internal here keeps the error visible in logs.
	if artist.Name == "" || artist.MBID == "" {
		return nil, apperr.New(codes.Internal,
			"artist is missing required fields for discovery",
			slog.String("artist_id", artistID),
			slog.Bool("name_empty", artist.Name == ""),
			slog.Bool("mbid_empty", artist.MBID == ""),
		)
	}

	// Get Official Site — missing site is not an error; search continues with nil
	site, err := uc.artistRepo.GetOfficialSite(ctx, artistID)
	if err != nil && !errors.Is(err, apperr.ErrNotFound) {
		return nil, fmt.Errorf("failed to get official site: %w", err)
	}
	if errors.Is(err, apperr.ErrNotFound) {
		site = nil
		err = nil
	}

	// Get existing upcoming concerts for deduplication
	existing, err := uc.concertRepo.ListByArtist(ctx, artistID, true)
	if err != nil {
		return nil, fmt.Errorf("failed to list existing concerts: %w", err)
	}

	// Search new concerts via external API (deadline inherited from HandlerTimeout)
	scraped, err := uc.concertSearcher.Search(ctx, artist, site, time.Now())
	if err != nil {
		return nil, fmt.Errorf("failed to search concerts via external API: %w", err)
	}

	// Deduplicate: one event per artist per date — CPU-bound O(n) set operations.
	_, filterSpan := otel.Tracer("usecase/concert").Start(ctx, "FilterNewConcerts")
	newScraped := entity.ScrapedConcerts(scraped).FilterNew(existing)
	filterSpan.SetAttributes(
		attribute.Int("filter.scraped_count", len(scraped)),
		attribute.Int("filter.new_count", len(newScraped)),
	)
	filterSpan.End()

	if filtered := len(scraped) - len(newScraped); filtered > 0 {
		uc.logger.Debug(ctx, "filtered existing/duplicate events (same date)",
			slog.String("artist_id", artistID),
			slog.Int("filtered_count", filtered),
		)
	}

	// Publish concert.discovered.v1 event (artist-level batch)
	if len(newScraped) == 0 {
		uc.logger.Debug(ctx, "no new concerts after deduplication",
			slog.String("artist_id", artistID),
		)
		return nil, nil
	}

	// Note: the artist.Name / MBID guard fires at the top of executeSearch
	// (right after artistRepo.Get) so the Gemini call is never reached for
	// data-quality failures. By the time we get here both fields are
	// guaranteed non-empty.

	eventData := entity.ConcertDiscoveredData{
		ArtistID:   artistID,
		ArtistName: artist.Name,
		Concerts:   newScraped,
	}

	if err := uc.publisher.PublishEvent(ctx, entity.SubjectConcertDiscovered, eventData); err != nil {
		uc.logger.Error(ctx, "failed to publish concert.discovered event", err,
			slog.String("artist_id", artistID),
			slog.Int("concert_count", len(newScraped)),
		)
		// Non-fatal: CronJob will re-discover on next run.
		// The defer will call markSearchFailed because err != nil.
		return nil, err
	}

	uc.logger.Info(ctx, "published concert.discovered event",
		slog.String("artist_id", artistID),
		slog.String("artist_name", artist.Name),
		slog.Int("concert_count", len(newScraped)),
	)

	// Record the discovery so the discoveryWindow skip suppresses redundant
	// re-searches until the next announcement cycle is likely.
	uc.markSearchFound(ctx, artistID)

	// Build Concert entities from the deduplicated scraped data to return to the caller.
	// Event / Venue IDs stay empty because the search path returns concerts for
	// immediate display rather than persistence. The series ID is generated
	// (UUIDv7) on the fly so the embedded SeriesId carries a valid UUID and
	// passes the response-side protovalidate guards; the synthetic ID has no
	// referent in the DB and is discarded by the client after rendering.

	concerts := make([]*entity.Concert, 0, len(newScraped))
	for _, s := range newScraped {
		syntheticSeriesID, err := uuid.NewV7()
		if err != nil {
			return nil, fmt.Errorf("generate synthetic series ID for search response: %w", err)
		}
		// Search-path Concerts are display-only DTOs (never persisted); the
		// SeriesType is cosmetic here, so SINGLE is a safe default.
		c := s.ToConcert(artistID, syntheticSeriesID.String(), "", "", entity.SeriesTypeSingle)
		// Replace ToConcert's id-only Performer shell with the resolved
		// Artist entity so the response carries a complete performer with
		// Name and MBID (validated non-empty by the guard above).
		c.Performers = []*entity.Artist{artist}
		concerts = append(concerts, c)
	}
	return concerts, nil
}

// markSearchCompleted updates the search log status to completed.
// It uses context.WithoutCancel to detach from the parent's deadline while
// preserving trace context for span correlation.
func (uc *concertUseCase) markSearchCompleted(ctx context.Context, artistID string) {
	updateCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), statusUpdateTimeout)
	defer cancel()

	if err := uc.searchLogRepo.UpdateStatus(updateCtx, artistID, entity.SearchLogStatusCompleted); err != nil {
		uc.logger.Error(ctx, "failed to mark search as completed", err, slog.String("artist_id", artistID))
	}
}

// markSearchFound records that the search discovered at least one new concert.
// Failure is non-fatal and only logged: the discovery event was already
// published, so a missed last_found_at update merely lets the next CronJob tick
// re-search this artist sooner than the discoveryWindow would otherwise allow.
func (uc *concertUseCase) markSearchFound(ctx context.Context, artistID string) {
	updateCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), statusUpdateTimeout)
	defer cancel()

	if err := uc.searchLogRepo.MarkFound(updateCtx, artistID); err != nil {
		uc.logger.Error(ctx, "failed to mark search as found", err, slog.String("artist_id", artistID))
	}
}

// markSearchFailed updates the search log status to failed.
// It uses context.WithoutCancel to detach from the parent's deadline while
// preserving trace context for span correlation.
func (uc *concertUseCase) markSearchFailed(ctx context.Context, artistID string) {
	updateCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), statusUpdateTimeout)
	defer cancel()

	if err := uc.searchLogRepo.UpdateStatus(updateCtx, artistID, entity.SearchLogStatusFailed); err != nil {
		uc.logger.Error(ctx, "failed to mark search as failed", err, slog.String("artist_id", artistID))
	}
}
