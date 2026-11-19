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
	// List returns every published concert (no audience filter), each carrying
	// its event id and performers, for catalog review and management.
	//
	// # Possible errors
	//
	//  - Internal: database query failure.
	List(ctx context.Context) ([]*entity.Concert, error)

	// ListCatalogByArtist returns every published concert the artist performs
	// in (no audience filter: CANCELLED and UNLISTED included), for an admin
	// to manage that artist's concerts.
	//
	// # Possible errors
	//
	//  - InvalidArgument: no artist is given.
	//  - Internal: database query failure.
	ListCatalogByArtist(ctx context.Context, artistID string) ([]*entity.Concert, error)

	// Delete permanently removes a published concert by its event id with the
	// records that depend only on it (see [entity.ConcertRepository.Delete]
	// for the exact set); nothing is recorded about its slot. It is idempotent: deleting an id that no longer exists succeeds.
	//
	// # Possible errors
	//
	//  - InvalidArgument: If the event id is empty or malformed.
	//  - FailedPrecondition: If the event has a Paid Order, a Settlement that
	//    is not Reversed, or a purchase in progress.
	//  - Internal: If the delete fails.
	Delete(ctx context.Context, eventID string) error

	// ListByArtist returns the artist's Concerts of every date that are
	// visible to fans, the earliest first (Concert.ListByArtist). An artist
	// with no Concerts, and an artist that does not exist, give an empty
	// list, not an error.
	//
	// # Possible errors
	//
	//  - InvalidArgument: no artist id is given.
	//  - Internal: database query failure.
	ListByArtist(ctx context.Context, artistID string) ([]*entity.Concert, error)

	// ListFollowed returns the fan-visible Concerts of the artists the user
	// follows whose local date is on or after from (Concert.ListByFollower),
	// grouped by date and sorted into home, nearby and away lists by
	// proximity to home. A nil from means the current date onward. A user
	// who follows no artist, and a user that does not exist, give no groups,
	// not an error.
	//
	// # Possible errors
	//
	//  - Internal: database query failure.
	ListFollowed(ctx context.Context, userID string, home *entity.Home, from *time.Time) ([]*entity.ProximityGroup, error)

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
	// already-known upcoming events. It returns no concerts, without an
	// external search, for an artist whose activity has ended.
	//
	// trigger names what started the run; it is reported with the run's
	// outcome when the external search is performed.
	//
	// # Possible errors
	//
	//  - NotFound: If the artist does not exist.
	//  - Internal: search or database failure.
	SearchNewConcerts(ctx context.Context, artistID string, trigger SearchTrigger) ([]*entity.Concert, error)

	// SearchNewConcertsOnFirstFollow triggers concert discovery for an artist
	// the very first time it is followed by anyone, and is a no-op on every
	// later follow. It is the discovery trigger for the FOLLOW.created event:
	// the event consumer decodes the message and calls this method so the
	// Follow use case never depends on concert discovery.
	//
	// "First follow" is determined by the absence of a ConcertSearch row
	// (ConcertSearch.GetByArtistID NotFound); once SearchNewConcerts marks the
	// artist as searched, subsequent calls for the same artist are a no-op
	// here. When several fans follow the same artist concurrently, each
	// publishes its own FOLLOW.created event and this method may run more
	// than once for the artist, but SearchNewConcerts's own dedup (against
	// already-known concerts and pending concert candidates) and the DB natural key
	// on events guarantee any discovered concerts are stored only once.
	//
	// # Possible errors
	//
	//  - Internal: concert search lookup or search failure.
	SearchNewConcertsOnFirstFollow(ctx context.Context, artistID string) error

	// Get returns the Concert of one Event when that Event has an event page
	// (see [entity.Event.HasEventPage]). The returned Concert carries its own
	// publish state and its Series as every Concert read returns it, including
	// its description, cover image, visibility and publish state. No signed-in
	// caller is needed.
	//
	// # Possible errors
	//
	//  - NotFound: If no Concert has the id, or its Event has no event page.
	//    The same error is returned in every case so a caller cannot learn
	//    whether a DRAFT or UNLISTED event exists.
	//  - Any other repository error, returned unchanged.
	Get(ctx context.Context, eventID string) (*entity.Concert, error)

	// ListBySeries returns every Concert of one Series that has an event page,
	// ordered by local date and then by start time (as
	// ConcertRepository.ListEventsBySeries orders them). Each Concert carries
	// its Series as every Concert read returns it. No signed-in caller is
	// needed.
	//
	// # Possible errors
	//
	//  - NotFound: If no Series has the id, or the Series has no event page.
	//  - Any other repository error, returned unchanged.
	ListBySeries(ctx context.Context, seriesID string) ([]*entity.Concert, error)
}

