package usecase

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/pkg/analyticslog"
	"github.com/pannpers/go-logging/logging"
)

// ConcertCreationUseCase processes discovered concert batches. It resolves each
// concert's venue and branches the outcome: a genuinely new concert at a resolved
// venue is auto-published, a same-slot conflict or an unresolved venue is staged
// for admin review.
type ConcertCreationUseCase interface {
	// CreateFromDiscovered processes a batch of scraped concerts for a single
	// artist. For each concert it resolves a venue via Google Places API, then:
	//   - unresolved venue → staged for review (no venues row, no publish);
	//   - resolved venue with a same-slot conflict → staged for reconciliation
	//     (no publish), resolved later via ConcertCandidateUseCase.Approve;
	//   - resolved venue, genuinely new → auto-published: the events of one
	//     series are stored in one Concert.Create call, which announces
	//     CONCERT.created so follower notifications fire immediately.
	CreateFromDiscovered(ctx context.Context, data entity.ConcertDiscoveredData) error

	// RunCreateFromDiscovered is the create-from-discovered workflow
	// ConcertUseCase.SearchNewConcerts starts.
	RunCreateFromDiscovered(ctx context.Context, f Flow, in entity.ConcertDiscoveredData) (struct{}, error)
}

// concertCreationUseCase implements ConcertCreationUseCase. It reuses the
// package-level venue/duplicate helpers shared with ConcertCandidateUseCase.Approve
// so discovery and approval agree on venue resolution and conflict detection.
type concertCreationUseCase struct {
	concertCandidateRepo entity.ConcertCandidateRepository
	venueRepo            entity.VenueRepository
	concertRepo          entity.ConcertRepository
	seriesRepo           entity.SeriesRepository
	placeSearcher        entity.VenuePlaceSearcher
	analytics            AnalyticsClient
	clock                Clock
	logger               *logging.Logger
}

// Compile-time interface compliance check.
var _ ConcertCreationUseCase = (*concertCreationUseCase)(nil)

// NewConcertCreationUseCase creates a new ConcertCreationUseCase.
// placeSearcher must not be nil; panics if not provided. analytics may be
// nil; the batch outcome is then not reported.
func NewConcertCreationUseCase(
	concertCandidateRepo entity.ConcertCandidateRepository,
	venueRepo entity.VenueRepository,
	concertRepo entity.ConcertRepository,
	seriesRepo entity.SeriesRepository,
	placeSearcher entity.VenuePlaceSearcher,
	analytics AnalyticsClient,
	clock Clock,
	logger *logging.Logger,
) ConcertCreationUseCase {
	if placeSearcher == nil {
		panic("placeSearcher is required")
	}
	return &concertCreationUseCase{
		concertCandidateRepo: concertCandidateRepo,
		venueRepo:            venueRepo,
		concertRepo:          concertRepo,
		seriesRepo:           seriesRepo,
		placeSearcher:        placeSearcher,
		analytics:            analytics,
		clock:                clock,
		logger:               logger,
	}
}

// ingestTally counts what one CreateFromDiscovered batch did with its events,
// for the concert.ingest.completed signal.
type ingestTally struct {
	published, staged, filled, conflicts int
}

// Outcomes of the concert.ingest.completed signal.
const (
	ingestOutcomeCompleted = "completed"
	ingestOutcomeFailed    = "failed"
)

