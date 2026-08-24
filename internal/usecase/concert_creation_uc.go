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

// CreateFromDiscovered processes a discovered concert batch, resolving each
// concert's venue and branching between auto-publish, staging, and suppression.
//
// Per concert:
//  1. Resolve the venue via Google Places (batch-local cache avoids repeat calls).
//  2. Unresolved venue → stage for review (no venues row, no publish): an
//     unresolved venue is inherently unconfident, so it keeps the human gate.
//  3. Resolve/create the venues row, then consult the suppression set — a
//     deleted-then-rediscovered natural key is skipped entirely.
//  4. Same-slot conflict → stage for reconciliation (no publish).
//  5. Genuinely new → auto-publish (insert series/events/performers) and publish
//     CONCERT.created. The known-start fill of an existing unknown-start row
//     inserts nothing and publishes no new-concert event.
func (uc *concertCreationUseCase) CreateFromDiscovered(ctx context.Context, data entity.ConcertDiscoveredData) error {
	// Batch-local place cache: (listed_venue_name, admin_area) → *VenuePlace.
	// Avoids redundant Places API calls for the same venue within one batch.
	type placeKey = string
	newPlaces := make(map[placeKey]*entity.VenuePlace)

	for _, sc := range data.Concerts {
		sc.ListedVenueName = entity.NormalizeVenueName(sc.ListedVenueName)
		if sc.ListedVenueName == "" {
			uc.logger.Warn(ctx, "skipping discovered concert: empty venue name from Gemini",
				slog.String("artist_id", data.ArtistID),
				slog.String("title", sc.Title),
				slog.Any("admin_area", sc.AdminArea),
				slog.String("local_date", sc.LocalDate.Format("2006-01-02")),
			)
			continue
		}
		if sc.Title == "" {
			uc.logger.Warn(ctx, "skipping discovered concert: empty title from Gemini",
				slog.String("artist_id", data.ArtistID),
				slog.String("listed_venue_name", sc.ListedVenueName),
				slog.String("local_date", sc.LocalDate.Format("2006-01-02")),
			)
			continue
		}

		place, err := uc.resolvePlace(ctx, sc.ListedVenueName, sc.AdminArea, newPlaces)
		if err != nil {
			return fmt.Errorf("resolve venue %q: %w", sc.ListedVenueName, err)
		}
		// Cache the result (nil or not) so subsequent concerts in the same batch
		// with the same normalized venue name skip the Places API entirely.
		newPlaces[venueKey(sc.ListedVenueName, sc.AdminArea)] = place

		staged := buildStagedConcert(entity.NewID(), data.ArtistID, sc, place)

		// Unresolved venue → stage for review. Auto-publishing here would mint a
		// coordinate-less venue from the raw name and publish it unreviewed.
		if place == nil {
			if err := uc.stagedConcertRepo.Upsert(ctx, staged); err != nil {
				return fmt.Errorf("upsert staged concert (unresolved venue) %q: %w", sc.Title, err)
			}
			uc.logger.Info(ctx, "staged concert with unresolved venue for review",
				slog.String("artist_id", data.ArtistID),
				slog.String("staged_concert_id", staged.ID),
				slog.String("listed_venue_name", sc.ListedVenueName),
				slog.String("local_date", sc.LocalDate.Format("2006-01-02")),
			)
			continue
		}

		// Resolved venue: resolve or create the venues row (created only here, on
		// the path that may auto-publish — never for a conflict or unresolved row).
		venueID, err := resolveOrCreateVenue(ctx, staged, uc.venueRepo, uc.logger)
		if err != nil {
			return fmt.Errorf("resolve or create venue for %q: %w", sc.Title, err)
		}

		// Suppression gate: a deleted-then-rediscovered slot is skipped entirely so
		// an operator's deletion is not undone by the next discovery run.
		suppressed, err := uc.suppressedConcertRepo.Exists(ctx, venueID, staged.LocalDate, staged.StartTime)
		if err != nil {
			return fmt.Errorf("check suppression for %q: %w", sc.Title, err)
		}
		if suppressed {
			uc.logger.Info(ctx, "skipping suppressed concert (deleted by operator)",
				slog.String("artist_id", data.ArtistID),
				slog.String("venue_id", venueID),
				slog.String("local_date", sc.LocalDate.Format("2006-01-02")),
			)
			continue
		}

		// Same-slot conflict → stage for reconciliation (no publish).
		conflict, err := detectDuplicateEvent(ctx, staged, venueID, uc.concertRepo)
		if err != nil {
			return fmt.Errorf("detect duplicate event for %q: %w", sc.Title, err)
		}
		if conflict != nil {
			if err := uc.stagedConcertRepo.Upsert(ctx, staged); err != nil {
				return fmt.Errorf("upsert staged concert (conflict) %q: %w", sc.Title, err)
			}
			uc.logger.Info(ctx, "staged conflicting concert for reconciliation",
				slog.String("artist_id", data.ArtistID),
				slog.String("staged_concert_id", staged.ID),
				slog.String("existing_event_id", conflict.ID),
				slog.String("local_date", sc.LocalDate.Format("2006-01-02")),
			)
			continue
		}

		// Genuinely new at a resolved venue → auto-publish. buildAndInsertConcerts
		// also covers the known-start fill (fills an existing unknown-start row and
		// inserts nothing); that path publishes no new-concert event.
		insertedIDs, err := buildAndInsertConcerts(ctx, data.ArtistID, sc, venueID, uc.seriesRepo, uc.concertRepo, uc.logger)
		if err != nil {
			return fmt.Errorf("auto-publish concert %q: %w", sc.Title, err)
		}
		if len(insertedIDs) == 0 {
			uc.logger.Info(ctx, "filled existing event start time (no new concert published)",
				slog.String("artist_id", data.ArtistID),
				slog.String("local_date", sc.LocalDate.Format("2006-01-02")),
			)
			continue
		}

		created := ConcertCreatedData{
			ArtistID:   data.ArtistID,
			ConcertIDs: insertedIDs,
		}
		if err := uc.publisher.PublishEvent(ctx, entity.SubjectConcertCreated, created); err != nil {
			uc.logger.Error(ctx, "failed to publish CONCERT.created after auto-publish", err,
				slog.String("artist_id", data.ArtistID),
			)
			// Non-fatal: events are persisted; a missed notification retries next run.
		}

		uc.logger.Info(ctx, "auto-published new concert",
			slog.String("artist_id", data.ArtistID),
			slog.String("local_date", sc.LocalDate.Format("2006-01-02")),
			slog.Int("inserted", len(insertedIDs)),
		)
	}

	return nil
}

