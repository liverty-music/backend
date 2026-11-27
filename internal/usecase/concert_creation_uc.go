package usecase

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/pkg/analyticslog"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-logging/logging"
)

// ConcertCreationUseCase processes discovered concert batches. It decides the
// venue of each found concert, whether it is a concert already stored
// (decideFoundEvent, shared with ConcertCandidateUseCase.Approve), and
// publishes, fills, merges or stages it for admin review.
type ConcertCreationUseCase interface {
	// CreateFromDiscovered processes a batch of scraped concerts for a single
	// artist. Per series it decides each event's Venue (the Artist's event
	// listed under its name, else Google Places), chooses the Series, and
	// then, per event:
	//   - the Artist's event already holds it → nothing is stored;
	//   - it brings the start time of the Artist's unknown-start event → the
	//     time is filled, or that event is merged into another Artist's event
	//     holding the slot under the same name;
	//   - another Artist's discovered event at the same slot and name → the
	//     Artist is added to its performers;
	//   - an unresolved venue or any other collision → staged for review,
	//     resolved later via ConcertCandidateUseCase.Approve;
	//   - otherwise → published. The events of one series are stored in one
	//     Concert.Create call, which announces CONCERT.created so follower
	//     notifications fire immediately.
	CreateFromDiscovered(ctx context.Context, data entity.ConcertDiscoveredData) error

	// RunCreateFromDiscovered is the create-from-discovered workflow a concert
	// search (ConcertUseCase.RunSearch) starts, on [ConcertDiscoveryQueue].
	RunCreateFromDiscovered(ctx context.Context, f Flow, in entity.ConcertDiscoveredData) (struct{}, error)

	// RemoveEmptyDiscoveredSeries removes every discovered Series created
	// more than a day before now with no Event, no ConcertCandidate and no
	// TicketSale (Series.DeleteOrphaned): a Series discovery created whose
	// events were all rejected, merged away or never stored.
	//
	// # Possible errors
	//
	//  - Unavailable: the removal could not run; the next day's run removes
	//    the Series.
	RemoveEmptyDiscoveredSeries(ctx context.Context, now time.Time) error

	// ScheduleEmptySeriesRemoval is the scheduled workflow
	// [WorkflowScheduleEmptySeriesRemoval]: it starts
	// [WorkflowRemoveEmptyDiscoveredSeries] for the scheduled time on
	// [ConcertDiscoveryQueue], so the removal waits for any batch being
	// processed.
	ScheduleEmptySeriesRemoval(ctx context.Context, f Flow, scheduled time.Time) (struct{}, error)

	// RunRemoveEmptyDiscoveredSeries is the workflow
	// [WorkflowRemoveEmptyDiscoveredSeries]: it runs
	// RemoveEmptyDiscoveredSeries as one step.
	RunRemoveEmptyDiscoveredSeries(ctx context.Context, f Flow, scheduled time.Time) (struct{}, error)
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

// CreateFromDiscovered processes a discovered concert batch grouped by series
// (createSeriesFromDiscovered): every event's Venue is decided first, then
// the series' Series is chosen, then each event is decided and the events to
// publish are stored in ONE Concert.Create call, which announces them
// together, so a multi-date tour wakes the notification consumer once. A
// collision at one slot stages that event and never fails the batch. A Series
// the batch leaves empty is removed by RemoveEmptyDiscoveredSeries a day
// later, not here.
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

		if err := uc.createSeriesFromDiscovered(ctx, data.ArtistID, series, newPlaces, tally); err != nil {
			return err
		}
	}
	return nil
}

// placedEvent is a discovered event of a series with its Venue decided.
type placedEvent struct {
	ev *entity.DiscoveredEvent
	// venueID is the event's Venue; "" when it could not be decided.
	venueID string
	// place is the place its venue was looked up as; nil when it reused the
	// Venue of the Artist's event or none was found.
	place *entity.VenuePlace
	// unresolved says why the Venue could not be decided; "" when it was.
	unresolved string
}

