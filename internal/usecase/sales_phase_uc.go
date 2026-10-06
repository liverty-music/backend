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

// salesPhaseSearchInterval is how long a searched series is not searched
// again, unless all of its known sales have ended.
const salesPhaseSearchInterval = 30 * 24 * time.Hour

// SalesPhaseDiscoveryUseCase selects the series of an artist that need a
// sales-phase search, calls the searcher once for the artist over those
// series, upserts the results, records the search, and publishes a
// SALES_PHASE.discovered event for each brand-new phase.
type SalesPhaseDiscoveryUseCase interface {
	// DiscoverForArtist runs the discovery pipeline for one artist and
	// returns the number of new phases announced.
	DiscoverForArtist(ctx context.Context, artist *entity.Artist) (int, error)
}

type salesPhaseDiscoveryUseCase struct {
	concertRepo    entity.ConcertRepository
	artistRepo     entity.ArtistRepository
	salesPhaseRepo entity.SalesPhaseRepository
	searchLogRepo  entity.SalesPhaseSearchLogRepository
	journeyRepo    entity.TicketJourneyRepository
	searcher       entity.SalesPhaseSearcher
	publisher      EventPublisher
	logger         *logging.Logger
}

// Compile-time interface compliance check.
var _ SalesPhaseDiscoveryUseCase = (*salesPhaseDiscoveryUseCase)(nil)

// NewSalesPhaseDiscoveryUseCase wires the discovery use case.
func NewSalesPhaseDiscoveryUseCase(
	concertRepo entity.ConcertRepository,
	artistRepo entity.ArtistRepository,
	salesPhaseRepo entity.SalesPhaseRepository,
	searchLogRepo entity.SalesPhaseSearchLogRepository,
	journeyRepo entity.TicketJourneyRepository,
	searcher entity.SalesPhaseSearcher,
	publisher EventPublisher,
	logger *logging.Logger,
) SalesPhaseDiscoveryUseCase {
	return &salesPhaseDiscoveryUseCase{
		concertRepo:    concertRepo,
		artistRepo:     artistRepo,
		salesPhaseRepo: salesPhaseRepo,
		searchLogRepo:  searchLogRepo,
		journeyRepo:    journeyRepo,
		searcher:       searcher,
		publisher:      publisher,
		logger:         logger,
	}
}

// DiscoverForArtist implements [SalesPhaseDiscoveryUseCase].
//
// Pipeline (at most ONE grounded search per artist):
//  1. Group the artist's upcoming concerts into series refs (series_id,
//     title, upcoming event dates).
//  2. Keep the series that need a search, cheapest check first: a fan tracks
//     it, no stored phase's application has not ended, and it was not
//     searched in the last 30 days.
//  3. Resolve the artist's official-site URL (the grounding seed).
//  4. Call SalesPhaseSearcher.SearchSalesPhases ONCE for the kept series.
//  5. Upsert each returned candidate and publish SALES_PHASE.discovered for
//     newly inserted phases, then record the search of every kept series.
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
	seriesRefs := groupSalesSeries(concerts)
	if len(seriesRefs) == 0 {
		uc.logger.Info(ctx, "sales_phase_discovery: no upcoming series for artist", attrs...)
		return 0, nil
	}

	now := time.Now()
	seriesRefs, err = uc.selectSeriesToSearch(ctx, seriesRefs, now)
	if err != nil {
		return 0, err
	}
	if len(seriesRefs) == 0 {
		uc.logger.Info(ctx, "sales_phase_discovery: no series needs a search", attrs...)
		return 0, nil
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

	searchedTime := time.Now()
	candidates, err := uc.searcher.SearchSalesPhases(ctx, &entity.SalesPhaseSearchInput{
		ArtistName:      artist.Name,
		OfficialSiteURL: site.URL,
		Series:          seriesRefs,
	})
	if err != nil {
		// Nothing is recorded, so the next daily run searches these series again.
		uc.logger.Error(ctx, "sales_phase_discovery: searcher failed for artist", err, attrs...)
		return 0, err
	}
	uc.logger.Info(ctx, "sales_phase_discovery: searcher returned candidates",
		append(attrs, slog.Int("series_count", len(seriesRefs)), slog.Int("count", len(candidates)))...)

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

	searchedIDs := make([]string, len(seriesRefs))
	for i, ref := range seriesRefs {
		searchedIDs[i] = ref.SeriesID
	}
	if err := uc.searchLogRepo.Record(ctx, searchedIDs, searchedTime); err != nil {
		return totalNew, err
	}

	uc.logger.Info(ctx, "sales_phase_discovery: complete for artist",
		append(attrs, slog.Int("series_count", len(seriesRefs)), slog.Int("new_phases", totalNew))...)
	return totalNew, nil
}