// CreateFromDiscovered processes a discovered concert batch grouped by series.
// The series_id shared by a whole group is resolved ONCE (adopt an existing
// event's series, else mint), the series row is created eagerly, then each
// member event is resolved and branched between auto-publish and staging —
// all under the same series_id.
//
// Per series:
//  1. Resolve the group series_id via a cheap (artist, dates) DB lookup — NO
//     Places API. Create the series row now (when minted) so it exists before
//     any event references it.
//
// Per event:
//  1. Re-discovery fast-path: an existing (artist, date) event at the same
//     listed venue is a re-discovery — skip the Places API, fill a known start
//     onto an unknown-start row if applicable, and insert nothing new.
//  2. Otherwise resolve the venue via Google Places (batch-local cache).
//  3. Unresolved venue → stage for review (no venues row, no publish).
//  4. Resolve/create the venues row.
//  5. Same-slot conflict → stage for reconciliation (no publish).
//  6. Genuinely new → insert the event under the group series_id.
//
// After all events of a series are decided, the events to publish are stored
// in ONE Concert.Create call, which announces them together, so a multi-date
// tour wakes the notification consumer once. A lightweight cleanup at the end removes any
// series left with no events and no pending concert candidates (the provisional-mint or
// all-candidate-then-rejected case).
func (uc *concertCreationUseCase) CreateFromDiscovered(ctx context.Context, data entity.ConcertDiscoveredData) (err error) {
	// Batch-local place cache: (listed_venue_name, admin_area) → *VenuePlace.
	// Avoids redundant Places API calls for the same venue within one batch,
	// shared across every series of this artist's batch.
	newPlaces := make(map[string]*entity.VenuePlace)
	tally := &ingestTally{}

	// Every batch reports what it did, a failed one included with what it had
	// done before failing. The message context carries the trace of the
	// search that found the batch, so the signal joins that search's signals.
	defer func() {
		outcome := ingestOutcomeCompleted
		if err != nil {
			outcome = ingestOutcomeFailed
		}
		sendSystemSignal(ctx, uc.analytics, uc.logger, EventConcertIngestCompleted, uc.clock(), AnalyticsProperties{
			"artist_id":       data.ArtistID,
			"outcome":         outcome,
			"published_count": tally.published,
			"staged_count":    tally.staged,
			"filled_count":    tally.filled,
			"conflict_count":  tally.conflicts,
		})
	}()

	// mintedSeriesIDs collects the series this batch newly created, so the
	// end-of-batch orphan sweep is scoped to them alone and cannot delete another
	// concurrently-processing run's just-created (still event-less) series.
	var mintedSeriesIDs []string
	for _, series := range data.Series {
		if series == nil || len(series.Events) == 0 {
			continue
		}
		if series.Title == "" {
			uc.logger.Warn(ctx, "skipping discovered series: empty title from Gemini",
				slog.String("artist_id", data.ArtistID),
				slog.Int("event_count", len(series.Events)),
			)
			continue
		}

		mintedID, err := uc.createSeriesFromDiscovered(ctx, data.ArtistID, series, newPlaces, tally)
		if err != nil {
			return err
		}
		if mintedID != "" {
			mintedSeriesIDs = append(mintedSeriesIDs, mintedID)
		}
	}

	// Sweep only the series THIS batch minted that ended up with no events and no
	// pending concert candidates (a provisional group mint that turned out to be a
	// co-headliner and adopted a sibling's event's series, so its own row was left
	// empty). Scoping to minted ids avoids racing another run's eager series row.
	if _, err := uc.seriesRepo.DeleteOrphaned(ctx, mintedSeriesIDs); err != nil {
		uc.logger.Error(ctx, "failed to clean up orphaned series", err,
			slog.String("artist_id", data.ArtistID),
		)
		// Non-fatal: an orphaned series row is harmless and swept next run.
	}
	return nil
}

