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
	"github.com/pannpers/go-logging/logging"
)

// ConcertCreationUseCase defines the interface for processing discovered concert
// batches. It resolves venues, persists concerts, and publishes downstream events.
type ConcertCreationUseCase interface {
	// CreateFromDiscovered processes a batch of scraped concerts for a single artist.
	// For each concert it resolves a venue via Google Places API, builds concert
	// entities, bulk-inserts them, and publishes a concert.created.v1 event.
	// Concerts whose venues cannot be resolved are skipped with a structured log.
	CreateFromDiscovered(ctx context.Context, data entity.ConcertDiscoveredData) error
}

// concertCreationUseCase implements ConcertCreationUseCase.
type concertCreationUseCase struct {
	venueRepo     entity.VenueRepository
	seriesRepo    entity.SeriesRepository
	concertRepo   entity.ConcertRepository
	placeSearcher entity.VenuePlaceSearcher
	publisher     EventPublisher
	logger        *logging.Logger
}

// Compile-time interface compliance check.
var _ ConcertCreationUseCase = (*concertCreationUseCase)(nil)

// NewConcertCreationUseCase creates a new ConcertCreationUseCase.
// placeSearcher must not be nil; panics if not provided.
func NewConcertCreationUseCase(
	venueRepo entity.VenueRepository,
	seriesRepo entity.SeriesRepository,
	concertRepo entity.ConcertRepository,
	placeSearcher entity.VenuePlaceSearcher,
	publisher EventPublisher,
	logger *logging.Logger,
) ConcertCreationUseCase {
	if placeSearcher == nil {
		panic("placeSearcher is required")
	}
	return &concertCreationUseCase{
		venueRepo:     venueRepo,
		seriesRepo:    seriesRepo,
		concertRepo:   concertRepo,
		placeSearcher: placeSearcher,
		publisher:     publisher,
		logger:        logger,
	}
}

