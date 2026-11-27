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

	// ListByLocation returns all concerts in or near the first-level Area
	// whose local_event_date falls within [from, to], grouped by date and
	// classified by proximity to the Area and its reference point. Only HOME
	// and NEARBY concerts are returned; AWAY-only date groups are omitted
	// entirely. An Area without a reference point yields only its HOME
	// concerts.
	//
	// # Possible errors
	//
	//  - InvalidArgument: the date range exceeds the maximum window, or the
	//    area is no first-level Area.
	//  - Internal: database query failure.
	ListByLocation(ctx context.Context, area string, from, to time.Time) ([]*entity.ProximityGroup, error)

	// SearchNewConcerts starts a search for new concerts of the artist and
	// returns without waiting for it. Unless a rule skips the search (the
	// artist's activity has ended, an active organizer represents it, or its
	// ConcertSearch is fresh, recently discovered or pending), it records the
	// ConcertSearch as pending and starts the search-concerts workflow named
	// after the artist and that search time (RunSearch).
	//
	// trigger names what started the search; it is reported with the search's
	// outcome when the external search is called.
	//
	// # Possible errors
	//
	//  - NotFound: If the artist does not exist.
	//  - Internal: If the artist is missing the name or MBID a search needs,
	//    or the database fails.
	//  - Unavailable: If the search cannot be started.
	SearchNewConcerts(ctx context.Context, artistID string, trigger SearchTrigger) error

	// RunSearch is the search-concerts workflow, one attempt of a search that
	// SearchNewConcerts started. Only the workflow worker runs it.
	//
	// # Possible errors
	//
	//  - Internal: If the search or the artist cannot be read, or an attempt
	//    cannot be recorded.
	RunSearch(ctx context.Context, f Flow, in ConcertSearchAttempt) (struct{}, error)

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
	// than once for the artist, but the search's own dedup (against
	// already-known concerts and pending concert candidates) and the DB natural key
	// on events guarantee any discovered concerts are stored only once.
	//
	// # Possible errors
	//
	//  - Internal: concert search lookup failure.
	//  - Any error of SearchNewConcerts.
	SearchNewConcertsOnFirstFollow(ctx context.Context, artistID string) error

	// Get returns the Concert of one Event when that Event has a concert page
	// (see [entity.Event.HasEventPage]). The returned Concert carries its own
	// publish state and its Series as every Concert read returns it, including
	// its description, cover image, visibility and publish state. No signed-in
	// caller is needed.
	//
	// # Possible errors
	//
	//  - NotFound: If no Concert has the id, or its Event has no concert page.
	//    The same error is returned in every case so a caller cannot learn
	//    whether a DRAFT or UNLISTED event exists.
	//  - Any other repository error, returned unchanged.
	Get(ctx context.Context, eventID string) (*entity.Concert, error)

	// ListBySeries returns every Concert of one Series that has a concert page,
	// ordered by local date and then by start time (as
	// ConcertRepository.ListEventsBySeries orders them). Each Concert carries
	// its Series as every Concert read returns it. No signed-in caller is
	// needed.
	//
	// # Possible errors
	//
	//  - NotFound: If no Series has the id, or the Series has no concert page.
	//  - Any other repository error, returned unchanged.
	ListBySeries(ctx context.Context, seriesID string) ([]*entity.Concert, error)
}