// errNoEventPage is the single NotFound returned by Get and ListBySeries for
// every miss, so the response never reveals why an event has no page.
func errNoEventPage() error {
	return apperr.New(codes.NotFound, "event page not found")
}

// concertUseCase implements ConcertUseCase; concertCandidateUseCase builds on
// it, so concert read logic stays in one place.
type concertUseCase struct {
	artistRepo           entity.ArtistRepository
	concertRepo          entity.ConcertRepository
	venueRepo            entity.VenueRepository
	seriesRepo           entity.SeriesRepository
	organizerRepo        entity.OrganizerRepository
	concertSearchRepo    entity.ConcertSearchRepository
	concertCandidateRepo entity.ConcertCandidateRepository
	rejectedConcertRepo  entity.RejectedConcertRepository
	concertSearcher      entity.ConcertSearcher
	centroidResolver     CentroidResolver
	workflows            Workflows
	metrics              ConcertMetrics
	// analytics receives the concert.search.completed signal; nil sends none.
	analytics AnalyticsClient
	// searchCacheTTL is how long a completed search is reused before a repeat
	// external call is allowed. Configured per environment (prod runs longer).
	searchCacheTTL time.Duration
	// discoveryWindow is how long after a successful discovery the external
	// search is skipped, since announcements arrive in batches then go quiet.
	discoveryWindow time.Duration
	clock           Clock
	logger          *logging.Logger
}

// pendingTimeout is the maximum age of a pending concert search before it is
// considered stale and treated as failed (self-healing for crashed workers).
const pendingTimeout = 3 * time.Minute

// statusUpdateTimeout is the context timeout for concert search status updates.
// Uses a fresh context.Background() to ensure updates succeed even when the
// parent context (Gemini API call) has expired.
const statusUpdateTimeout = 5 * time.Second

// Compile-time interface compliance check
var _ ConcertUseCase = (*concertUseCase)(nil)

// NewConcertUseCase creates a new concert use case.
// It orchestrates concert searching, retrieval, the hand-over of new concerts, and the
// admin approval/management operations (approve, reject, list, delete).
func NewConcertUseCase(
	artistRepo entity.ArtistRepository,
	concertRepo entity.ConcertRepository,
	venueRepo entity.VenueRepository,
	seriesRepo entity.SeriesRepository,
	organizerRepo entity.OrganizerRepository,
	concertSearchRepo entity.ConcertSearchRepository,
	concertCandidateRepo entity.ConcertCandidateRepository,
	rejectedConcertRepo entity.RejectedConcertRepository,
	concertSearcher entity.ConcertSearcher,
	centroidResolver CentroidResolver,
	workflows Workflows,
	metrics ConcertMetrics,
	analytics AnalyticsClient,
	searchCacheTTL time.Duration,
	discoveryWindow time.Duration,
	clock Clock,
	logger *logging.Logger,
) *concertUseCase {
	return &concertUseCase{
		artistRepo:           artistRepo,
		concertRepo:          concertRepo,
		venueRepo:            venueRepo,
		seriesRepo:           seriesRepo,
		organizerRepo:        organizerRepo,
		concertSearchRepo:    concertSearchRepo,
		concertCandidateRepo: concertCandidateRepo,
		rejectedConcertRepo:  rejectedConcertRepo,
		concertSearcher:      concertSearcher,
		centroidResolver:     centroidResolver,
		workflows:            workflows,
		metrics:              metrics,
		analytics:            analytics,
		searchCacheTTL:       searchCacheTTL,
		discoveryWindow:      discoveryWindow,
		clock:                clock,
		logger:               logger,
	}
}

