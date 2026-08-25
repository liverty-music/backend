package usecase

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-logging/logging"
)

// ConcertCreationUseCase processes discovered concert batches. It resolves each
// concert's venue and branches the outcome: a genuinely new concert at a resolved
// venue is auto-published, a same-slot conflict or an unresolved venue is staged
// for admin review, and a suppressed natural key is skipped entirely.
type ConcertCreationUseCase interface {
	// CreateFromDiscovered processes a batch of scraped concerts for a single
	// artist. For each concert it resolves a venue via Google Places API, then:
	//   - unresolved venue → staged for review (no venues row, no publish);
	//   - resolved venue whose natural key is suppressed → skipped (no publish,
	//     no stage), so an operator's deletion is not undone by re-discovery;
	//   - resolved venue with a same-slot conflict → staged for reconciliation
	//     (no publish), resolved later via AdminConcertUseCase.Approve;
	//   - resolved venue, genuinely new → auto-published: the
	//     series/events/event_performers rows are inserted and CONCERT.created is
	//     published so follower notifications fire immediately.
	CreateFromDiscovered(ctx context.Context, data entity.ConcertDiscoveredData) error
}

// concertCreationUseCase implements ConcertCreationUseCase. It reuses the
// package-level venue/duplicate helpers shared with AdminConcertUseCase.Approve
// so discovery and approval agree on venue resolution and conflict detection.
type concertCreationUseCase struct {
	stagedConcertRepo     entity.StagedConcertRepository
	venueRepo             entity.VenueRepository
	concertRepo           entity.ConcertRepository
	seriesRepo            entity.SeriesRepository
	suppressedConcertRepo entity.SuppressedConcertRepository
	placeSearcher         entity.VenuePlaceSearcher
	publisher             EventPublisher
	logger                *logging.Logger
}

// Compile-time interface compliance check.
var _ ConcertCreationUseCase = (*concertCreationUseCase)(nil)

// NewConcertCreationUseCase creates a new ConcertCreationUseCase.
// placeSearcher must not be nil; panics if not provided.
func NewConcertCreationUseCase(
	stagedConcertRepo entity.StagedConcertRepository,
	venueRepo entity.VenueRepository,
	concertRepo entity.ConcertRepository,
	seriesRepo entity.SeriesRepository,
	suppressedConcertRepo entity.SuppressedConcertRepository,
	placeSearcher entity.VenuePlaceSearcher,
	publisher EventPublisher,
	logger *logging.Logger,
) ConcertCreationUseCase {
	if placeSearcher == nil {
		panic("placeSearcher is required")
	}
	return &concertCreationUseCase{
		stagedConcertRepo:     stagedConcertRepo,
		venueRepo:             venueRepo,
		concertRepo:           concertRepo,
		seriesRepo:            seriesRepo,
		suppressedConcertRepo: suppressedConcertRepo,
		placeSearcher:         placeSearcher,
		publisher:             publisher,
		logger:                logger,
	}
}

