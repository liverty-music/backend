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
// batches. It resolves venues, stages concerts for approval, and does NOT
// publish CONCERT.created directly — that happens on Approve.
type ConcertCreationUseCase interface {
	// CreateFromDiscovered processes a batch of scraped concerts for a single
	// artist. For each concert it resolves a venue via Google Places API and
	// stages the result in staged_concerts for admin review. Concerts whose
	// venues cannot be resolved are skipped with a structured log. CONCERT.created
	// is NOT published here; it is published only when a staged row is approved
	// via ConcertApprovalUseCase.Approve.
	CreateFromDiscovered(ctx context.Context, data entity.ConcertDiscoveredData) error
}

// concertCreationUseCase implements ConcertCreationUseCase.
type concertCreationUseCase struct {
	stagedConcertRepo entity.StagedConcertRepository
	placeSearcher     entity.VenuePlaceSearcher
	logger            *logging.Logger
}

// Compile-time interface compliance check.
var _ ConcertCreationUseCase = (*concertCreationUseCase)(nil)

// NewConcertCreationUseCase creates a new ConcertCreationUseCase.
// placeSearcher must not be nil; panics if not provided.
func NewConcertCreationUseCase(
	stagedConcertRepo entity.StagedConcertRepository,
	placeSearcher entity.VenuePlaceSearcher,
	logger *logging.Logger,
) ConcertCreationUseCase {
	if placeSearcher == nil {
		panic("placeSearcher is required")
	}
	return &concertCreationUseCase{
		stagedConcertRepo: stagedConcertRepo,
		placeSearcher:     placeSearcher,
		logger:            logger,
	}
}

// CreateFromDiscovered processes a discovered concert batch: resolves venues
// and stages each concert for admin approval.
//
// Venue resolution strategy (same as before):
//  1. Call Google Places API to get canonical place_id, name, and coordinates.
//  2. If NotFound, skip the concert with a Warn.
//  3. Denormalise the resolved venue fields onto the staged_concerts row.
//
// No venues row is created here. No events, series, or performers are
// inserted. No CONCERT.created event is published.
func (uc *concertCreationUseCase) CreateFromDiscovered(ctx context.Context, data entity.ConcertDiscoveredData) error {
	// Batch-local place cache: (listed_venue_name, admin_area) → *VenuePlace.
	// Avoids redundant Places API calls for the same venue within one batch.
	type placeKey = string
	newPlaces := make(map[placeKey]*entity.VenuePlace)

	for _, sc := range data.Concerts {
		if sc.ListedVenueName == "" {
			uc.logger.Warn(ctx, "skipping staged concert: empty venue name from Gemini",
				slog.String("artist_id", data.ArtistID),
				slog.String("title", sc.Title),
				slog.Any("admin_area", sc.AdminArea),
				slog.String("local_date", sc.LocalDate.Format("2006-01-02")),
			)
			continue
		}
		if sc.Title == "" {
			uc.logger.Warn(ctx, "skipping staged concert: empty title from Gemini",
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

		// A nil place means Google Places could not resolve the venue. We still
		// stage the concert (with its resolved-venue preview absent) so a
		// developer can review a venue the searcher could not resolve.
		if place != nil {
			newPlaces[venueKey(sc.ListedVenueName, sc.AdminArea)] = place
		} else {
			uc.logger.Info(ctx, "staging concert with unresolved venue for review",
				slog.String("artist_id", data.ArtistID),
				slog.String("title", sc.Title),
				slog.String("listed_venue_name", sc.ListedVenueName),
				slog.Any("admin_area", sc.AdminArea),
				slog.String("local_date", sc.LocalDate.Format("2006-01-02")),
			)
		}

		id, err := uuid.NewV7()
		if err != nil {
			return fmt.Errorf("generate staged concert ID: %w", err)
		}

		staged := buildStagedConcert(id.String(), data.ArtistID, sc, place)

		if err := uc.stagedConcertRepo.Upsert(ctx, staged); err != nil {
			return fmt.Errorf("upsert staged concert %q: %w", sc.Title, err)
		}

		uc.logger.Info(ctx, "staged concert queued for approval",
			slog.String("artist_id", data.ArtistID),
			slog.String("staged_concert_id", staged.ID),
			slog.String("title", sc.Title),
			slog.String("local_date", sc.LocalDate.Format("2006-01-02")),
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
// This helper is shared by ConcertApprovalUseCase.Approve. It replicates the
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
		sid, err := uuid.NewV7()
		if err != nil {
			return nil, fmt.Errorf("generate series ID: %w", err)
		}
		seriesID = sid.String()
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

	eventID, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("generate event ID: %w", err)
	}
	concert := sc.ToConcert(artistID, seriesID, eventID.String(), resolvedVenueID, seriesType)

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
