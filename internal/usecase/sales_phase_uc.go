package usecase

import (
	"context"
	"log/slog"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-logging/logging"
)

// SalesPhaseDiscoveryUseCase enumerates upcoming series for followed artists,
// calls the sales-phase searcher once per series, upserts the results, and
// publishes a SALES_PHASE.discovered event for each brand-new phase.
type SalesPhaseDiscoveryUseCase interface {
	// DiscoverForArtist runs the full discovery pipeline for one artist: list
	// their upcoming concerts, group by series, search each series, upsert.
	// Returns the number of new phases announced.
	DiscoverForArtist(ctx context.Context, artist *entity.Artist) (int, error)
}

type salesPhaseDiscoveryUseCase struct {
	concertRepo    entity.ConcertRepository
	salesPhaseRepo entity.SalesPhaseRepository
	searcher       entity.SalesPhaseSearcher
	publisher      EventPublisher
	window         time.Duration
	logger         *logging.Logger
}

// Compile-time interface compliance check.
var _ SalesPhaseDiscoveryUseCase = (*salesPhaseDiscoveryUseCase)(nil)

// NewSalesPhaseDiscoveryUseCase wires the discovery use case.
//
// window is the look-ahead used to filter upcoming concerts from the database.
// Phases whose covered events all lie beyond the window are still persisted —
// the window only controls which series are included in the discovery run.
func NewSalesPhaseDiscoveryUseCase(
	concertRepo entity.ConcertRepository,
	salesPhaseRepo entity.SalesPhaseRepository,
	searcher entity.SalesPhaseSearcher,
	publisher EventPublisher,
	window time.Duration,
	logger *logging.Logger,
) SalesPhaseDiscoveryUseCase {
	return &salesPhaseDiscoveryUseCase{
		concertRepo:    concertRepo,
		salesPhaseRepo: salesPhaseRepo,
		searcher:       searcher,
		publisher:      publisher,
		window:         window,
		logger:         logger,
	}
}

// DiscoverForArtist implements [SalesPhaseDiscoveryUseCase].
//
// Pipeline:
//  1. List upcoming concerts for the artist (upcomingOnly = true).
//  2. Group concerts by series; build a SalesSeriesCandidate per series with
//     the series' candidate events (id, date, venue, admin_area).
//  3. For each series call SalesPhaseSearcher.SearchSalesPhases.
//  4. For each candidate returned: call Upsert. If the outcome is
//     UpsertOutcomeInserted, publish a SALES_PHASE.discovered event.
func (uc *salesPhaseDiscoveryUseCase) DiscoverForArtist(ctx context.Context, artist *entity.Artist) (int, error) {
	attrs := []slog.Attr{
		slog.String("artist_id", artist.ID),
		slog.String("artist_name", artist.Name),
	}
	uc.logger.Info(ctx, "sales_phase_discovery: starting for artist", attrs...)

	concerts, err := uc.concertRepo.ListByArtist(ctx, artist.ID, true)
	if err != nil {
		return 0, err
	}
	if len(concerts) == 0 {
		uc.logger.Info(ctx, "sales_phase_discovery: no upcoming concerts for artist", attrs...)
		return 0, nil
	}

	// Group concerts by series. We use a stable insertion-order slice so the
	// series are processed in a deterministic order (by first encounter).
	type seriesEntry struct {
		candidate entity.SalesSeriesCandidate
	}
	order := make([]string, 0)
	bySeriesID := make(map[string]*seriesEntry)

	for _, c := range concerts {
		if c.SeriesID == "" || c.Series == nil {
			continue
		}
		// Skip events beyond the discovery window. Allow zero window (process all).
		if uc.window > 0 && c.LocalDate.After(time.Now().UTC().Add(uc.window)) {
			continue
		}
		e, ok := bySeriesID[c.SeriesID]
		if !ok {
			e = &seriesEntry{
				candidate: entity.SalesSeriesCandidate{
					SeriesID:    c.SeriesID,
					SeriesTitle: c.Series.Title,
					ArtistName:  artist.Name,
				},
			}
			bySeriesID[c.SeriesID] = e
			order = append(order, c.SeriesID)
		}
		ce := &entity.SalesPhaseCandidateEvent{
			EventID:   c.ID,
			LocalDate: c.LocalDate,
		}
		if c.ListedVenueName != nil {
			ce.ListedVenueName = *c.ListedVenueName
		}
		if c.Venue != nil && c.Venue.AdminArea != nil {
			ce.AdminArea = *c.Venue.AdminArea
		}
		e.candidate.CandidateEvents = append(e.candidate.CandidateEvents, ce)
	}

	if len(order) == 0 {
		uc.logger.Info(ctx, "sales_phase_discovery: no series in window for artist", attrs...)
		return 0, nil
	}

	var totalNew int
	for _, seriesID := range order {
		// Respect cancellation between series.
		if ctx.Err() != nil {
			break
		}
		entry := bySeriesID[seriesID]
		sc := entry.candidate

		seriesAttrs := append(attrs,
			slog.String("series_id", sc.SeriesID),
			slog.String("series_title", sc.SeriesTitle),
		)
		uc.logger.Info(ctx, "sales_phase_discovery: searching series", seriesAttrs...)

		candidates, err := uc.searcher.SearchSalesPhases(
			ctx,
			sc.ArtistName,
			sc.SeriesTitle,
			sc.SeriesID,
			sc.CandidateEvents,
		)
		if err != nil {
			uc.logger.Error(ctx, "sales_phase_discovery: searcher failed for series", err, seriesAttrs...)
			// Non-fatal: continue with the next series.
			continue
		}

		uc.logger.Info(ctx, "sales_phase_discovery: searcher returned candidates",
			append(seriesAttrs, slog.Int("count", len(candidates)))...)

		for _, candidate := range candidates {
			phaseID, outcome, err := uc.salesPhaseRepo.Upsert(ctx, candidate)
			if err != nil {
				uc.logger.Error(ctx, "sales_phase_discovery: upsert failed", err,
					append(seriesAttrs, slog.String("anchor_event_id", candidate.AnchorEventID))...)
				continue
			}
			if outcome == entity.UpsertOutcomeInserted {
				// Announce only genuinely new phases; re-discovery is silent.
				uc.publishDiscovered(ctx, phaseID, candidate, seriesAttrs)
				totalNew++
			}
		}
	}

	uc.logger.Info(ctx, "sales_phase_discovery: complete for artist",
		append(attrs,
			slog.Int("series_count", len(order)),
			slog.Int("new_phases", totalNew),
		)...)
	return totalNew, nil
}

// publishDiscovered publishes a SALES_PHASE.discovered event. Failure is
// logged as a warning and swallowed — announcement is best-effort; the
// phase is already persisted.
func (uc *salesPhaseDiscoveryUseCase) publishDiscovered(
	ctx context.Context,
	phaseID string,
	c *entity.SalesPhaseCandidate,
	attrs []slog.Attr,
) {
	data := entity.SalesPhaseDiscoveredData{
		PhaseID:         phaseID,
		SeriesID:        c.SeriesID,
		CoveredEventIDs: c.CoveredEventIDs,
	}
	if err := uc.publisher.PublishEvent(ctx, entity.SubjectSalesPhaseDiscovered, data); err != nil {
		uc.logger.Warn(ctx, "sales_phase_discovery: failed to publish SALES_PHASE.discovered",
			append(attrs, slog.String("error", err.Error()))...)
	}
}