// createSeriesFromDiscovered resolves the group series_id once, creates the
// series row when minted, decides each member event (staging the colliding and
// unresolved ones), and stores every event to publish under that series_id in
// one Concert.Create call, which announces them together. It returns the series_id it minted (empty when
// it adopted an existing series), so the caller can scope the orphan sweep.
func (uc *concertCreationUseCase) createSeriesFromDiscovered(
	ctx context.Context,
	artistID string,
	series *entity.DiscoveredSeries,
	newPlaces map[string]*entity.VenuePlace,
	tally *ingestTally,
) (string, error) {
	seriesID, existed, existingEvents, err := resolveSeriesForGroup(ctx, artistID, series.Events, uc.concertRepo)
	if err != nil {
		return "", fmt.Errorf("resolve series for %q: %w", series.Title, err)
	}

	// Eagerly create the series row when minted so it exists before any event
	// (published or staged) references it. Create is idempotent (ON CONFLICT (id)
	// DO NOTHING) so a redelivered message is harmless.
	mintedID := ""
	if !existed {
		mintedID = seriesID
		row := &entity.Series{ID: seriesID, Title: series.Title, Type: series.Type, SourceURL: series.SourceURL}
		if _, err := uc.seriesRepo.Create(ctx, row); err != nil {
			return "", fmt.Errorf("create series %q: %w", series.Title, err)
		}
	}

	toPublish, err := uc.decideSeriesEvents(ctx, artistID, seriesID, series, existingEvents, newPlaces, tally)
	if err != nil {
		return "", err
	}
	if len(toPublish) == 0 {
		return mintedID, nil
	}

	// One Concert.Create for the series, so it tells the Artist's followers
	// about all of them at once.
	insertedIDs, err := uc.concertRepo.Create(ctx, toPublish...)
	if err != nil {
		return "", fmt.Errorf("auto-publish concerts of %q: %w", series.Title, err)
	}
	tally.published += len(insertedIDs)
	if len(insertedIDs) > 0 {
		uc.logger.Info(ctx, "auto-published new concerts for series",
			slog.String("artist_id", artistID),
			slog.String("series_id", seriesID),
			slog.String("title", series.Title),
			slog.Int("inserted", len(insertedIDs)),
		)
	}

	return mintedID, nil
}

// decideSeriesEvents decides each member event of the series (see
// persistDiscoveredEvent) and returns the Concerts to publish together in one
// Concert.Create call. An event with no venue name left after normalization is
// skipped with a warning.
func (uc *concertCreationUseCase) decideSeriesEvents(
	ctx context.Context,
	artistID, seriesID string,
	series *entity.DiscoveredSeries,
	existingEvents []*entity.Event,
	newPlaces map[string]*entity.VenuePlace,
	tally *ingestTally,
) ([]*entity.Concert, error) {
	// Index existing (artist, date) events by date for the re-discovery fast-path.
	existingByDate := make(map[string][]*entity.Event, len(existingEvents))
	for _, ev := range existingEvents {
		k := ev.LocalDate.Format("2006-01-02")
		existingByDate[k] = append(existingByDate[k], ev)
	}

	// claimedFill is shared across this series' events so a NULL-start row that one
	// event fills is not re-claimed by a second same-(venue,date) event carrying a
	// different known start — that second event falls through to a fresh insert
	// instead of being COALESCE-swallowed and lost.
	claimedFill := make(map[string]bool)

	// toPublish collects the events of this series to publish, stored together
	// in one Concert.Create call after every event has been decided.
	var toPublish []*entity.Concert
	for _, ev := range series.Events {
		ev.ListedVenueName = entity.NormalizeVenueName(ev.ListedVenueName)
		if ev.ListedVenueName == "" {
			uc.logger.Warn(ctx, "skipping discovered event: empty venue name from Gemini",
				slog.String("artist_id", artistID),
				slog.String("title", series.Title),
				slog.Any("admin_area", ev.AdminArea),
				slog.String("local_date", ev.LocalDate.Format("2006-01-02")),
			)
			continue
		}

		concert, err := uc.persistDiscoveredEvent(ctx, artistID, seriesID, series, ev, existingByDate[ev.LocalDate.Format("2006-01-02")], claimedFill, newPlaces, toPublish, tally)
		if err != nil {
			return nil, err
		}
		if concert != nil {
			toPublish = append(toPublish, concert)
		}
	}
	return toPublish, nil
}

// rediscover handles an event the artist already plays at its venue and date:
// when an existing event matches it, a known start time is filled onto an
// unknown-start row (counted in tally) and the matching event is returned, so
// nothing new is stored. It returns nil when no existing event matches, a
// genuinely new start time.
func (uc *concertCreationUseCase) rediscover(
	ctx context.Context,
	artistID string,
	ev *entity.DiscoveredEvent,
	sameVenue []*entity.Event,
	claimedFill map[string]bool,
	tally *ingestTally,
) (*entity.Event, error) {
	match, isFill := resolveExistingEvent(sameVenue, ev, claimedFill)
	if match == nil || !isFill {
		return match, nil
	}
	claimedFill[match.ID] = true
	if err := uc.concertRepo.FillEventStartTimes(ctx,
		[]string{match.ID},
		[]*time.Time{entity.NullableTime(ev.StartTime)},
		[]*time.Time{entity.NullableTime(ev.OpenTime)},
	); err != nil {
		return nil, fmt.Errorf("fill event start times: %w", err)
	}
	tally.filled++
	uc.logger.Info(ctx, "filled existing event start time on re-discovery (no new concert)",
		slog.String("artist_id", artistID),
		slog.String("local_date", ev.LocalDate.Format("2006-01-02")),
	)
	return match, nil
}