// CreateFromDiscovered processes a discovered concert batch grouped by series.
// The series_id shared by a whole group is resolved ONCE (adopt an existing
// event's series, else mint), the series row is created eagerly, then each
// member event is resolved and branched between auto-publish, staging, and
// suppression — all under the same series_id.
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
//  4. Resolve/create the venues row, then consult the suppression set — a
//     deleted-then-rediscovered natural key is skipped entirely.
//  5. Same-slot conflict → stage for reconciliation (no publish).
//  6. Genuinely new → insert the event under the group series_id.
//
// After all events of a series are processed, ONE CONCERT.created is published
// carrying every newly-inserted event id, so a multi-date tour wakes the
// notification consumer once. A lightweight cleanup at the end removes any
// series left with no events and no pending staged rows (the provisional-mint or
// all-staged-then-rejected case).
func (uc *concertCreationUseCase) CreateFromDiscovered(ctx context.Context, data entity.ConcertDiscoveredData) error {
	// Batch-local place cache: (listed_venue_name, admin_area) → *VenuePlace.
	// Avoids redundant Places API calls for the same venue within one batch,
	// shared across every series of this artist's batch.
	newPlaces := make(map[string]*entity.VenuePlace)

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

		mintedID, err := uc.createSeriesFromDiscovered(ctx, data.ArtistID, series, newPlaces)
		if err != nil {
			return err
		}
		if mintedID != "" {
			mintedSeriesIDs = append(mintedSeriesIDs, mintedID)
		}
	}

	// Sweep only the series THIS batch minted that ended up with no events and no
	// pending staged rows (a provisional group mint that turned out to be a
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
// series row when minted, persists every publishable member event under that
// series_id (staging the rest), and publishes ONE CONCERT.created for all newly
// inserted events of this series. It returns the series_id it minted (empty when
// it adopted an existing series), so the caller can scope the orphan sweep.
func (uc *concertCreationUseCase) createSeriesFromDiscovered(
	ctx context.Context,
	artistID string,
	series *entity.DiscoveredSeries,
	newPlaces map[string]*entity.VenuePlace,
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

	var insertedIDs []string
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

		ids, err := uc.persistDiscoveredEvent(ctx, artistID, seriesID, series, ev, existingByDate[ev.LocalDate.Format("2006-01-02")], claimedFill, newPlaces)
		if err != nil {
			return "", err
		}
		insertedIDs = append(insertedIDs, ids...)
	}

	// Publish ONE CONCERT.created per discovered series carrying every newly
	// inserted event id, so a multi-date tour wakes the notification consumer
	// once rather than per event.
	if len(insertedIDs) > 0 {
		created := ConcertCreatedData{
			ArtistID:   artistID,
			ConcertIDs: insertedIDs,
		}
		if err := uc.publisher.PublishEvent(ctx, entity.SubjectConcertCreated, created); err != nil {
			uc.logger.Error(ctx, "failed to publish CONCERT.created after auto-publish", err,
				slog.String("artist_id", artistID),
				slog.String("series_id", seriesID),
			)
			// Non-fatal: events are persisted; a missed notification retries next run.
		}
		uc.logger.Info(ctx, "auto-published new concerts for series",
			slog.String("artist_id", artistID),
			slog.String("series_id", seriesID),
			slog.String("title", series.Title),
			slog.Int("inserted", len(insertedIDs)),
		)
	}

	return mintedID, nil
}