// createSeriesFromDiscovered stores one discovered series in two passes
// (design D5 of recognize-same-discovered-concert): it decides the Venue of
// each event, then chooses the series' Series, then decides each event
// (decideFoundEvent) and stores every event to publish in one Concert.Create
// call, which announces them together.
func (uc *concertCreationUseCase) createSeriesFromDiscovered(
	ctx context.Context,
	artistID string,
	series *entity.DiscoveredSeries,
	newPlaces map[string]*entity.VenuePlace,
	tally *ingestTally,
) error {
	artistEvents, err := uc.findArtistEvents(ctx, artistID, series.Events)
	if err != nil {
		return fmt.Errorf("find events of %q: %w", series.Title, err)
	}
	placed, err := uc.placeEvents(ctx, artistID, series, artistEvents, newPlaces)
	if err != nil {
		return err
	}
	seriesID, err := uc.chooseSeries(ctx, artistID, series, placed, artistEvents)
	if err != nil {
		return err
	}

	b := &seriesBatch{artistID: artistID, seriesID: seriesID, series: series, artistEvents: artistEvents, claimed: map[string]bool{}, tally: tally}
	for _, p := range placed {
		if err := uc.decideEvent(ctx, b, p); err != nil {
			return err
		}
	}
	if len(b.toPublish) == 0 {
		return nil
	}

	// One Concert.Create for the series, so it tells the Artist's followers
	// about all of them at once.
	insertedIDs, err := uc.concertRepo.Create(ctx, b.toPublish...)
	if err != nil {
		return fmt.Errorf("auto-publish concerts of %q: %w", series.Title, err)
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
	return nil
}

// findArtistEvents returns the Artist's stored events on the dates of the
// events.
func (uc *concertCreationUseCase) findArtistEvents(ctx context.Context, artistID string, events []*entity.DiscoveredEvent) ([]*entity.Concert, error) {
	dates := make([]time.Time, 0, len(events))
	for _, ev := range events {
		dates = append(dates, ev.LocalDate)
	}
	return uc.concertRepo.FindEventsByArtistAndDate(ctx, artistID, dates)
}

// placeEvents decides the Venue of each event of the series ("Re-discovery
// of the artist's own event" and "Venue resolution"): the Venue of the
// Artist's event on its date listed under its name, without looking it up;
// else the Venue its place resolves to, when it has an area and a time zone.
// An event with no venue name left after normalization is skipped with a
// warning. A failed place search fails the batch before anything is stored
// but venues.
func (uc *concertCreationUseCase) placeEvents(
	ctx context.Context,
	artistID string,
	series *entity.DiscoveredSeries,
	artistEvents []*entity.Concert,
	newPlaces map[string]*entity.VenuePlace,
) ([]placedEvent, error) {
	placed := make([]placedEvent, 0, len(series.Events))
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
		if e := firstOf(artistEvents, func(e *entity.Concert) bool {
			return e.LocalDate.Equal(ev.LocalDate) && e.ListedUnder(ev.ListedVenueName)
		}); e != nil {
			placed = append(placed, placedEvent{ev: ev, venueID: e.VenueID})
			continue
		}
		if ev.AdminArea == nil || ev.TimeZone == nil {
			placed = append(placed, placedEvent{ev: ev, unresolved: "area or time zone unknown"})
			continue
		}
		p, err := uc.placeEvent(ctx, series, ev, newPlaces)
		if err != nil {
			return nil, err
		}
		placed = append(placed, p)
	}
	return placed, nil
}