// persistDiscoveredEvent resolves a single member event under the
// already-resolved group series_id and returns the Concert to publish, or nil
// for a re-discovery, fill, or staged outcome. toPublish holds the
// series' events already decided for publication, so an event colliding with
// one of them is staged too.
func (uc *concertCreationUseCase) persistDiscoveredEvent(
	ctx context.Context,
	artistID, seriesID string,
	series *entity.DiscoveredSeries,
	ev *entity.DiscoveredEvent,
	sameDateEvents []*entity.Event,
	claimedFill map[string]bool,
	newPlaces map[string]*entity.VenuePlace,
	toPublish []*entity.Concert,
	tally *ingestTally,
) (*entity.Concert, error) {
	// Re-discovery fast-path: an existing (artist, date) event at the same listed
	// venue is a re-discovery. Skip the Places API entirely; fill a known start
	// onto an unknown-start row when applicable, and insert nothing new. Only a
	// new date — or a listed venue not among the artist's existing events on this
	// date — falls through to venue resolution below.
	if sameVenue := eventsAtSameVenue(sameDateEvents, ev.ListedVenueName); len(sameVenue) > 0 {
		match, err := uc.rediscover(ctx, artistID, ev, sameVenue, claimedFill, tally)
		if err != nil {
			return nil, err
		}
		if match != nil {
			// Exact re-discovery (or fill) — no new event, no Places call.
			return nil, nil
		}
		// A genuinely new start time at a venue the artist already plays on this
		// date: reuse the known venue_id (no Places call), but still run the SAME
		// duplicate-staging gate as the general path — so an
		// unknown-start entry colliding with a known-start row stages for review
		// instead of inserting a phantom NULL-start duplicate.
		venueID := sameVenue[0].VenueID
		candidate := buildConcertCandidate(entity.NewID(), artistID, seriesID, series, ev, nil)
		return uc.stageOrPublish(ctx, artistID, seriesID, series, ev, venueID, candidate, toPublish, tally)
	}

	// An event without an admin area or a time zone cannot make a Venue: it
	// is staged without looking the venue up, for an admin to complete.
	if ev.AdminArea == nil || ev.TimeZone == nil {
		candidate := buildConcertCandidate(entity.NewID(), artistID, seriesID, series, ev, nil)
		return nil, uc.stageUnresolved(ctx, candidate, series, ev, tally, "area or time zone unknown")
	}

	place, err := uc.resolvePlace(ctx, ev.ListedVenueName, ev.AdminArea, newPlaces)
	if err != nil {
		return nil, fmt.Errorf("resolve venue %q: %w", ev.ListedVenueName, err)
	}
	// Cache the result (nil or not) so subsequent events in the same batch with
	// the same normalized venue name skip the Places API entirely.
	newPlaces[venueKey(ev.ListedVenueName, ev.AdminArea)] = place

	candidate := buildConcertCandidate(entity.NewID(), artistID, seriesID, series, ev, place)

	// Unresolved venue → stage for review. Auto-publishing here would mint a
	// coordinate-less venue from the raw name and publish it unreviewed.
	if place == nil {
		return nil, uc.stageUnresolved(ctx, candidate, series, ev, tally, "unresolved venue")
	}

	// Resolved venue: resolve or create the venues row (created only here, on the
	// path that may auto-publish — never for a conflict or unresolved row).
	fetched := uc.clock()
	venueID, err := findOrCreateVenue(ctx, venueSpec{
		listedName: ev.ListedVenueName,
		adminArea:  *ev.AdminArea,
		timeZone:   *ev.TimeZone,
		placeID:    &place.ProviderUserID,
		coordinates: func(context.Context) (*entity.PlaceCoordinates, error) {
			return &entity.PlaceCoordinates{Coordinates: place.Coordinates, FetchTime: fetched}, nil
		},
	}, uc.venueRepo, uc.logger)
	if err != nil {
		return nil, fmt.Errorf("resolve or create venue for %q: %w", series.Title, err)
	}

	return uc.stageOrPublish(ctx, artistID, seriesID, series, ev, venueID, candidate, toPublish, tally)
}