// ListByArtist returns the artist's fan-visible concerts of every date.
func (uc *concertUseCase) ListByArtist(ctx context.Context, artistID string) ([]*entity.Concert, error) {
	concerts, err := uc.concertRepo.ListByArtist(ctx, artistID)
	if err != nil {
		return nil, err
	}

	return concerts, nil
}

// Get returns the Concert of one Event when the Event has an event page.
func (uc *concertUseCase) Get(ctx context.Context, eventID string) (*entity.Concert, error) {
	concerts, err := uc.concertRepo.ListByIDs(ctx, []string{eventID})
	if err != nil {
		return nil, err
	}
	if len(concerts) == 0 {
		return nil, errNoEventPage()
	}
	concert := concerts[0]
	if concert.Series == nil || !concert.HasEventPage(concert.Series) {
		return nil, errNoEventPage()
	}
	return concert, nil
}

// ListBySeries returns the Concerts of one Series that has an event page, in
// ListEventsBySeries order.
func (uc *concertUseCase) ListBySeries(ctx context.Context, seriesID string) ([]*entity.Concert, error) {
	series, err := uc.seriesRepo.Get(ctx, seriesID)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return nil, errNoEventPage()
		}
		return nil, err
	}

	// ListEventsBySeries leaves DRAFT events out, so a Series has an event page
	// exactly when one of these events has one.
	events, err := uc.concertRepo.ListEventsBySeries(ctx, seriesID)
	if err != nil {
		return nil, err
	}
	if !series.HasAnyEventPage(events) {
		return nil, errNoEventPage()
	}

	ids := make([]string, len(events))
	for i, e := range events {
		ids[i] = e.ID
	}
	concerts, err := uc.concertRepo.ListByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}

	// ListByIDs orders by date only; restore the ListEventsBySeries order so
	// same-day shows are ordered by start time.
	byID := make(map[string]*entity.Concert, len(concerts))
	for _, c := range concerts {
		byID[c.ID] = c
	}
	ordered := make([]*entity.Concert, 0, len(concerts))
	for _, id := range ids {
		c, ok := byID[id]
		if !ok {
			continue
		}
		ordered = append(ordered, c)
	}
	return ordered, nil
}