// CreateFromDiscovered processes a discovered concert batch: resolves venues,
// persists concerts, and publishes downstream events.
//
// Each scraped concert is paired with a freshly-generated Series row (1:1
// fallback with SeriesType=SINGLE). Smarter series grouping — folding multiple
// stops of the same tour under one Series — is deferred to a follow-up change
// (auto-discovery-series-grouping). The data model already supports the
// eventual N:1 grouping; only the discovery prompt and grouping heuristic are
// missing.
func (uc *concertCreationUseCase) CreateFromDiscovered(ctx context.Context, data entity.ConcertDiscoveredData) error {
	// Phase 1 — resolve a venue for each scraped concert, dropping entries that
	// cannot be persisted (empty title/venue, or a venue Google Places cannot
	// resolve). The grouping markers (IsTour / TourGroup) ride along on sc so
	// the build phase can fold a tour's stops under one Series.
	type resolvedConcert struct {
		sc      *entity.ScrapedConcert
		venueID string
	}
	var entries []resolvedConcert
	newVenues := make(map[string]*entity.Venue) // track newly created venues by cache key

	for _, sc := range data.Concerts {
		if sc.ListedVenueName == "" {
			// Skip TBA-venue entries; an empty text_query returns 400 from Places and would poison the batch.
			uc.logger.Warn(ctx, "skipping concert: empty venue name from Gemini",
				slog.String("artist_id", data.ArtistID),
				slog.String("title", sc.Title),
				slog.Any("admin_area", sc.AdminArea),
				slog.String("local_date", sc.LocalDate.Format("2006-01-02")),
				slog.Time("start_time", sc.StartTime),
				slog.Time("open_time", sc.OpenTime),
				slog.String("source_url", sc.SourceURL),
			)
			continue
		}
		if sc.Title == "" {
			// Skip empty-title entries: SeriesRepository.Create rejects
			// `Title == ""` with InvalidArgument, which propagates out of
			// CreateFromDiscovered and aborts the entire Pub/Sub batch —
			// every valid concert in the same message is then lost on the
			// Pub/Sub retry. Per-concert skip with a Warn keeps the rest
			// of the batch persisting and surfaces the upstream data issue.
			uc.logger.Warn(ctx, "skipping concert: empty title from Gemini",
				slog.String("artist_id", data.ArtistID),
				slog.String("listed_venue_name", sc.ListedVenueName),
				slog.String("local_date", sc.LocalDate.Format("2006-01-02")),
				slog.String("source_url", sc.SourceURL),
			)
			continue
		}
		venueID, venue, skip, err := uc.resolveVenue(ctx, sc.ListedVenueName, sc.AdminArea, newVenues)
		if err != nil {
			return fmt.Errorf("resolve venue %q: %w", sc.ListedVenueName, err)
		}
		if skip {
			uc.logger.Warn(ctx, "skipping concert: venue not found in Google Places",
				slog.String("artist_id", data.ArtistID),
				slog.String("title", sc.Title),
				slog.String("listed_venue_name", sc.ListedVenueName),
				slog.Any("admin_area", sc.AdminArea),
				slog.String("local_date", sc.LocalDate.Format("2006-01-02")),
				slog.Any("start_time", sc.StartTime),
				slog.Any("open_time", sc.OpenTime),
				slog.String("source_url", sc.SourceURL),
			)
			continue
		}

		if venue != nil {
			newVenues[venueKey(sc.ListedVenueName, sc.AdminArea)] = venue
		}
		entries = append(entries, resolvedConcert{sc: sc, venueID: venueID})
	}
	if len(entries) == 0 {
		return nil
	}

	// Phase 2 — fetch existing events at the resolved (venue, date) pairs. They
	// drive two decisions below: adopting a parent Series from already-persisted
	// member events, and filling a later-announced start_at onto a row first
	// seen without one (instead of inserting a duplicate).
	venueIDs := make([]string, len(entries))
	dates := make([]time.Time, len(entries))
	for i, e := range entries {
		venueIDs[i] = e.venueID
		dates[i] = e.sc.LocalDate
	}
	existingEvents, err := uc.concertRepo.FindEventsByVenueAndDate(ctx, venueIDs, dates)
	if err != nil {
		return fmt.Errorf("find existing events for resolution: %w", err)
	}
	existingByVenueDate := make(map[string][]*entity.Event, len(existingEvents))
	for _, ev := range existingEvents {
		k := venueDateKey(ev.VenueID, ev.LocalDate)
		existingByVenueDate[k] = append(existingByVenueDate[k], ev)
	}

	// knownStartAt records (venue, date) coordinates that already have a
	// known-start representation — either an existing DB row or another entry in
	// THIS batch. An unknown-start entry at such a coordinate is redundant: the
	// show is represented by the known-start row, and a (venue, date, NULL) key
	// can never dedup onto a known-start row, so building it would only insert a
	// phantom NULL-start duplicate. It is skipped (see Phase 4). Computed over
	// both sources so the decision is independent of intra-batch entry order.
	knownStartAt := make(map[string]bool)
	for _, ev := range existingEvents {
		if ev.StartTime != nil && !ev.StartTime.IsZero() {
			knownStartAt[venueDateKey(ev.VenueID, ev.LocalDate)] = true
		}
	}
	for _, e := range entries {
		if !e.sc.StartTime.IsZero() {
			knownStartAt[venueDateKey(e.venueID, e.sc.LocalDate)] = true
		}
	}

	// Phase 3 — group resolved entries: tour stops sharing a TourGroup fold into
	// one group; every standalone is its own group. The handle is intra-run
	// only; the persisted Series identity is adopted from existing events (or
	// minted) per group below.
	type concertGroup struct {
		entries []resolvedConcert
		isTour  bool
	}
	var groupOrder []string
	groups := make(map[string]*concertGroup)
	for i, e := range entries {
		groupKey := fmt.Sprintf("S%d", i) // standalone: unique per entry
		if e.sc.IsTour && e.sc.TourGroup > 0 {
			groupKey = fmt.Sprintf("T%d", e.sc.TourGroup)
		}
		g, ok := groups[groupKey]
		if !ok {
			g = &concertGroup{isTour: e.sc.IsTour}
			groups[groupKey] = g
			groupOrder = append(groupOrder, groupKey)
		}
		g.entries = append(g.entries, e)
	}

	// Phase 4 — for each group, adopt or mint a Series and build Concert rows.
	var concerts []*entity.Concert
	var seriesBatch []*entity.Series
	var fillIDs []string
	var fillStarts, fillOpens []*time.Time
	// claimedFill tracks NULL-start rows already claimed for a fill in this
	// batch, so two known-start entries at the same (venue, date) (e.g. a
	// matinee and an evening announced together against one unknown-start row)
	// do not both target the same row with conflicting times.
	claimedFill := make(map[string]bool)

	for _, groupKey := range groupOrder {
		g := groups[groupKey]
		seriesType := entity.SeriesTypeSingle
		if g.isTour {
			seriesType = entity.SeriesTypeTour
		}

		// Resolve each member once against the existing events at its (venue,
		// date), capturing the action. Claiming fills here (sequentially) means
		// two known starts at the same (venue, date) cannot target one
		// unknown-start row. The adopted series is the series of the first member
		// that resolves onto an existing event — same physical show (exact key)
		// or the unknown-start row a known start fills. Resolving on the physical
		// identity (not just (venue, date)) keeps an unrelated show at the same
		// venue/date but a different start time from being merged in; a genuine
		// sibling 昼夜 stop of the SAME tour still shares the series via the group.
		type resolution struct {
			entry  resolvedConcert
			match  *entity.Event
			isFill bool
		}
		resolutions := make([]resolution, 0, len(g.entries))
		seriesID := ""
		for _, e := range g.entries {
			cands := existingByVenueDate[venueDateKey(e.venueID, e.sc.LocalDate)]
			match, isFill := resolveExistingEvent(cands, e.sc, claimedFill)
			if match != nil {
				if seriesID == "" {
					seriesID = match.SeriesID
				}
				if isFill {
					claimedFill[match.ID] = true
				}
			}
			resolutions = append(resolutions, resolution{entry: e, match: match, isFill: isFill})
		}
		minted := seriesID == ""
		if minted {
			id, err := uuid.NewV7()
			if err != nil {
				return fmt.Errorf("generate series ID: %w", err)
			}
			seriesID = id.String()
		}

		seriesAdded := false
		for _, r := range resolutions {
			// An unknown-start entry whose physical key is (venue, date, NULL)
			// can only dedup onto an existing NULL-start row (NULLS NOT
			// DISTINCT). If a known-start representation already exists at this
			// (venue, date) — in the DB or elsewhere in this batch — the show is
			// that known-start row, which a NULL-start key can never match, so
			// building this entry would only insert a phantom NULL-start
			// duplicate. Skip and log (accepted residual; the artist cannot be
			// pinned to a session without a time). When NO known start exists,
			// the entry is built: it either attaches to an existing unknown-start
			// row via Create's UPSERT (the co-headliner-with-unknown-start case)
			// or is a genuinely new unknown-time show.
			if r.entry.sc.StartTime.IsZero() && knownStartAt[venueDateKey(r.entry.venueID, r.entry.sc.LocalDate)] {
				uc.logger.Warn(ctx, "skipping unknown-start concert: a known-start row already represents this venue/date (cannot pin to a session)",
					slog.String("artist_id", data.ArtistID),
					slog.String("title", r.entry.sc.Title),
					slog.String("listed_venue_name", r.entry.sc.ListedVenueName),
					slog.String("local_date", r.entry.sc.LocalDate.Format("2006-01-02")),
				)
				continue
			}

			// Fill: a known start resolving onto an unknown-start row fills that
			// row (already claimed during resolution). Create's UPSERT then dedups
			// this entry onto the filled row and the performer JOIN attaches the artist.
			if r.isFill {
				fillIDs = append(fillIDs, r.match.ID)
				fillStarts = append(fillStarts, entity.NullableTime(r.entry.sc.StartTime))
				fillOpens = append(fillOpens, entity.NullableTime(r.entry.sc.OpenTime))
			}

			eventID, err := uuid.NewV7()
			if err != nil {
				return fmt.Errorf("generate concert ID: %w", err)
			}
			concert := r.entry.sc.ToConcert(data.ArtistID, seriesID, eventID.String(), r.entry.venueID, seriesType)
			concerts = append(concerts, concert)
			// Only minted series need a row inserted; an adopted series already
			// exists. ToConcert builds the shell Series; add it once per group
			// so the FK from events.series_id is satisfied before Create runs.
			if minted && !seriesAdded {
				seriesBatch = append(seriesBatch, concert.Series)
				seriesAdded = true
			}
		}
	}

	if len(concerts) == 0 {
		return nil
	}

	// Fill later-announced start times onto rows first seen without one, before
	// Create so its UPSERT dedups the filled entries onto those rows.
	if len(fillIDs) > 0 {
		if err := uc.concertRepo.FillEventStartTimes(ctx, fillIDs, fillStarts, fillOpens); err != nil {
			return fmt.Errorf("fill event start times: %w", err)
		}
	}

	// Bulk insert series first so events.series_id FK is satisfied. Orphan
	// series (series rows whose concerts later fail to insert) are accepted as
	// a known minor cost of running across two repository transactions; the
	// alternative (a unit-of-work passing tx between repos) is heavier than
	// this code path warrants today.
	if len(seriesBatch) > 0 {
		if _, err := uc.seriesRepo.Create(ctx, seriesBatch...); err != nil {
			return fmt.Errorf("create series: %w", err)
		}
	}

	// Bulk insert concerts (ON CONFLICT DO NOTHING handles duplicates).
	// insertedIDs holds only the IDs that were actually persisted — natural-
	// key UPSERT conflicts keep the pre-existing event's id, so the phantom
	// UUIDs generated above are filtered out by Create.
	var insertedIDs []string
	if len(concerts) > 0 {
		ids, err := uc.concertRepo.Create(ctx, concerts...)
		if err != nil {
			return fmt.Errorf("create concerts: %w", err)
		}
		insertedIDs = ids
	}

	uc.logger.Info(ctx, "concerts persisted",
		slog.String("artist_id", data.ArtistID),
		slog.Int("requested", len(concerts)),
		slog.Int("inserted", len(insertedIDs)),
	)

	// Publish concert.created.v1 only when at least one concert was genuinely
	// inserted. Emitting the event with phantom UUIDs would cause downstream
	// NotifyNewConcerts to reject the batch with InvalidArgument.
	if len(insertedIDs) > 0 {
		createdData := ConcertCreatedData{
			ArtistID:   data.ArtistID,
			ConcertIDs: insertedIDs,
		}
		if err := uc.publishEvent(ctx, entity.SubjectConcertCreated, createdData); err != nil {
			uc.logger.Error(ctx, "failed to publish concert.created event", err,
				slog.String("artist_id", data.ArtistID),
			)
			// Non-fatal: concerts are already persisted.
		}
	}

	return nil
}