// stageUnresolved stages an event whose venue cannot be made (its area or
// time zone is unknown, or no place was found) for an admin to complete.
func (uc *concertCreationUseCase) stageUnresolved(ctx context.Context, candidate *entity.ConcertCandidate, series *entity.DiscoveredSeries, ev *entity.DiscoveredEvent, tally *ingestTally, reason string) error {
	if err := uc.concertCandidateRepo.Upsert(ctx, candidate); err != nil {
		return fmt.Errorf("upsert concert candidate (%s) %q: %w", reason, series.Title, err)
	}
	tally.staged++
	uc.logger.Info(ctx, "concert candidate staged for review", analyticslog.Attrs(analyticslog.TopicCatalogReview,
		slog.String("artist_id", candidate.ArtistID),
		slog.String("concert_candidate_id", candidate.ID),
		slog.String("series_id", candidate.SeriesID),
		slog.String("listed_venue_name", ev.ListedVenueName),
		slog.String("local_date", ev.LocalDate.Format("2006-01-02")),
		slog.String("reason", reason),
	)...)
	return nil
}

// stageOrPublish runs the shared tail for a resolved-venue event: same-slot
// conflict detection against the stored events and the series' events already
// decided for publication (staging for reconciliation on a conflict — which
// also covers an unknown-start entry that collides with a known-start row),
// and finally the Concert to publish under the group series_id. candidate is
// the row to persist if the event must be staged. It is the single decision
// point shared by the re-discovery fast-path and the general
// Places path, so the two cannot diverge on the human-gate rules.
func (uc *concertCreationUseCase) stageOrPublish(
	ctx context.Context,
	artistID, seriesID string,
	series *entity.DiscoveredSeries,
	ev *entity.DiscoveredEvent,
	venueID string,
	candidate *entity.ConcertCandidate,
	toPublish []*entity.Concert,
	tally *ingestTally,
) (*entity.Concert, error) {
	// Same-slot conflict → stage for reconciliation (no publish). Checked before
	// insert so a colliding event keeps the human gate.
	conflict, err := detectDuplicateEvent(ctx, candidate, venueID, uc.concertRepo)
	if err != nil {
		return nil, fmt.Errorf("detect duplicate event for %q: %w", series.Title, err)
	}
	if conflict == nil {
		conflict = duplicateEventAmong(plannedAt(toPublish, venueID, ev.LocalDate), candidate)
	}
	if conflict != nil {
		if err := uc.concertCandidateRepo.Upsert(ctx, candidate); err != nil {
			return nil, fmt.Errorf("upsert concert candidate (conflict) %q: %w", series.Title, err)
		}
		tally.conflicts++
		uc.logger.Info(ctx, "conflicting concert kept as a candidate for reconciliation", analyticslog.Attrs(analyticslog.TopicCatalogReview,
			slog.String("artist_id", artistID),
			slog.String("concert_candidate_id", candidate.ID),
			slog.String("series_id", seriesID),
			slog.String("existing_event_id", conflict.ID),
			slog.String("local_date", ev.LocalDate.Format("2006-01-02")),
		)...)
		return nil, nil
	}

	return ev.ToConcertUnderSeries(artistID, seriesID, entity.NewID(), venueID), nil
}

// plannedAt returns the events among toPublish at venueID on date.
func plannedAt(toPublish []*entity.Concert, venueID string, date time.Time) []*entity.Event {
	var events []*entity.Event
	for _, c := range toPublish {
		if c.VenueID == venueID && c.LocalDate.Equal(date) {
			events = append(events, &c.Event)
		}
	}
	return events
}