// placeEvent looks up the venue of an event with an area and a time zone and
// finds or creates its Venue.
func (uc *concertCreationUseCase) placeEvent(ctx context.Context, series *entity.DiscoveredSeries, ev *entity.DiscoveredEvent, newPlaces map[string]*entity.VenuePlace) (placedEvent, error) {
	place, err := uc.resolvePlace(ctx, ev.ListedVenueName, ev.AdminArea, newPlaces)
	if err != nil {
		return placedEvent{}, fmt.Errorf("resolve venue %q: %w", ev.ListedVenueName, err)
	}
	// Cache the result (nil or not) so subsequent events in the same batch with
	// the same normalized venue name skip the Places API entirely.
	newPlaces[venueKey(ev.ListedVenueName, ev.AdminArea)] = place
	// Unresolved venue: staged for review. Auto-publishing here would mint a
	// coordinate-less venue from the raw name and publish it unreviewed.
	if place == nil {
		return placedEvent{ev: ev, unresolved: "unresolved venue"}, nil
	}
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
		return placedEvent{}, fmt.Errorf("resolve or create venue for %q: %w", series.Title, err)
	}
	return placedEvent{ev: ev, venueID: venueID, place: place}, nil
}

// chooseSeries chooses the series' Series ("One Series per discovered
// series"): the Series of an event the Artist performs at alone, on the date
// of one of the series' events and listed under its name or at its Venue
// (the smallest id); else, for a TOUR, the Artist's upcoming discovered tour
// of the same title (the smallest id); else a new Series, created now so it
// exists before any event references it.
func (uc *concertCreationUseCase) chooseSeries(
	ctx context.Context,
	artistID string,
	series *entity.DiscoveredSeries,
	placed []placedEvent,
	artistEvents []*entity.Concert,
) (string, error) {
	if id := seriesOfSameShow(placed, artistEvents); id != "" {
		return id, nil
	}
	if series.Type == entity.SeriesTypeTour {
		id, err := uc.upcomingTourOfSameTitle(ctx, artistID, series.Title)
		if err != nil {
			return "", err
		}
		if id != "" {
			return id, nil
		}
	}
	// Create is idempotent (ON CONFLICT (id) DO NOTHING) so a redelivered
	// message is harmless.
	row := &entity.Series{ID: entity.NewID(), Title: series.Title, Type: series.Type, SourceURL: series.SourceURL}
	if _, err := uc.seriesRepo.Create(ctx, row); err != nil {
		return "", fmt.Errorf("create series %q: %w", series.Title, err)
	}
	return row.ID, nil
}

// seriesOfSameShow returns the smallest Series id among the discovered events
// the Artist performs at alone that are on the date of one of the placed events
// and listed under its name or at its Venue; "" when there is none. An event
// the Artist shares with another Artist may be in that Artist's Series, so it
// never pulls the series in.
func seriesOfSameShow(placed []placedEvent, artistEvents []*entity.Concert) string {
	seriesID := ""
	for _, e := range artistEvents {
		// An Organizer's Series holds only the Organizer's events.
		if len(e.Artists) != 1 || e.SeriesID == "" || e.PublishState != nil {
			continue
		}
		matches := slices.ContainsFunc(placed, func(p placedEvent) bool {
			return e.LocalDate.Equal(p.ev.LocalDate) &&
				(e.ListedUnder(p.ev.ListedVenueName) || (p.venueID != "" && e.VenueID == p.venueID))
		})
		if matches && (seriesID == "" || e.SeriesID < seriesID) {
			seriesID = e.SeriesID
		}
	}
	return seriesID
}

// upcomingTourOfSameTitle returns the smallest id among the Artist's
// upcoming discovered tours of the same tour title ([entity.SameTourTitle]);
// "" when there is none.
func (uc *concertCreationUseCase) upcomingTourOfSameTitle(ctx context.Context, artistID, title string) (string, error) {
	tours, err := uc.seriesRepo.ListUpcomingToursByArtist(ctx, artistID, uc.clock())
	if err != nil {
		return "", fmt.Errorf("list upcoming tours for %q: %w", title, err)
	}
	seriesID := ""
	for _, t := range tours {
		if entity.SameTourTitle(t.Title, title) && (seriesID == "" || t.ID < seriesID) {
			seriesID = t.ID
		}
	}
	return seriesID, nil
}