// groupSalesSeries groups upcoming concerts into series refs in first-seen
// order, collecting each series' upcoming event dates.
func groupSalesSeries(concerts []*entity.Concert) []*entity.SalesSeriesRef {
	var refs []*entity.SalesSeriesRef
	bySeriesID := make(map[string]*entity.SalesSeriesRef)
	for _, c := range concerts {
		if c.SeriesID == "" || c.Series == nil {
			continue
		}
		ref, ok := bySeriesID[c.SeriesID]
		if !ok {
			ref = &entity.SalesSeriesRef{SeriesID: c.SeriesID, Title: c.Series.Title}
			bySeriesID[c.SeriesID] = ref
			refs = append(refs, ref)
		}
		ref.EventDates = append(ref.EventDates, c.LocalDate)
	}
	return refs
}

// selectSeriesToSearch keeps the series that need a search, in this order:
// a fan tracks it, none of its stored phases' applications is still running,
// and it was never searched or last searched at least 30 days ago.
func (uc *salesPhaseDiscoveryUseCase) selectSeriesToSearch(
	ctx context.Context,
	refs []*entity.SalesSeriesRef,
	now time.Time,
) ([]*entity.SalesSeriesRef, error) {
	kept := make([]*entity.SalesSeriesRef, 0, len(refs))
	for _, ref := range refs {
		trackers, err := uc.journeyRepo.ListUserIDsTrackingSeries(ctx, ref.SeriesID)
		if err != nil {
			return nil, err
		}
		if len(trackers) == 0 {
			continue
		}
		phases, err := uc.salesPhaseRepo.GetBySeries(ctx, ref.SeriesID)
		if err != nil {
			return nil, err
		}
		if hasRunningApplication(phases, now) {
			continue
		}
		kept = append(kept, ref)
	}
	if len(kept) == 0 {
		return nil, nil
	}

	ids := make([]string, len(kept))
	for i, ref := range kept {
		ids[i] = ref.SeriesID
	}
	logs, err := uc.searchLogRepo.ListBySeries(ctx, ids)
	if err != nil {
		return nil, err
	}
	searchedAt := make(map[string]time.Time, len(logs))
	for _, l := range logs {
		searchedAt[l.SeriesID] = l.SearchedTime
	}

	out := kept[:0]
	for _, ref := range kept {
		if t, ok := searchedAt[ref.SeriesID]; ok && now.Sub(t) < salesPhaseSearchInterval {
			continue
		}
		out = append(out, ref)
	}
	return out, nil
}

// hasRunningApplication reports whether any phase's application has not ended.
func hasRunningApplication(phases []*entity.SalesPhase, now time.Time) bool {
	for _, p := range phases {
		if !p.HasApplicationEnded(now) {
			return true
		}
	}
	return false
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
		PhaseID:        phaseID,
		SeriesID:       c.SeriesID,
		Method:         int16(c.Method),
		ApplyStartTime: c.ApplyStartTime,
	}
	if err := uc.publisher.PublishEvent(ctx, entity.SubjectSalesPhaseDiscovered, data); err != nil {
		uc.logger.Warn(ctx, "sales_phase_discovery: failed to publish SALES_PHASE.discovered",
			append(attrs, slog.String("error", err.Error()))...)
	}
}