// buildConcertCandidate constructs a ConcertCandidate for one member event of a
// discovered series and the resolved VenuePlace. The series row already exists
// (created when the group's series_id was resolved), so seriesID is carried as a
// real foreign key. Title and source URL are copied from the series for the
// review queue display and rejection log; the canonical values live on the
// series row. The candidate carries the event's admin area and time zone as
// discovered, and the resolved place id when a place was chosen.
func buildConcertCandidate(id, artistID, seriesID string, series *entity.DiscoveredSeries, ev *entity.DiscoveredEvent, place *entity.VenuePlace) *entity.ConcertCandidate {
	candidate := &entity.ConcertCandidate{
		ID:              id,
		ArtistID:        artistID,
		SeriesID:        seriesID,
		Title:           series.Title,
		LocalDate:       ev.LocalDate,
		ListedVenueName: ev.ListedVenueName,
	}
	candidate.StartTime = entity.NullableTime(ev.StartTime)
	candidate.OpenTime = entity.NullableTime(ev.OpenTime)
	if ev.AdminArea != nil {
		a := *ev.AdminArea
		candidate.AdminArea = &a
	}
	if series.SourceURL != "" {
		u := series.SourceURL
		candidate.SourceURL = &u
	}
	if ev.AdminArea != nil && ev.TimeZone != nil {
		z := *ev.TimeZone
		candidate.TimeZone = &z
	}
	// Of the map catalog's answer only the place id is kept; its name and
	// coordinates choose the place and are not stored.
	if place != nil {
		id := place.ProviderUserID
		candidate.ResolvedPlaceID = &id
	}
	return candidate
}

// resolvePlace looks up a Google Places entry for the given venue name.
// It uses a batch-local cache to avoid redundant API calls within one
// CreateFromDiscovered invocation.
//
// The search returns candidates and entity.SelectVenuePlace picks one. It
// returns (place, nil) on success, (nil, nil) when the search found no place, or
// (nil, err) on error. A nil place is NOT a skip signal: the concert is still
// staged for review with its resolved-venue preview absent, so a developer can
// judge a venue that Google Places could not resolve.
func (uc *concertCreationUseCase) resolvePlace(
	ctx context.Context,
	name string,
	adminArea *string,
	cache map[string]*entity.VenuePlace,
) (*entity.VenuePlace, error) {
	// Step 1: batch-local cache.
	if p, ok := cache[venueKey(name, adminArea)]; ok {
		return p, nil
	}

	// Step 2: call Google Places API.
	area := ""
	if adminArea != nil {
		area = *adminArea
	}
	places, err := uc.placeSearcher.SearchPlace(ctx, name, area)
	if err != nil {
		return nil, fmt.Errorf("search place %q: %w", name, err)
	}
	return entity.SelectVenuePlace(name, places), nil
}

// resolveSeriesForGroup resolves the series_id shared by a whole discovered
// series group BEFORE any venue/Places resolution. It adopts the series_id of an
// existing event that matches one of the group's discovered events on BOTH the
// local date AND the normalized listed venue — a true re-discovery of the same
// physical show — otherwise it mints a fresh UUIDv7. It returns the resolved id,
// whether it was adopted, and the existing (artist, date) events found (so the
// caller can skip Places for dates already covered). NO Places API is called:
// listed_venue_name is a raw string carried on both the discovered event and the
// existing row, so the venue match is free.
//
// Matching on (date, venue) — not date alone — prevents an unrelated same-day
// show (a festival slot, a support billing, or a stale fragment at a different
// venue) from hijacking the group's series, and prevents a real multi-date tour
// that merely coincides on one date with an existing SINGLE series from
// inheriting that series' identity for all its dates. After the data-
// consolidation migration at most one distinct existing series matches; if
// several are seen (a residual fragment or a concurrent co-headliner mint) the
// earliest UUIDv7 is adopted as a defensive fallback so the group converges.
func resolveSeriesForGroup(
	ctx context.Context,
	artistID string,
	events []*entity.DiscoveredEvent,
	concertRepo entity.ConcertRepository,
) (seriesID string, existed bool, existing []*entity.Event, err error) {
	dates := make([]time.Time, 0, len(events))
	want := make(map[string]bool, len(events))
	for _, ev := range events {
		dates = append(dates, ev.LocalDate)
		want[entity.DateVenueKey(ev.LocalDate, ev.ListedVenueName)] = true
	}

	existing, err = concertRepo.FindEventsByArtistAndDate(ctx, artistID, dates)
	if err != nil {
		return "", false, nil, fmt.Errorf("find events by artist and date: %w", err)
	}
	for _, e := range existing {
		if e.SeriesID == "" || e.ListedVenueName == nil {
			continue
		}
		if !want[entity.DateVenueKey(e.LocalDate, *e.ListedVenueName)] {
			continue
		}
		if seriesID == "" || e.SeriesID < seriesID {
			seriesID = e.SeriesID
		}
	}
	if seriesID != "" {
		return seriesID, true, existing, nil
	}
	return entity.NewID(), false, existing, nil
}