// seriesBatch is what deciding the events of one series accumulates.
type seriesBatch struct {
	artistID string
	seriesID string
	series   *entity.DiscoveredSeries
	// artistEvents are the Artist's events on the series' dates, kept
	// current with what the series filled, merged and plans to publish.
	artistEvents []*entity.Concert
	// claimed are the unknown-start events this series already filled, so
	// one is filled by at most one of its events.
	claimed map[string]bool
	// toPublish are the Concerts stored together in one Concert.Create call.
	toPublish []*entity.Concert
	tally     *ingestTally
}

// decideEvent decides one placed event of the series and does what the
// decision says: nothing, fill, merge, stage, or collect a Concert to
// publish.
func (uc *concertCreationUseCase) decideEvent(ctx context.Context, b *seriesBatch, p placedEvent) error {
	ev := p.ev
	candidate := buildConcertCandidate(entity.NewID(), b.artistID, b.seriesID, b.series, ev, p.place)
	if p.unresolved != "" {
		return uc.stage(ctx, b, candidate, nil, p.unresolved)
	}
	venueEvents, err := uc.concertRepo.FindEventsByVenueAndDate(ctx, []string{p.venueID}, []time.Time{ev.LocalDate})
	if err != nil {
		return fmt.Errorf("find events at the venue of %q: %w", b.series.Title, err)
	}
	found := foundEvent{artistID: b.artistID, listedVenueName: ev.ListedVenueName, venueID: p.venueID, localDate: ev.LocalDate, startTime: ev.StartTime}
	d := decideFoundEvent(found, b.artistEvents, venueEvents, b.claimed)
	if d.outcome == outcomePublish || d.outcome == outcomeAddPerformer {
		if planned := collidingPlanned(b.toPublish, found); planned != nil {
			return uc.stage(ctx, b, candidate, &planned.Event, "collides with an event of the batch")
		}
	}

	switch d.outcome {
	case outcomeNothing:
		b.claimMatched(d.event, ev.StartTime)
		return nil
	case outcomeFill:
		return uc.fill(ctx, b, candidate, d.event, ev)
	case outcomeMergeInto:
		err := uc.concertRepo.Merge(ctx, d.source.ID, d.event.ID)
		if errors.Is(err, apperr.ErrNotFound) || errors.Is(err, apperr.ErrFailedPrecondition) {
			// Another run or an approval merged or removed one of them
			// meanwhile, or the Artist's event is the last of a Series with
			// ticket sales: an admin decides instead of the batch failing.
			return uc.stage(ctx, b, candidate, &d.event.Event, "not merged")
		}
		if err != nil {
			return fmt.Errorf("merge event %s into %s: %w", d.source.ID, d.event.ID, err)
		}
		b.artistEvents = append(b.artistEvents, d.event)
		uc.logger.Info(ctx, "merged the artist's event into another artist's event at the same slot",
			slog.String("artist_id", b.artistID),
			slog.String("merged_event_id", d.source.ID),
			slog.String("event_id", d.event.ID),
		)
		return nil
	case outcomeStage:
		return uc.stage(ctx, b, candidate, &d.event.Event, "collision")
	case outcomeAddPerformer:
		c := ev.ToConcertUnderSeries(b.artistID, d.event.SeriesID, entity.NewID(), p.venueID)
		c.StartTime = d.event.StartTime
		b.plan(c)
		return nil
	default:
		b.plan(ev.ToConcertUnderSeries(b.artistID, b.seriesID, entity.NewID(), p.venueID))
		return nil
	}
}

// claimMatched notes that an Organizer's unknown-start event stands for the
// found start time, as a filled event does: a second found start time of the
// batch is a new event.
func (b *seriesBatch) claimMatched(e *entity.Concert, found *entity.TimeOfDay) {
	if e.StartTime == nil && found != nil {
		b.claimed[e.ID] = true
	}
}