// resolveVenue resolves a venue for a scraped concert.
//
// Resolution strategy:
//  1. Check batch-local cache by listed_venue_name (avoids any I/O for same-batch duplicates).
//  2. Look up existing venue by listed_venue_name in the database (avoids Places API for known venues).
//  3. Call Google Places API to get canonical place_id.
//  4. Look up existing venue by google_place_id in the database (dedup across different listed names).
//  5. If not found, create a new venue from the Places result.
//
// Returns skip=true when Places API returns NotFound, signalling the caller
// to skip the concert. Non-nil *Venue is returned only when a new venue was created.
func (uc *concertCreationUseCase) resolveVenue(
	ctx context.Context,
	name string,
	adminArea *string,
	newVenues map[string]*entity.Venue,
) (string, *entity.Venue, bool, error) {
	// Step 1: Check batch-local cache by listed_venue_name + admin_area.
	if v, ok := newVenues[venueKey(name, adminArea)]; ok {
		return v.ID, nil, false, nil
	}

	// Step 2: Look up existing venue by listed_venue_name in DB (skips Places API).
	existing, err := uc.venueRepo.GetByListedName(ctx, name, adminArea)
	if err == nil {
		return existing.ID, nil, false, nil
	}
	if !errors.Is(err, apperr.ErrNotFound) {
		return "", nil, false, fmt.Errorf("get venue by listed name: %w", err)
	}

	// Step 3: Call Google Places API.
	area := ""
	if adminArea != nil {
		area = *adminArea
	}
	place, err := uc.placeSearcher.SearchPlace(ctx, name, area)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return "", nil, true, nil
		}
		return "", nil, false, fmt.Errorf("search place %q: %w", name, err)
	}

	// Step 4: Look up existing venue by google_place_id (handles different listed names for same place).
	existingByPlaceID, err := uc.venueRepo.GetByPlaceID(ctx, place.ExternalID)
	if err == nil {
		return existingByPlaceID.ID, nil, false, nil
	}
	if !errors.Is(err, apperr.ErrNotFound) {
		return "", nil, false, fmt.Errorf("get venue by place ID: %w", err)
	}

	// Step 5: Create new venue from Places API result.
	id, venue, err := uc.createVenueFromPlace(ctx, name, adminArea, place)
	if err != nil {
		return "", nil, false, err
	}
	return id, venue, false, nil
}