// errNoEventPage is the single NotFound returned by Get and ListBySeries for
// every miss, so the response never reveals why an event has no page.
func errNoEventPage() error {
	return apperr.New(codes.NotFound, "concert page not found")
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
	areas                entity.AreaRepository
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

// pendingTimeout is the age up to which a pending concert search counts as in
// progress: the time its attempts may keep calling the external search. An
// older one is searched again (self-healing for a search that never ended).
const pendingTimeout = searchWindow

// statusUpdateTimeout is the context timeout for recording a search's find.
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
	areas entity.AreaRepository,
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
		areas:                areas,
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

// Get returns the Concert of one Event when the Event has a concert page.
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

// ListBySeries returns the Concerts of one Series that has a concert page, in
// ListEventsBySeries order.
func (uc *concertUseCase) ListBySeries(ctx context.Context, seriesID string) ([]*entity.Concert, error) {
	series, err := uc.seriesRepo.Get(ctx, seriesID)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return nil, errNoEventPage()
		}
		return nil, err
	}

	// ListEventsBySeries leaves DRAFT events out, so a Series has a concert page
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
// and classified by proximity to the given home. The home's reference point
// is its Area's; a level 1 that resolves to no first-level Area, or to
// another one, leaves the home without one, so only its admin area is HOME.
func (uc *concertUseCase) ListByArtists(ctx context.Context, artistIDs []string, home *entity.Home) ([]*entity.ProximityGroup, error) {
	if home != nil {
		home.ReferencePoint = nil
		area, err := uc.areas.Resolve(ctx, home.Level1, nil)
		switch {
		case err == nil && area.Code == home.Level1:
			home.ReferencePoint = area.ReferencePoint
		case err != nil && !errors.Is(err, apperr.ErrNotFound) && !errors.Is(err, apperr.ErrInvalidArgument):
			return nil, err
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
func (uc *concertUseCase) ListByLocation(ctx context.Context, area string, from, to time.Time) ([]*entity.ProximityGroup, error) {
	query := entity.LocationQuery{From: from, To: to}
	if err := query.Check(); err != nil {
		return nil, err
	}
	resolved, err := uc.areas.Resolve(ctx, area, nil)
	if errors.Is(err, apperr.ErrNotFound) || (err == nil && resolved.Code != area) {
		return nil, apperr.New(codes.InvalidArgument, "the area is no first-level Area", slog.String("area", area))
	}
	if err != nil {
		return nil, err
	}
	location := entity.LocationOf(resolved)
	query.Location = location

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

// SearchNewConcerts records the search of the artist's new concerts and
// starts it; the search-concerts workflow does the rest.
func (uc *concertUseCase) SearchNewConcerts(ctx context.Context, artistID string, trigger SearchTrigger) error {
	artist, err := uc.artistRepo.Get(ctx, artistID)
	if err != nil {
		return fmt.Errorf("failed to get artist: %w", err)
	}

	// An artist whose activity has ended (a dissolved group, a solo artist
	// who died) will not announce concerts: skip the external search. When
	// the catalog clears the end, the artist is searched again.
	if !artist.IsActive() {
		uc.logger.Debug(ctx, "skipping discovery for artist whose activity has ended",
			slog.String("artist_id", artistID),
		)
		return nil
	}

	skip, err := uc.skipSearch(ctx, artistID)
	if err != nil || skip {
		return err
	}

	// An artist row with an empty Name or MBID is a data-integrity problem
	// the search cannot recover from: both are required to search and to link
	// the concerts it finds. Fail before anything is recorded or spent; only
	// an admin's fix of the row helps, and the error keeps showing until then.
	if err := artist.CheckSearchable(); err != nil {
		return err
	}

	// Mark as pending. If this fails, abort: the search only runs for a
	// pending ConcertSearch.
	searchTime, err := uc.concertSearchRepo.Upsert(ctx, artistID, entity.ConcertSearchStatusPending)
	if err != nil {
		return fmt.Errorf("failed to mark search as pending: %w", err)
	}

	// No deadline: the engine counts one from the dequeue, so each attempt
	// checks the search window against the search time itself.
	first := ConcertSearchAttempt{ArtistID: artistID, SearchTime: searchTime, Trigger: trigger, Attempt: 1}
	if err := uc.workflows.Start(ctx, WorkflowSearchConcerts, searchAttemptID(artistID, searchTime, 1), first,
		WithQueue(ConcertSearchQueue)); err != nil {
		return fmt.Errorf("start the concert search: %w", err)
	}
	return nil
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
	if uc.isRepresented(ctx, artistID) {
		return true, nil
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
			slog.Time("last_find_time", concertSearch.LastFindTime),
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

// isRepresented reports whether an active organizer represents the artist,
// and logs why discovery leaves it out. A failure to check is logged and
// reported as not represented.
func (uc *concertUseCase) isRepresented(ctx context.Context, artistID string) bool {
	if uc.organizerRepo == nil {
		return false
	}
	represented, err := uc.organizerRepo.IsArtistRepresentedByActiveOrganizer(ctx, artistID)
	if err != nil {
		uc.logger.Warn(ctx, "failed to check organizer representation for discovery exclusion",
			slog.String("artist_id", artistID),
			slog.Any("error", err),
		)
		return false
	}
	if represented {
		uc.logger.Debug(ctx, "skipping discovery for organizer-represented artist",
			slog.String("artist_id", artistID),
		)
	}
	return represented
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
	if err := uc.SearchNewConcerts(ctx, artistID, SearchTriggerFirstFollow); err != nil {
		return fmt.Errorf("search new concerts for first follow: %w", err)
	}
	return nil
}

// markSearchFound records that the search discovered at least one new concert.
// Failure is non-fatal and only logged: the new concerts were already handed
// on, so a missed last_find_time update merely lets the next CronJob tick
// re-search this artist sooner than the discoveryWindow would otherwise allow.
func (uc *concertUseCase) markSearchFound(ctx context.Context, artistID string) {
	updateCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), statusUpdateTimeout)
	defer cancel()

	if err := uc.concertSearchRepo.MarkFound(updateCtx, artistID); err != nil {
		uc.logger.Error(ctx, "failed to mark search as found", err, slog.String("artist_id", artistID))
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
