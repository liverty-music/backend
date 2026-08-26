package usecase

import (
	"context"
	"errors"
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

// ConcertUseCase defines the interface for concert-related business logic.
type ConcertUseCase interface {
	// ListByArtist returns all concerts for a specific artist.
	//
	// # Possible errors
	//
	//  - NotFound: If the artist does not exist.
	//  - Internal: database query failure.
	ListByArtist(ctx context.Context, artistID string) ([]*entity.Concert, error)

	// ListByFollower returns concerts for artists followed by the given user whose
	// local event date is on or after from. A nil from defaults to today onward.
	//
	// # Possible errors
	//
	//  - NotFound: If the user does not exist.
	ListByFollower(ctx context.Context, userID string, from *time.Time) ([]*entity.Concert, error)

	// ListByFollowerGrouped returns concerts for followed artists, grouped by date
	// and classified into home/nearby/away lanes based on proximity to the user's home.
	// Only concerts whose local event date is on or after from are included; a nil
	// from defaults to today onward.
	//
	// # Possible errors
	//
	//  - NotFound: If the user does not exist.
	ListByFollowerGrouped(ctx context.Context, userID string, home *entity.Home, from *time.Time) ([]*entity.ProximityGroup, error)

	// ListByArtists returns concerts for the specified artists, grouped by date
	// and classified by proximity to the given home.
	//
	// # Possible errors
	//
	//  - Internal: database query failure.
	ListByArtists(ctx context.Context, artistIDs []string, home *entity.Home) ([]*entity.ProximityGroup, error)

	// ListByLocation returns all concerts near the given reference point whose
	// local_event_date falls within [from, to], grouped by date and classified by
	// proximity. Only HOME and NEARBY concerts are returned; AWAY-only date groups
	// are omitted entirely.
	//
	// # Possible errors
	//
	//  - InvalidArgument: If the date range exceeds the maximum window.
	//  - Internal: database query failure.
	ListByLocation(ctx context.Context, location *entity.GeoLocation, from, to time.Time) ([]*entity.ProximityGroup, error)

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

// concertUseCase implements both the consumer-facing ConcertUseCase and the
// admin-facing AdminConcertUseCase. A single implementation keeps concert read
// logic in one place; the two interfaces segregate capabilities so the consumer
// handler never sees the admin operations (approve/reject/delete).
type concertUseCase struct {
	artistRepo          entity.ArtistRepository
	concertRepo         entity.ConcertRepository
	venueRepo           entity.VenueRepository
	seriesRepo          entity.SeriesRepository
	organizerRepo       entity.OrganizerRepository
	searchLogRepo       entity.SearchLogRepository
	stagedConcertRepo   entity.StagedConcertRepository
	rejectedConcertRepo entity.RejectedConcertLogRepository
	concertSearcher     entity.ConcertSearcher
	centroidResolver    CentroidResolver
	publisher           EventPublisher
	metrics             ConcertMetrics
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
var (
	_ ConcertUseCase      = (*concertUseCase)(nil)
	_ AdminConcertUseCase = (*concertUseCase)(nil)
)

// NewConcertUseCase creates a new concert use case.
// It orchestrates concert searching, retrieval, event publishing, and the
// admin approval/management operations (approve, reject, list, delete).
func NewConcertUseCase(
	artistRepo entity.ArtistRepository,
	concertRepo entity.ConcertRepository,
	venueRepo entity.VenueRepository,
	seriesRepo entity.SeriesRepository,
	organizerRepo entity.OrganizerRepository,
	searchLogRepo entity.SearchLogRepository,
	stagedConcertRepo entity.StagedConcertRepository,
	rejectedConcertRepo entity.RejectedConcertLogRepository,
	concertSearcher entity.ConcertSearcher,
	centroidResolver CentroidResolver,
	publisher EventPublisher,
	metrics ConcertMetrics,
	searchCacheTTL time.Duration,
	discoveryWindow time.Duration,
	logger *logging.Logger,
) *concertUseCase {
	return &concertUseCase{
		artistRepo:          artistRepo,
		concertRepo:         concertRepo,
		venueRepo:           venueRepo,
		seriesRepo:          seriesRepo,
		organizerRepo:       organizerRepo,
		searchLogRepo:       searchLogRepo,
		stagedConcertRepo:   stagedConcertRepo,
		rejectedConcertRepo: rejectedConcertRepo,
		concertSearcher:     concertSearcher,
		centroidResolver:    centroidResolver,
		publisher:           publisher,
		metrics:             metrics,
		searchCacheTTL:      searchCacheTTL,
		discoveryWindow:     discoveryWindow,
		logger:              logger,
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

// ListByFollower returns concerts for artists followed by the given user whose
// local event date is on or after from (nil defaults to today onward).
func (uc *concertUseCase) ListByFollower(ctx context.Context, userID string, from *time.Time) ([]*entity.Concert, error) {
	return uc.concertRepo.ListByFollower(ctx, userID, from)
}

// ListByFollowerGrouped returns concerts for followed artists, grouped by date
// and classified into home/nearby/away lanes based on proximity to the user's home.
// A nil from defaults to today onward; a past from widens the range into the past.
func (uc *concertUseCase) ListByFollowerGrouped(ctx context.Context, userID string, home *entity.Home, from *time.Time) ([]*entity.ProximityGroup, error) {
	concerts, err := uc.concertRepo.ListByFollower(ctx, userID, from)
	if err != nil {
		return nil, err
	}

	return entity.GroupByDateAndProximity(concerts, home), nil
}

// ListByArtists returns concerts for the specified artists, grouped by date
// and classified by proximity to the given home.
func (uc *concertUseCase) ListByArtists(ctx context.Context, artistIDs []string, home *entity.Home) ([]*entity.ProximityGroup, error) {
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

// maxLocationDateRange is the maximum span of a ListByLocation date range. The
// proto message-level CEL constraint only enforces from <= to (protovalidate CEL
// has no calendar-day arithmetic for google.type.Date), so the 30-day cap is
// enforced here in the use case.
const maxLocationDateRange = 30 * 24 * time.Hour

// ListByLocation returns all concerts near the given reference point within
// [from, to], grouped by date and classified by proximity. It classifies via the
// shared GroupByDateAndProximity using a transient Home adapted from the reference
// point, then strips AWAY-only date groups so only HOME/NEARBY concerts surface.
func (uc *concertUseCase) ListByLocation(ctx context.Context, location *entity.GeoLocation, from, to time.Time) ([]*entity.ProximityGroup, error) {
	if to.Sub(from) > maxLocationDateRange {
		return nil, apperr.New(codes.InvalidArgument,
			"date range must not exceed 30 days",
			slog.Time("from", from),
			slog.Time("to", to),
		)
	}

	concerts, err := uc.concertRepo.ListByLocation(ctx, location, from, to)
	if err != nil {
		return nil, fmt.Errorf("list concerts by location: %w", err)
	}

	// Adapt the reference point into a transient Home so the shared proximity
	// classifier can run; then drop date groups that ended up with no HOME or
	// NEARBY concerts (the bounding-box pre-filter admits venues up to ~200 km+
	// away that the Haversine cut then reclassifies as AWAY).
	groups := entity.GroupByDateAndProximity(concerts, location.AsHome())

	nearby := groups[:0]
	for _, g := range groups {
		if len(g.Home)+len(g.Nearby) == 0 {
			continue
		}
		// AWAY is never surfaced in the All Nearby view; clear it so the response
		// mapper cannot leak beyond-200 km venues that shared the bounding box.
		g.Away = nil
		nearby = append(nearby, g)
	}
	return nearby, nil
}

// SearchNewConcerts discovers new concerts for the given artist synchronously.
// It returns the newly discovered concerts after deduplication against
// already-known upcoming events.
func (uc *concertUseCase) SearchNewConcerts(ctx context.Context, artistID string) ([]*entity.Concert, error) {
	// Discovery exclusion: skip Gemini search entirely for artists whose
	// concerts are managed directly by an active organizer. The organizer
	// publishes their concerts via the authoring surface; a background
	// re-discovery would only produce noisy duplicate-detection conflicts and
	// consume external API quota for no net gain. When the organizer is
	// deactivated, IsArtistRepresentedByActiveOrganizer returns false and
	// discovery resumes automatically.
	if uc.organizerRepo != nil {
		represented, err := uc.organizerRepo.IsArtistRepresentedByActiveOrganizer(ctx, artistID)
		if err != nil {
			// Non-fatal: log and proceed rather than silently blocking discovery.
			uc.logger.Warn(ctx, "failed to check organizer representation for discovery exclusion",
				slog.String("artist_id", artistID),
				slog.Any("error", err),
			)
		} else if represented {
			uc.logger.Debug(ctx, "skipping discovery for organizer-represented artist",
				slog.String("artist_id", artistID),
			)
			return nil, nil
		}
	}

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
// `entity.FilterNewSeries`, aligned with the DB-level natural key
// `UNIQUE (series_id, local_event_date, venue_id)` enforced by the events
// table (added in migration `20260523145447_add_series_hierarchy`). The pre-
// v0.41.0 per-artist constraint `(artist_id, local_event_date)` was dropped
// in that migration alongside the singular events.artist_id column.
//
// The application-level FilterNewSeries check on `(date, listed_venue_name)` avoids
// unnecessary publish/UPSERT round-trips for re-scrapes; the DB natural key
// is the source of truth and uses the resolved `venue_id` instead of the raw
// listed name, so the application key is a best-effort upstream filter.
func (uc *concertUseCase) executeSearch(ctx context.Context, artistID string) (result []*entity.Concert, err error) {
	defer func() {
		switch {
		case err != nil:
			uc.markSearchFailed(ctx, artistID)
			uc.metrics.RecordConcertSearch(ctx, "error")
		case len(result) == 0:
			// A healthy run that discovered nothing new — distinct from a
			// fruitful run so pipeline-health views can spot quota burned
			// for no yield.
			uc.markSearchCompleted(ctx, artistID)
			uc.metrics.RecordConcertSearch(ctx, "zero_results")
		default:
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

	// Get existing upcoming concerts for deduplication.
	existing, err := uc.concertRepo.ListByArtist(ctx, artistID, true)
	if err != nil {
		return nil, fmt.Errorf("failed to list existing concerts: %w", err)
	}

	// Fetch pending staged rows for this artist so that already-staged concerts
	// are not re-staged. The rejection log is intentionally NOT consulted here:
	// a rejected concert can always re-enter the queue on a subsequent run.
	pendingKeys, err := uc.stagedConcertRepo.ListPendingDedupKeysByArtist(ctx, artistID)
	if err != nil {
		return nil, fmt.Errorf("failed to list pending staged concert keys: %w", err)
	}

	// Search new concerts via external API (deadline inherited from HandlerTimeout).
	// The searcher returns discovered concerts already grouped by series.
	scraped, err := uc.concertSearcher.Search(ctx, artist, site, time.Now())
	if err != nil {
		return nil, fmt.Errorf("failed to search concerts via external API: %w", err)
	}
	scrapedEvents := entity.SumSeriesEvents(scraped)

	// Deduplicate against published concerts, per event while preserving grouping.
	_, filterSpan := otel.Tracer("usecase/concert").Start(ctx, "FilterNewConcerts")
	newSeries := entity.FilterNewSeries(scraped, existing)
	newEvents := entity.SumSeriesEvents(newSeries)
	filterSpan.SetAttributes(
		attribute.Int("filter.scraped_count", scrapedEvents),
		attribute.Int("filter.new_count", newEvents),
	)
	filterSpan.End()

	// Further deduplicate against pending staged rows. The staged dedup key uses
	// (local_date, normalized listed_venue_name), matching FilterNewSeries'
	// semantics: the venue name is compared through entity.NormalizeVenueName on
	// both sides so name drift across runs does not re-stage an already-pending
	// concert. Events pending review are dropped; a series emptied by that drop
	// is removed entirely.
	if len(pendingKeys) > 0 {
		pendingSet := make(map[string]bool, len(pendingKeys))
		for _, k := range pendingKeys {
			pendingSet[k.LocalDate.Format("2006-01-02")+"|"+entity.NormalizeVenueName(k.ListedVenueName)] = true
		}
		kept := newSeries[:0]
		for _, s := range newSeries {
			evs := s.Events[:0]
			for _, ev := range s.Events {
				if pendingSet[ev.LocalDate.Format("2006-01-02")+"|"+entity.NormalizeVenueName(ev.ListedVenueName)] {
					continue
				}
				evs = append(evs, ev)
			}
			if len(evs) == 0 {
				continue
			}
			s.Events = evs
			kept = append(kept, s)
		}
		newSeries = kept
		newEvents = entity.SumSeriesEvents(newSeries)
	}

	if filtered := scrapedEvents - newEvents; filtered > 0 {
		uc.logger.Debug(ctx, "filtered existing/duplicate events (same date)",
			slog.String("artist_id", artistID),
			slog.Int("filtered_count", filtered),
		)
	}

	// Publish concert.discovered.v1 event (artist-level batch)
	if newEvents == 0 {
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
		Series:     newSeries,
	}

	if err := uc.publisher.PublishEvent(ctx, entity.SubjectConcertDiscovered, eventData); err != nil {
		uc.logger.Error(ctx, "failed to publish concert.discovered event", err,
			slog.String("artist_id", artistID),
			slog.Int("concert_count", newEvents),
		)
		// Non-fatal: CronJob will re-discover on next run.
		// The defer will call markSearchFailed because err != nil.
		return nil, err
	}

	uc.logger.Info(ctx, "published concert.discovered event",
		slog.String("artist_id", artistID),
		slog.String("artist_name", artist.Name),
		slog.Int("series_count", len(newSeries)),
		slog.Int("concert_count", newEvents),
	)

	// Record the discovery so the discoveryWindow skip suppresses redundant
	// re-searches until the next announcement cycle is likely.
	uc.markSearchFound(ctx, artistID)

	// Build Concert entities from the deduplicated series to return to the caller.
	// Event / Venue IDs stay empty because the search path returns concerts for
	// immediate display rather than persistence. One synthetic series ID (UUIDv7)
	// is minted per discovered series and shared across its events so the embedded
	// SeriesId carries a valid UUID and passes the response-side protovalidate
	// guards; the synthetic ID has no referent in the DB and is discarded by the
	// client after rendering.
	concerts := make([]*entity.Concert, 0, newEvents)
	for _, s := range newSeries {
		syntheticSeriesID := entity.NewID()
		for _, ev := range s.Events {
			c := s.ToConcert(ev, artistID, syntheticSeriesID, "", "")
			// Replace ToConcert's id-only Performer shell with the resolved
			// Artist entity so the response carries a complete performer with
			// Name and MBID (validated non-empty by the guard above).
			c.Performers = []*entity.Artist{artist}
			concerts = append(concerts, c)
		}
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