// ListFollowed returns concerts for followed artists, grouped by date
// and classified into home/nearby/away lanes based on proximity to the user's home.
// A nil from defaults to today onward; a past from widens the range into the past.
func (uc *concertUseCase) ListFollowed(ctx context.Context, userID string, home *entity.Home, from *time.Time) ([]*entity.ProximityGroup, error) {
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

// ListByLocation returns the concerts near the given reference point within
// [from, to], grouped by date, with only the HOME and NEARBY concerts of each
// date (entity.LocationQuery).
func (uc *concertUseCase) ListByLocation(ctx context.Context, location *entity.GeoLocation, from, to time.Time) ([]*entity.ProximityGroup, error) {
	query := entity.LocationQuery{Location: location, From: from, To: to}
	if err := query.Check(); err != nil {
		return nil, err
	}

	concerts, err := uc.concertRepo.ListByLocation(ctx, location, from, to)
	if err != nil {
		return nil, fmt.Errorf("list concerts by location: %w", err)
	}
	return query.NearbyGroups(concerts), nil
}

// SearchTrigger names what started a SearchNewConcerts run, reported as the
// `trigger` of concert.search.completed.
type SearchTrigger string

const (
	// SearchTriggerDaily is the daily run over followed artists.
	SearchTriggerDaily SearchTrigger = "daily_run"
	// SearchTriggerFirstFollow is SearchNewConcertsOnFirstFollow.
	SearchTriggerFirstFollow SearchTrigger = "first_follow"
	// SearchTriggerManual is a run started by hand, outside the daily run.
	SearchTriggerManual SearchTrigger = "manual"
)

// Outcomes of the concert.search.completed signal.
const (
	concertSearchOutcomeFailed     = "failed"
	concertSearchOutcomeNothingNew = "nothing_new"
	concertSearchOutcomeFound      = "found"
)

// SearchNewConcerts discovers new concerts for the given artist synchronously.
// It returns the newly discovered concerts after deduplication against
// already-known upcoming events.
func (uc *concertUseCase) SearchNewConcerts(ctx context.Context, artistID string, trigger SearchTrigger) ([]*entity.Concert, error) {
	artist, err := uc.artistRepo.Get(ctx, artistID)
	if err != nil {
		return nil, fmt.Errorf("failed to get artist: %w", err)
	}

	// An artist whose activity has ended (a dissolved group, a solo artist
	// who died) will not announce concerts: skip the external search. When
	// the catalog clears the end, the artist is searched again.
	if !artist.IsActive() {
		uc.logger.Debug(ctx, "skipping discovery for artist whose activity has ended",
			slog.String("artist_id", artistID),
		)
		return nil, nil
	}

	skip, err := uc.skipSearch(ctx, artistID)
	if err != nil || skip {
		return nil, err
	}

	// Mark as pending. If this fails, abort — without a pending row,
	// downstream UpdateStatus calls silently no-op (0 rows affected).
	searchTime, err := uc.concertSearchRepo.Upsert(ctx, artistID, entity.ConcertSearchStatusPending)
	if err != nil {
		return nil, fmt.Errorf("failed to mark search as pending: %w", err)
	}

	return uc.executeSearch(ctx, artist, trigger, searchTime)
}

// skipSearch reports whether no external search runs for the artist now, and
// logs why.
//
// Discovery exclusion: an artist whose concerts are managed directly by an
// active organizer is skipped. The organizer publishes their concerts via the
// authoring surface; a background re-discovery would only produce noisy
// duplicate-detection conflicts and consume external API quota for no net
// gain. When the organizer is deactivated, IsArtistRepresentedByActiveOrganizer
// returns false and discovery resumes automatically. A failure to check is
// non-fatal: it is logged and discovery proceeds rather than being silently
// blocked.
//
// Otherwise the stored concert search decides: skip if recently completed,
// recently found a new concert, or currently pending.
func (uc *concertUseCase) skipSearch(ctx context.Context, artistID string) (bool, error) {
	if uc.organizerRepo != nil {
		represented, err := uc.organizerRepo.IsArtistRepresentedByActiveOrganizer(ctx, artistID)
		if err != nil {
			uc.logger.Warn(ctx, "failed to check organizer representation for discovery exclusion",
				slog.String("artist_id", artistID),
				slog.Any("error", err),
			)
		} else if represented {
			uc.logger.Debug(ctx, "skipping discovery for organizer-represented artist",
				slog.String("artist_id", artistID),
			)
			return true, nil
		}
	}

	concertSearch, err := uc.concertSearchRepo.GetByArtistID(ctx, artistID)
	if err != nil && !errors.Is(err, apperr.ErrNotFound) {
		return false, fmt.Errorf("failed to get concert search: %w", err)
	}
	if concertSearch == nil {
		return false, nil
	}
	now := uc.clock()
	if concertSearch.IsFresh(now, uc.searchCacheTTL) {
		uc.logger.Debug(ctx, "skipping external search, recently searched",
			slog.String("artist_id", artistID),
			slog.Time("search_time", concertSearch.SearchTime),
		)
		return true, nil
	}
	// Skip even when the last search is stale if a new concert was found
	// recently: announcements arrive in batches then go quiet, so a repeat
	// search would just re-find the same events and dedup to nothing.
	if concertSearch.WasRecentlyDiscovered(now, uc.discoveryWindow) {
		uc.logger.Debug(ctx, "skipping external search, recently discovered new concert",
			slog.String("artist_id", artistID),
			slog.Time("last_found_at", concertSearch.LastFoundTime),
		)
		return true, nil
	}
	if concertSearch.IsPending(now, pendingTimeout) {
		uc.logger.Debug(ctx, "skipping external search, already pending",
			slog.String("artist_id", artistID),
		)
		return true, nil
	}
	return false, nil
}

// SearchNewConcertsOnFirstFollow triggers concert discovery for an artist the
// first time it is followed by anyone (no ConcertSearch row exists yet), and is a
// no-op otherwise.
func (uc *concertUseCase) SearchNewConcertsOnFirstFollow(ctx context.Context, artistID string) error {
	_, err := uc.concertSearchRepo.GetByArtistID(ctx, artistID)
	if err == nil {
		// Concert search exists — artist has been searched before, skip.
		return nil
	}
	if !errors.Is(err, apperr.ErrNotFound) {
		return fmt.Errorf("check concert search for first-follow search: %w", err)
	}

	// No concert search — this is a first follow. Trigger concert discovery.
	uc.logger.Info(ctx, "first follow detected, triggering concert search",
		slog.String("artist_id", artistID),
	)
	if _, err := uc.SearchNewConcerts(ctx, artistID, SearchTriggerFirstFollow); err != nil {
		return fmt.Errorf("search new concerts for first follow: %w", err)
	}
	return nil
}

// executeSearch performs the actual Gemini search, deduplication, and the start
// of CreateFromDiscovered for the new concerts.
// It returns the newly discovered concerts and updates the concert search status on exit.
//
// Deduplication uses `(local_event_date, listed_venue_name)` matching via
// `entity.FilterNewSeries`, aligned with the DB-level natural key
// `UNIQUE (series_id, local_event_date, venue_id)` enforced by the events
// table (added in migration `20260523145447_add_series_hierarchy`). The pre-
// v0.41.0 per-artist constraint `(artist_id, local_event_date)` was dropped
// in that migration alongside the singular events.artist_id column.
//
// The application-level FilterNewSeries check on `(date, listed_venue_name)` avoids
// unnecessary hand-over/UPSERT round-trips for re-scrapes; the DB natural key
// is the source of truth and uses the resolved `venue_id` instead of the raw
// listed name, so the application key is a best-effort upstream filter.
//
// searchTime is the search time of the Artist's ConcertSearch; the
// create-from-discovered workflow the search starts is named after it.
func (uc *concertUseCase) executeSearch(ctx context.Context, artist *entity.Artist, trigger SearchTrigger, searchTime time.Time) (result []*entity.Concert, err error) {
	artistID := artist.ID
	// searched is set once the external search is called; only such a run
	// reports its outcome. announcedSeries/Events count what was handed on.
	var (
		searched                         bool
		announcedSeries, announcedEvents int
	)
	defer func() {
		if searched {
			uc.reportSearchOutcome(ctx, artistID, trigger, announcedSeries, announcedEvents, len(result), err)
		}
	}()
	defer func() { uc.recordSearchEnd(ctx, artistID, len(result), err) }()

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
	if err := artist.CheckSearchable(); err != nil {
		return nil, err
	}

	newSeries, newEvents, err := uc.findNewSeries(ctx, artist, &searched)
	if err != nil {
		return nil, err
	}

	// Hand the new concerts to CreateFromDiscovered (artist-level batch).
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

	if err := uc.workflows.Start(ctx, WorkflowCreateFromDiscovered, discoveryWorkflowID(artistID, searchTime), eventData); err != nil {
		uc.logger.Error(ctx, "failed to start create-from-discovered", err,
			slog.String("artist_id", artistID),
			slog.Int("concert_count", newEvents),
		)
		// The CronJob will re-discover on the next run.
		// The defer will call markSearchFailed because err != nil.
		return nil, err
	}

	announcedSeries, announcedEvents = len(newSeries), newEvents
	uc.logger.Info(ctx, "started create-from-discovered",
		slog.String("artist_id", artistID),
		slog.String("artist_name", artist.Name),
		slog.Int("series_count", len(newSeries)),
		slog.Int("concert_count", newEvents),
	)

	// Record the discovery so the discoveryWindow skip suppresses redundant
	// re-searches until the next announcement cycle is likely.
	uc.markSearchFound(ctx, artistID)

	return previewConcerts(artist, newSeries, newEvents), nil
}

// findNewSeries searches the artist's new concerts through the external API
// and returns the discovered series left after leaving out the events already
// published or pending review, with their event count. *searched is set once
// the external search is called.
func (uc *concertUseCase) findNewSeries(ctx context.Context, artist *entity.Artist, searched *bool) ([]*entity.DiscoveredSeries, int, error) {
	artistID := artist.ID
	// Get Official Site — missing site is not an error; search continues with nil
	site, err := uc.artistRepo.GetOfficialSite(ctx, artistID)
	if errors.Is(err, apperr.ErrNotFound) {
		site, err = nil, nil
	}
	if err != nil {
		return nil, 0, fmt.Errorf("failed to get official site: %w", err)
	}

	// One instant for the whole run: the search leaves out events dated
	// before the current date at their Venue at now, and the concerts it is
	// compared against are the upcoming ones at the same instant.
	now := uc.clock()

	// Get existing upcoming concerts for deduplication.
	existing, err := uc.concertRepo.ListUpcomingByArtist(ctx, artistID, now)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to list existing concerts: %w", err)
	}

	// Fetch pending concert candidates for this artist so that already-concert candidates
	// are not re-staged. The rejection log is intentionally NOT consulted here:
	// a rejected concert can always re-enter the queue on a subsequent run.
	pendingKeys, err := uc.concertCandidateRepo.ListPendingDedupKeysByArtist(ctx, artistID)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to list pending concert candidate keys: %w", err)
	}

	// Search new concerts via external API (deadline inherited from HandlerTimeout).
	// The searcher returns discovered concerts already grouped by series.
	*searched = true
	scraped, err := uc.concertSearcher.Search(ctx, artist, site, now)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to search concerts via external API: %w", err)
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

	// Further deduplicate against pending concert candidates: events pending
	// review are dropped and a series emptied by that is removed entirely.
	newSeries = entity.DropPending(newSeries, pendingKeys)
	newEvents = entity.SumSeriesEvents(newSeries)

	if filtered := scrapedEvents - newEvents; filtered > 0 {
		uc.logger.Debug(ctx, "filtered existing/duplicate events (same date)",
			slog.String("artist_id", artistID),
			slog.Int("filtered_count", filtered),
		)
	}

	return newSeries, newEvents, nil
}

