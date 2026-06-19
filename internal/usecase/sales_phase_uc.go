package usecase

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
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
	artistRepo     entity.ArtistRepository
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
// window is the look-ahead used to filter upcoming concerts from the database;
// it only controls which series are included in the discovery run.
func NewSalesPhaseDiscoveryUseCase(
	concertRepo entity.ConcertRepository,
	artistRepo entity.ArtistRepository,
	salesPhaseRepo entity.SalesPhaseRepository,
	searcher entity.SalesPhaseSearcher,
	publisher EventPublisher,
	window time.Duration,
	logger *logging.Logger,
) SalesPhaseDiscoveryUseCase {
	return &salesPhaseDiscoveryUseCase{
		concertRepo:    concertRepo,
		artistRepo:     artistRepo,
		salesPhaseRepo: salesPhaseRepo,
		searcher:       searcher,
		publisher:      publisher,
		window:         window,
		logger:         logger,
	}
}

// DiscoverForArtist implements [SalesPhaseDiscoveryUseCase].
//
// Pipeline (ONE grounded search per artist):
//  1. List the artist's upcoming concerts and group them into series refs
//     (series_id, title, known event dates).
//  2. Resolve the artist's official-site URL (the grounding seed) and the
//     incremental lower bound (last-searched timestamp minus an overlap margin).
//  3. Call SalesPhaseSearcher.SearchSalesPhases ONCE for the artist.
//  4. Upsert each returned candidate; publish SALES_PHASE.discovered for newly
//     inserted phases. On success, record the new last-searched timestamp.
func (uc *salesPhaseDiscoveryUseCase) DiscoverForArtist(ctx context.Context, artist *entity.Artist) (int, error) {
	now := time.Now().UTC()
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

	// Group concerts into series refs (stable insertion order), collecting each
	// series' known upcoming event dates for the model to disambiguate.
	order := make([]string, 0)
	bySeriesID := make(map[string]*entity.SalesSeriesRef)
	for _, c := range concerts {
		if c.SeriesID == "" || c.Series == nil {
			continue
		}
		if uc.window > 0 && c.LocalDate.After(now.Add(uc.window)) {
			continue
		}
		ref, ok := bySeriesID[c.SeriesID]
		if !ok {
			ref = &entity.SalesSeriesRef{SeriesID: c.SeriesID, Title: c.Series.Title}
			bySeriesID[c.SeriesID] = ref
			order = append(order, c.SeriesID)
		}
		ref.EventDates = append(ref.EventDates, c.LocalDate)
	}
	if len(order) == 0 {
		uc.logger.Info(ctx, "sales_phase_discovery: no series in window for artist", attrs...)
		return 0, nil
	}
	seriesRefs := make([]*entity.SalesSeriesRef, len(order))
	for i, sid := range order {
		seriesRefs[i] = bySeriesID[sid]
	}

	// Resolve the grounding seed URL. Without a usable official-site URL the
	// searcher cannot ground, so skip the artist (benign) rather than burn a
	// grounding call on an empty seed. A missing row is NotFound, not an infra
	// error; only genuine infra errors propagate (and let the job's circuit
	// breaker handle systemic failures).
	site, err := uc.artistRepo.GetOfficialSite(ctx, artist.ID)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			uc.logger.Warn(ctx, "sales_phase_discovery: no official site; skipping artist", attrs...)
			return 0, nil
		}
		return 0, err
	}
	if site.URL == "" {
		uc.logger.Warn(ctx, "sales_phase_discovery: empty official site URL; skipping artist", attrs...)
		return 0, nil
	}

	candidates, err := uc.searcher.SearchSalesPhases(ctx, &entity.SalesPhaseSearchInput{
		ArtistName:      artist.Name,
		OfficialSiteURL: site.URL,
		Series:          seriesRefs,
	})
	if err != nil {
		uc.logger.Error(ctx, "sales_phase_discovery: searcher failed for artist", err, attrs...)
		return 0, err
	}
	uc.logger.Info(ctx, "sales_phase_discovery: searcher returned candidates",
		append(attrs, slog.Int("series_count", len(order)), slog.Int("count", len(candidates)))...)

	var totalNew int
	for _, candidate := range candidates {
		if ctx.Err() != nil {
			break
		}
		phaseID, outcome, err := uc.salesPhaseRepo.Upsert(ctx, candidate)
		if err != nil {
			uc.logger.Error(ctx, "sales_phase_discovery: upsert failed", err,
				append(attrs, slog.String("series_id", candidate.SeriesID),
					slog.String("apply_start", candidate.ApplyStartTime.Format(time.RFC3339)))...)
			continue
		}
		if outcome == entity.UpsertOutcomeInserted {
			uc.publishDiscovered(ctx, phaseID, candidate, attrs)
			totalNew++
		}
	}

	uc.logger.Info(ctx, "sales_phase_discovery: complete for artist",
		append(attrs, slog.Int("series_count", len(order)), slog.Int("new_phases", totalNew))...)
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
		PhaseID:  phaseID,
		SeriesID: c.SeriesID,
	}
	if err := uc.publisher.PublishEvent(ctx, entity.SubjectSalesPhaseDiscovered, data); err != nil {
		uc.logger.Warn(ctx, "sales_phase_discovery: failed to publish SALES_PHASE.discovered",
			append(attrs, slog.String("error", err.Error()))...)
	}
}