// persistDiscoveredEvent resolves and persists a single member event under the
// already-resolved group series_id, returning the ids of any genuinely inserted
// events (zero for a re-discovery, fill, suppression, or staged outcome).
func (uc *concertCreationUseCase) persistDiscoveredEvent(
	ctx context.Context,
	artistID, seriesID string,
	series *entity.DiscoveredSeries,
	ev *entity.DiscoveredEvent,
	sameDateEvents []*entity.Event,
	claimedFill map[string]bool,
	newPlaces map[string]*entity.VenuePlace,
) ([]string, error) {
	// Re-discovery fast-path: an existing (artist, date) event at the same listed
	// venue is a re-discovery. Skip the Places API entirely; fill a known start
	// onto an unknown-start row when applicable, and insert nothing new. Only a
	// new date — or a listed venue not among the artist's existing events on this
	// date — falls through to venue resolution below.
	if sameVenue := eventsAtSameVenue(sameDateEvents, ev.ListedVenueName); len(sameVenue) > 0 {
		match, isFill := resolveExistingEvent(sameVenue, ev, claimedFill)
		if match != nil {
			if isFill {
				claimedFill[match.ID] = true
				if err := uc.concertRepo.FillEventStartTimes(ctx,
					[]string{match.ID},
					[]*time.Time{entity.NullableTime(ev.StartTime)},
					[]*time.Time{entity.NullableTime(ev.OpenTime)},
				); err != nil {
					return nil, fmt.Errorf("fill event start times: %w", err)
				}
				uc.logger.Info(ctx, "filled existing event start time on re-discovery (no new concert)",
					slog.String("artist_id", artistID),
					slog.String("local_date", ev.LocalDate.Format("2006-01-02")),
				)
			}
			// Exact re-discovery (or fill) — no new event, no Places call.
			return nil, nil
		}
		// A genuinely new start time at a venue the artist already plays on this
		// date: reuse the known venue_id (no Places call), but still run the SAME
		// suppression + duplicate-staging gate as the general path — so an
		// unknown-start entry colliding with a known-start row stages for review
		// instead of inserting a phantom NULL-start duplicate.
		venueID := sameVenue[0].VenueID
		staged := buildStagedConcert(entity.NewID(), artistID, seriesID, series, ev, nil)
		return uc.stageOrInsert(ctx, artistID, seriesID, series, ev, venueID, staged)
	}

	place, err := uc.resolvePlace(ctx, ev.ListedVenueName, ev.AdminArea, newPlaces)
	if err != nil {
		return nil, fmt.Errorf("resolve venue %q: %w", ev.ListedVenueName, err)
	}
	// Cache the result (nil or not) so subsequent events in the same batch with
	// the same normalized venue name skip the Places API entirely.
	newPlaces[venueKey(ev.ListedVenueName, ev.AdminArea)] = place

	staged := buildStagedConcert(entity.NewID(), artistID, seriesID, series, ev, place)

	// Unresolved venue → stage for review. Auto-publishing here would mint a
	// coordinate-less venue from the raw name and publish it unreviewed.
	if place == nil {
		if err := uc.stagedConcertRepo.Upsert(ctx, staged); err != nil {
			return nil, fmt.Errorf("upsert staged concert (unresolved venue) %q: %w", series.Title, err)
		}
		uc.logger.Info(ctx, "staged concert with unresolved venue for review",
			slog.String("artist_id", artistID),
			slog.String("staged_concert_id", staged.ID),
			slog.String("series_id", seriesID),
			slog.String("listed_venue_name", ev.ListedVenueName),
			slog.String("local_date", ev.LocalDate.Format("2006-01-02")),
		)
		return nil, nil
	}

	// Resolved venue: resolve or create the venues row (created only here, on the
	// path that may auto-publish — never for a conflict or unresolved row).
	venueID, err := resolveOrCreateVenue(ctx, staged, uc.venueRepo, uc.logger)
	if err != nil {
		return nil, fmt.Errorf("resolve or create venue for %q: %w", series.Title, err)
	}

	return uc.stageOrInsert(ctx, artistID, seriesID, series, ev, venueID, staged)
}

// stageOrInsert runs the shared tail for a resolved-venue event: the suppression
// gate, then same-slot conflict detection (staging for reconciliation on a
// conflict — which also covers an unknown-start entry that collides with an
// existing known-start row), and finally a genuinely-new insert under the group
// series_id. staged is the row to persist if the event must be staged. It is the
// single decision point shared by the re-discovery fast-path and the general
// Places path, so the two cannot diverge on the human-gate rules.
func (uc *concertCreationUseCase) stageOrInsert(
	ctx context.Context,
	artistID, seriesID string,
	series *entity.DiscoveredSeries,
	ev *entity.DiscoveredEvent,
	venueID string,
	staged *entity.StagedConcert,
) ([]string, error) {
	// Suppression gate: a deleted-then-rediscovered slot is skipped entirely.
	suppressed, err := uc.suppressedConcertRepo.Exists(ctx, venueID, ev.LocalDate, entity.NullableTime(ev.StartTime))
	if err != nil {
		return nil, fmt.Errorf("check suppression for %q: %w", series.Title, err)
	}
	if suppressed {
		uc.logger.Info(ctx, "skipping suppressed concert (deleted by operator)",
			slog.String("artist_id", artistID),
			slog.String("venue_id", venueID),
			slog.String("local_date", ev.LocalDate.Format("2006-01-02")),
		)
		return nil, nil
	}

	// Same-slot conflict → stage for reconciliation (no publish). Checked before
	// insert so a colliding event keeps the human gate.
	conflict, err := detectDuplicateEvent(ctx, staged, venueID, uc.concertRepo)
	if err != nil {
		return nil, fmt.Errorf("detect duplicate event for %q: %w", series.Title, err)
	}
	if conflict != nil {
		if err := uc.stagedConcertRepo.Upsert(ctx, staged); err != nil {
			return nil, fmt.Errorf("upsert staged concert (conflict) %q: %w", series.Title, err)
		}
		uc.logger.Info(ctx, "staged conflicting concert for reconciliation",
			slog.String("artist_id", artistID),
			slog.String("staged_concert_id", staged.ID),
			slog.String("series_id", seriesID),
			slog.String("existing_event_id", conflict.ID),
			slog.String("local_date", ev.LocalDate.Format("2006-01-02")),
		)
		return nil, nil
	}

	insertedIDs, err := insertEventUnderSeries(ctx, artistID, seriesID, ev, venueID, uc.concertRepo)
	if err != nil {
		return nil, fmt.Errorf("auto-publish concert %q: %w", series.Title, err)
	}
	return insertedIDs, nil
}