// createVenueFromPlace creates a new venue using canonical data from the Google Places API.
func (uc *concertCreationUseCase) createVenueFromPlace(
	ctx context.Context,
	listedName string,
	adminArea *string,
	place *entity.VenuePlace,
) (string, *entity.Venue, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", nil, fmt.Errorf("generate venue ID: %w", err)
	}

	venue := &entity.Venue{
		ID:              id.String(),
		Name:            place.Name,
		AdminArea:       adminArea,
		GooglePlaceID:   &place.ExternalID,
		Coordinates:     place.Coordinates,
		ListedVenueName: &listedName,
	}

	if err := uc.venueRepo.Create(ctx, venue); err != nil {
		return "", nil, fmt.Errorf("create venue: %w", err)
	}

	uc.logger.Info(ctx, "created new venue from Google Places",
		slog.String("venue_id", venue.ID),
		slog.String("venue_name", place.Name),
		slog.String("raw_name", listedName),
		slog.String("place_id", place.ExternalID),
	)

	return venue.ID, venue, nil
}

// publishEvent publishes data as a CloudEvent to the given subject.
func (uc *concertCreationUseCase) publishEvent(ctx context.Context, subject string, data any) error {
	return uc.publisher.PublishEvent(ctx, subject, data)
}

// venueDateKey returns the index key for grouping existing events by their
// physical (venue, date) coordinate during discovery-time resolution.
func venueDateKey(venueID string, d time.Time) string {
	return venueID + "|" + d.Format("2006-01-02")
}

// resolveExistingEvent finds the already-persisted event a scraped concert
// resolves onto, given the candidate events at its (venue, date), comparing
// start times via the canonical [entity.StartKey] (NULLS NOT DISTINCT: an
// unknown start keys as ""):
//   - an exact physical-key match (equal StartKey, incl. both-unknown) → (ev, false);
//   - else a NULL-start row that the entry's known start will fill, not yet
//     claimed by another entry in this batch → (ev, true);
//   - else (genuinely new, or only unrelated known-start sessions exist) → (nil, false).
//
// It is the single matching primitive shared by series adoption, the
// unknown-start skip decision, and fill detection, so all three agree on what
// "the same physical show" means.
func resolveExistingEvent(cands []*entity.Event, sc *entity.ScrapedConcert, claimedFill map[string]bool) (*entity.Event, bool) {
	incoming := entity.StartKey(entity.NullableTime(sc.StartTime))
	var nullRow *entity.Event
	for _, ev := range cands {
		evStart := entity.StartKey(ev.StartTime)
		if evStart == incoming {
			return ev, false
		}
		if evStart == "" && incoming != "" && !claimedFill[ev.ID] && nullRow == nil {
			nullRow = ev
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