// eventsAtSameVenue returns the subset of candidate events whose listed venue
// name is the same venue as listedVenueName (entity.SameListedVenue). Used by the discovery
// re-discovery fast-path to decide whether an event can skip the Places API
// (the artist already plays this venue on this date).
func eventsAtSameVenue(cands []*entity.Event, listedVenueName string) []*entity.Event {
	var out []*entity.Event
	for _, ev := range cands {
		if ev.ListedVenueName == nil {
			continue
		}
		if entity.SameListedVenue(*ev.ListedVenueName, listedVenueName) {
			out = append(out, ev)
		}
	}
	return out
}

// insertEventUnderSeries inserts one member event under an already-existing
// series (no series minting, no identity re-derivation) and returns the ids of
// genuinely inserted events (empty when the natural-key UPSERT deduplicated it).
// It is shared by the discovery auto-publish path and the admin approval path so
// the two cannot drift. It carries no series metadata — the series row already
// exists and the repository insert reads only seriesID.
func insertEventUnderSeries(
	ctx context.Context,
	artistID, seriesID string,
	ev *entity.DiscoveredEvent,
	venueID string,
	concertRepo entity.ConcertRepository,
) ([]string, error) {
	concert := ev.ToConcertUnderSeries(artistID, seriesID, entity.NewID(), venueID)
	insertedIDs, err := concertRepo.Create(ctx, concert)
	if err != nil {
		return nil, fmt.Errorf("create concert: %w", err)
	}
	return insertedIDs, nil
}

// resolveExistingEvent finds the already-persisted event a discovered event
// resolves onto, given the candidate events at its (venue, date), comparing
// start times via the canonical [entity.StartKey] (NULLS NOT DISTINCT: an
// unknown start keys as ""):
//   - an exact physical-key match (equal StartKey, incl. both-unknown) → (ev, false);
//   - else a NULL-start row that the entry's known start will fill, not yet
//     claimed by another entry in this batch → (ev, true);
//   - else (genuinely new, or only unrelated known-start sessions exist) → (nil, false).
//
// It is the single matching primitive shared by the re-discovery fast-path, the
// approval duplicate check, and fill detection, so all agree on what "the same
// physical show" means.
func resolveExistingEvent(cands []*entity.Event, ev *entity.DiscoveredEvent, claimedFill map[string]bool) (*entity.Event, bool) {
	incoming := entity.StartKey(entity.NullableTime(ev.StartTime))
	var nullRow *entity.Event
	for _, cand := range cands {
		evStart := entity.StartKey(cand.StartTime)
		if evStart == incoming {
			return cand, false
		}
		if evStart == "" && incoming != "" && !claimedFill[cand.ID] && nullRow == nil {
			nullRow = cand
		}
	}
	if nullRow != nil {
		return nullRow, true
	}
	return nil, false
}

// venueKey returns a composite cache key for the batch-local venue map.
// It combines listed_venue_name and admin_area to prevent collision between
// venues sharing the same name in different regions (e.g. "Zepp" in JP-13 vs JP-27).
func venueKey(name string, adminArea *string) string {
	if adminArea == nil {
		return name + "|"
	}
	return name + "|" + *adminArea
}