// buildStagedConcert constructs a StagedConcert for one member event of a
// discovered series and the resolved VenuePlace. The series row already exists
// (created when the group's series_id was resolved), so seriesID is carried as a
// real foreign key. Title and source URL are copied from the series for the
// review queue display and rejection log; the canonical values live on the
// series row. When place is nil the resolved_* fields stay nil (the venue could
// not be resolved).
func buildStagedConcert(id, artistID, seriesID string, series *entity.DiscoveredSeries, ev *entity.DiscoveredEvent, place *entity.VenuePlace) *entity.StagedConcert {
	staged := &entity.StagedConcert{
		ID:              id,
		ArtistID:        artistID,
		SeriesID:        seriesID,
		Title:           series.Title,
		LocalDate:       ev.LocalDate,
		ListedVenueName: ev.ListedVenueName,
	}
	staged.StartTime = entity.NullableTime(ev.StartTime)
	staged.OpenTime = entity.NullableTime(ev.OpenTime)
	if ev.AdminArea != nil {
		a := *ev.AdminArea
		staged.AdminArea = &a
	}
	if series.SourceURL != "" {
		u := series.SourceURL
		staged.SourceURL = &u
	}
	if place != nil {
		staged.ResolvedPlaceID = &place.ExternalID
		staged.ResolvedVenueName = &place.Name
		if place.Coordinates != nil {
			lat := place.Coordinates.Latitude
			lng := place.Coordinates.Longitude
			staged.ResolvedLatitude = &lat
			staged.ResolvedLongitude = &lng
		}
	}
	return staged
}

// resolvePlace looks up a Google Places entry for the given venue name.
// It uses a batch-local cache to avoid redundant API calls within one
// CreateFromDiscovered invocation.
//
// Returns (place, nil) on success, (nil, nil) when the place is not found, or
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
	place, err := uc.placeSearcher.SearchPlace(ctx, name, area)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("search place %q: %w", name, err)
	}
	return place, nil
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
		want[groupVenueDateKey(ev.LocalDate, ev.ListedVenueName)] = true
	}

	existing, err = concertRepo.FindEventsByArtistAndDate(ctx, artistID, dates)
	if err != nil {
		return "", false, nil, fmt.Errorf("find events by artist and date: %w", err)
	}
	for _, e := range existing {
		if e.SeriesID == "" || e.ListedVenueName == nil {
			continue
		}
		if !want[groupVenueDateKey(e.LocalDate, *e.ListedVenueName)] {
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

// groupVenueDateKey is the (local date, normalized listed venue) key used to
// match a discovered event against an existing event for series adoption.
func groupVenueDateKey(d time.Time, listedVenueName string) string {
	return d.Format("2006-01-02") + "|" + entity.NormalizeVenueName(listedVenueName)
}

// eventsAtSameVenue returns the subset of candidate events whose listed venue
// name matches listedVenueName under NormalizeVenueName. Used by the discovery
// re-discovery fast-path to decide whether an event can skip the Places API
// (the artist already plays this venue on this date).
func eventsAtSameVenue(cands []*entity.Event, listedVenueName string) []*entity.Event {
	target := entity.NormalizeVenueName(listedVenueName)
	var out []*entity.Event
	for _, ev := range cands {
		if ev.ListedVenueName == nil {
			continue
		}
		if entity.NormalizeVenueName(*ev.ListedVenueName) == target {
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