// previewConcerts builds the Concert entities of the deduplicated series to
// return to the caller.
// Event / Venue IDs stay empty because the search path returns concerts for
// immediate display rather than persistence. One synthetic series ID (UUIDv7)
// is minted per discovered series and shared across its events so the
// embedded SeriesId carries a valid UUID and passes the response-side
// protovalidate guards; the synthetic ID has no referent in the DB and is
// discarded by the client after rendering.
func previewConcerts(artist *entity.Artist, series []*entity.DiscoveredSeries, eventCount int) []*entity.Concert {
	concerts := make([]*entity.Concert, 0, eventCount)
	for _, s := range series {
		syntheticSeriesID := entity.NewID()
		for _, ev := range s.Events {
			c := s.ToConcert(ev, artist.ID, syntheticSeriesID, "", "")
			// Replace ToConcert's id-only Performer shell with the resolved
			// Artist entity so the response carries a complete performer with
			// Name and MBID (validated non-empty by the guard above).
			c.Artists = []*entity.Artist{artist}
			concerts = append(concerts, c)
		}
	}
	return concerts
}

// reportSearchOutcome sends the concert.search.completed signal of a run that
// called the external search, with what it handed on (announcedSeries and
// announcedEvents) and the outcome: failed with err, nothing new when it
// found no concert, found otherwise.
func (uc *concertUseCase) reportSearchOutcome(ctx context.Context, artistID string, trigger SearchTrigger, announcedSeries, announcedEvents, found int, err error) {
	outcome := concertSearchOutcomeFound
	switch {
	case err != nil:
		outcome = concertSearchOutcomeFailed
	case found == 0:
		outcome = concertSearchOutcomeNothingNew
	}
	props := AnalyticsProperties{
		"artist_id":    artistID,
		"trigger":      string(trigger),
		"outcome":      outcome,
		"series_count": announcedSeries,
		"event_count":  announcedEvents,
	}
	if err != nil {
		props["error_code"] = errorCode(err)
	}
	sendSystemSignal(ctx, uc.analytics, uc.logger, EventConcertSearchCompleted, uc.clock(), props)
}