// buildStagedConcert constructs a StagedConcert from a scraped concert and the
// resolved VenuePlace. When place is nil the resolved_* fields stay nil (the
// venue could not be resolved — this path is only reached when the caller has
// already decided not to skip the entry).
func buildStagedConcert(id, artistID string, sc *entity.ScrapedConcert, place *entity.VenuePlace) *entity.StagedConcert {
	staged := &entity.StagedConcert{
		ID:              id,
		ArtistID:        artistID,
		Title:           sc.Title,
		LocalDate:       sc.LocalDate,
		ListedVenueName: sc.ListedVenueName,
	}
	staged.StartTime = entity.NullableTime(sc.StartTime)
	staged.OpenTime = entity.NullableTime(sc.OpenTime)
	if sc.AdminArea != nil {
		a := *sc.AdminArea
		staged.AdminArea = &a
	}
	if sc.SourceURL != "" {
		u := sc.SourceURL
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

// buildAndInsertConcerts creates the venues row (if needed), mints series and
// event UUIDs, bulk-inserts series + events + performers, and returns the
// event IDs of genuinely inserted concerts.
//
// This helper is shared by AdminConcertUseCase.Approve. It replicates the
// Phase 2-4 logic from the pre-approval-gate CreateFromDiscovered, minus the
// venue-resolution step (already done at staging time).
//
// sc carries the approved scraped data; resolvedVenueID is the venues.id of
// the resolved (or newly created) venue for this concert.
func buildAndInsertConcerts(
	ctx context.Context,
	artistID string,
	sc *entity.ScrapedConcert,
	resolvedVenueID string,
	seriesRepo entity.SeriesRepository,
	concertRepo entity.ConcertRepository,
	logger *logging.Logger,
) ([]string, error) {
	// Fetch existing events at (venue, date) to adopt series and detect fills.
	existingEvents, err := concertRepo.FindEventsByVenueAndDate(ctx,
		[]string{resolvedVenueID},
		[]time.Time{sc.LocalDate},
	)
	if err != nil {
		return nil, fmt.Errorf("find existing events: %w", err)
	}
	existingByVenueDate := make(map[string][]*entity.Event, len(existingEvents))
	for _, ev := range existingEvents {
		k := venueDateKey(ev.VenueID, ev.LocalDate)
		existingByVenueDate[k] = append(existingByVenueDate[k], ev)
	}

	// Track known start times at this (venue, date) from existing DB rows.
	knownStartAt := make(map[string]bool)
	for _, ev := range existingEvents {
		if ev.StartTime != nil && !ev.StartTime.IsZero() {
			knownStartAt[venueDateKey(ev.VenueID, ev.LocalDate)] = true
		}
	}
	if !sc.StartTime.IsZero() {
		knownStartAt[venueDateKey(resolvedVenueID, sc.LocalDate)] = true
	}

	// Determine series type and match against existing events.
	seriesType := entity.SeriesTypeSingle
	if sc.IsTour {
		seriesType = entity.SeriesTypeTour
	}

	claimedFill := make(map[string]bool)
	cands := existingByVenueDate[venueDateKey(resolvedVenueID, sc.LocalDate)]
	match, isFill := resolveExistingEvent(cands, sc, claimedFill)

	// If an unknown-start concert exists but a known-start row already covers
	// this (venue, date), skip to avoid creating a phantom NULL-start duplicate.
	if sc.StartTime.IsZero() && knownStartAt[venueDateKey(resolvedVenueID, sc.LocalDate)] && match == nil {
		logger.Warn(ctx, "skipping approve: unknown-start concert, known-start row already exists",
			slog.String("artist_id", artistID),
			slog.String("title", sc.Title),
			slog.String("listed_venue_name", sc.ListedVenueName),
			slog.String("local_date", sc.LocalDate.Format("2006-01-02")),
		)
		return nil, nil
	}

	// Resolve or mint the series ID.
	seriesID := ""
	minted := false
	if match != nil {
		seriesID = match.SeriesID
	}
	if seriesID == "" {
		seriesID = entity.NewID()
		minted = true
	}

	// Fill start_at on the existing unknown-start row if this approved concert
	// carries a known start time.
	if isFill {
		if err := concertRepo.FillEventStartTimes(ctx,
			[]string{match.ID},
			[]*time.Time{entity.NullableTime(sc.StartTime)},
			[]*time.Time{entity.NullableTime(sc.OpenTime)},
		); err != nil {
			return nil, fmt.Errorf("fill event start times: %w", err)
		}
	}

	// Insert the series row only when we minted a new one.
	if minted {
		series := &entity.Series{
			ID:        seriesID,
			Title:     sc.Title,
			Type:      seriesType,
			SourceURL: sc.SourceURL,
		}
		if _, err := seriesRepo.Create(ctx, series); err != nil {
			return nil, fmt.Errorf("create series: %w", err)
		}
	}

	concert := sc.ToConcert(artistID, seriesID, entity.NewID(), resolvedVenueID, seriesType)

	insertedIDs, err := concertRepo.Create(ctx, concert)
	if err != nil {
		return nil, fmt.Errorf("create concert: %w", err)
	}

	return insertedIDs, nil
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