// plan collects a Concert to publish. It also counts among the Artist's
// events, so a later event of the series at the same time at another Venue
// is staged.
func (b *seriesBatch) plan(c *entity.Concert) {
	b.toPublish = append(b.toPublish, c)
	b.artistEvents = append(b.artistEvents, c)
}

// collidingPlanned returns the Concert already decided for publication in
// this series that the found event collides with: the same Venue and date
// and the same start time (or both unknown), or, with no found start, any
// start time.
func collidingPlanned(toPublish []*entity.Concert, found foundEvent) *entity.Concert {
	return firstOf(toPublish, func(c *entity.Concert) bool {
		return c.VenueID == found.venueID && c.LocalDate.Equal(found.localDate) &&
			(c.HasStartTime(found.startTime) || (found.startTime == nil && c.StartTime != nil))
	})
}

// fill fills the found start and open time onto the Artist's unknown-start
// event. Another event taking the slot meanwhile (AlreadyExists) stages the
// event instead of failing the batch.
func (uc *concertCreationUseCase) fill(ctx context.Context, b *seriesBatch, candidate *entity.ConcertCandidate, target *entity.Concert, ev *entity.DiscoveredEvent) error {
	b.claimed[target.ID] = true
	err := uc.concertRepo.FillEventStartTimes(ctx, []string{target.ID}, []*entity.TimeOfDay{ev.StartTime}, []*entity.TimeOfDay{ev.OpenTime})
	if errors.Is(err, apperr.ErrAlreadyExists) {
		return uc.stage(ctx, b, candidate, &target.Event, "slot taken while filling")
	}
	if err != nil {
		return fmt.Errorf("fill event start times: %w", err)
	}
	for _, e := range b.artistEvents {
		if e.ID == target.ID {
			e.StartTime = ev.StartTime
		}
	}
	b.tally.filled++
	uc.logger.Info(ctx, "filled existing event start time on re-discovery (no new concert)",
		slog.String("artist_id", b.artistID),
		slog.String("event_id", target.ID),
		slog.String("local_date", ev.LocalDate.Format("2006-01-02")),
	)
	return nil
}

// stage keeps the event as a ConcertCandidate for an admin: one whose venue
// cannot be made (conflict nil), or one colliding with conflict.
func (uc *concertCreationUseCase) stage(ctx context.Context, b *seriesBatch, candidate *entity.ConcertCandidate, conflict *entity.Event, reason string) error {
	if err := uc.concertCandidateRepo.Upsert(ctx, candidate); err != nil {
		return fmt.Errorf("upsert concert candidate (%s) %q: %w", reason, b.series.Title, err)
	}
	attrs := []slog.Attr{
		slog.String("artist_id", candidate.ArtistID),
		slog.String("concert_candidate_id", candidate.ID),
		slog.String("series_id", candidate.SeriesID),
		slog.String("listed_venue_name", candidate.ListedVenueName),
		slog.String("local_date", candidate.LocalDate.Format("2006-01-02")),
		slog.String("reason", reason),
	}
	if conflict == nil {
		b.tally.staged++
		uc.logger.Info(ctx, "concert candidate staged for review", analyticslog.Attrs(analyticslog.TopicCatalogReview, attrs...)...)
		return nil
	}
	b.tally.conflicts++
	attrs = append(attrs, slog.String("existing_event_id", conflict.ID))
	uc.logger.Info(ctx, "conflicting concert kept as a candidate for reconciliation", analyticslog.Attrs(analyticslog.TopicCatalogReview, attrs...)...)
	return nil
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
	candidate.StartTime = ev.StartTime
	candidate.OpenTime = ev.OpenTime
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
// The search returns candidates and entity.SelectVenuePlace picks one inside
// adminArea. It
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
	return entity.SelectVenuePlace(name, adminArea, places), nil
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