// recordSearchEnd stores the search's status and its metric when a run ends:
// failed with err, otherwise completed, with a metric that tells a healthy run
// that discovered nothing new from a fruitful one so pipeline-health views can
// spot quota burned for no yield.
func (uc *concertUseCase) recordSearchEnd(ctx context.Context, artistID string, found int, err error) {
	switch {
	case err != nil:
		uc.markSearchFailed(ctx, artistID)
		uc.metrics.RecordConcertSearch(ctx, "error")
	case found == 0:
		uc.markSearchCompleted(ctx, artistID)
		uc.metrics.RecordConcertSearch(ctx, "zero_results")
	default:
		uc.markSearchCompleted(ctx, artistID)
		uc.metrics.RecordConcertSearch(ctx, "success")
	}
}

// markSearchCompleted updates the concert search status to completed.
// It uses context.WithoutCancel to detach from the parent's deadline while
// preserving trace context for span correlation.
func (uc *concertUseCase) markSearchCompleted(ctx context.Context, artistID string) {
	updateCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), statusUpdateTimeout)
	defer cancel()

	if err := uc.concertSearchRepo.UpdateStatus(updateCtx, artistID, entity.ConcertSearchStatusCompleted); err != nil {
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

	if err := uc.concertSearchRepo.MarkFound(updateCtx, artistID); err != nil {
		uc.logger.Error(ctx, "failed to mark search as found", err, slog.String("artist_id", artistID))
	}
}

// markSearchFailed updates the concert search status to failed.
// It uses context.WithoutCancel to detach from the parent's deadline while
// preserving trace context for span correlation.
func (uc *concertUseCase) markSearchFailed(ctx context.Context, artistID string) {
	updateCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), statusUpdateTimeout)
	defer cancel()

	if err := uc.concertSearchRepo.UpdateStatus(updateCtx, artistID, entity.ConcertSearchStatusFailed); err != nil {
		uc.logger.Error(ctx, "failed to mark search as failed", err, slog.String("artist_id", artistID))
	}
}

// List returns every published concert for admin catalog management. Read logic
// is shared with the consumer path through ConcertRepository; this method only
// strips the audience filter.
func (uc *concertUseCase) List(ctx context.Context) ([]*entity.Concert, error) {
	concerts, err := uc.concertRepo.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list published concerts: %w", err)
	}
	return concerts, nil
}

// ListCatalogByArtist implements [ConcertUseCase].
func (uc *concertUseCase) ListCatalogByArtist(ctx context.Context, artistID string) ([]*entity.Concert, error) {
	if artistID == "" {
		return nil, apperr.New(codes.InvalidArgument, "artist id must not be empty")
	}
	concerts, err := uc.concertRepo.ListCatalogByArtist(ctx, artistID)
	if err != nil {
		return nil, fmt.Errorf("list the artist's concerts: %w", err)
	}
	return concerts, nil
}

// Delete permanently removes a published concert by its event id, with the
// records that depend only on it (ConcertRepository.Delete). An empty id is
// rejected; deleting an absent id succeeds and changes nothing. A live
// purchase of the event fails it with the repository's FailedPrecondition,
// unchanged.
func (uc *concertUseCase) Delete(ctx context.Context, eventID string) error {
	if eventID == "" {
		return apperr.New(codes.InvalidArgument, "event id must not be empty")
	}
	if err := uc.concertRepo.Delete(ctx, eventID); err != nil {
		return fmt.Errorf("delete concert %q: %w", eventID, err)
	}
	return nil
}
