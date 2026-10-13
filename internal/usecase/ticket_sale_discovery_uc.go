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

// ticketSaleSearchIntervalDays is how many Japan-time dates a searched series
// waits before it is searched again, unless all of its known sales have ended.
const ticketSaleSearchIntervalDays = 10

// ticketSaleSearchZone is Japan time, the zone the daily discovery run is
// scheduled in. JST has no DST, so a fixed zone is exact.
var ticketSaleSearchZone = time.FixedZone("JST", 9*60*60)

// ticketSaleSearchDue reports whether a series last searched at searched is
// due again at now. It compares Japan-time dates rather than elapsed time, so
// a search recorded a little after 21:00 does not push the next search to the
// following day's run.
func ticketSaleSearchDue(searched, now time.Time) bool {
	date := func(t time.Time) time.Time {
		local := t.In(ticketSaleSearchZone)
		return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, ticketSaleSearchZone)
	}
	return !date(now).Before(date(searched).AddDate(0, 0, ticketSaleSearchIntervalDays))
}

// TicketSaleDiscoveryUseCase selects the series of an artist that need a
// search for published ticket sales, calls the searcher once for the artist
// over those series, stores the results as discovered TicketSales, records the
// search, and publishes a SALES_PHASE.discovered event for each new sale.
type TicketSaleDiscoveryUseCase interface {
	// DiscoverForArtist runs the discovery pipeline for one artist and
	// returns the number of new discovered sales.
	DiscoverForArtist(ctx context.Context, artist *entity.Artist) (int, error)
}

type ticketSaleDiscoveryUseCase struct {
	concertRepo    entity.ConcertRepository
	artistRepo     entity.ArtistRepository
	ticketSaleRepo entity.TicketSaleRepository
	searchRepo     entity.TicketSaleSearchRepository
	journeyRepo    entity.TicketJourneyRepository
	searcher       entity.TicketSaleSearcher
	publisher      EventPublisher
	logger         *logging.Logger
}

// Compile-time interface compliance check.
var _ TicketSaleDiscoveryUseCase = (*ticketSaleDiscoveryUseCase)(nil)

// NewTicketSaleDiscoveryUseCase wires the discovery use case.
func NewTicketSaleDiscoveryUseCase(
	concertRepo entity.ConcertRepository,
	artistRepo entity.ArtistRepository,
	ticketSaleRepo entity.TicketSaleRepository,
	searchRepo entity.TicketSaleSearchRepository,
	journeyRepo entity.TicketJourneyRepository,
	searcher entity.TicketSaleSearcher,
	publisher EventPublisher,
	logger *logging.Logger,
) TicketSaleDiscoveryUseCase {
	return &ticketSaleDiscoveryUseCase{
		concertRepo:    concertRepo,
		artistRepo:     artistRepo,
		ticketSaleRepo: ticketSaleRepo,
		searchRepo:     searchRepo,
		journeyRepo:    journeyRepo,
		searcher:       searcher,
		publisher:      publisher,
		logger:         logger,
	}
}

// DiscoverForArtist implements [TicketSaleDiscoveryUseCase].
//
// Pipeline (at most ONE grounded search per artist):
//  1. Group the artist's upcoming concerts of series without an Organizer
//     into series refs (series_id, title, upcoming event dates). A
//     first-party Series sells only through its Organizer's own sales.
//  2. Keep the series that need a search, cheapest check first: a fan tracks
//     it, none of its sales is not yet over, and it was not searched in the
//     last 10 Japan-time dates.
//  3. Resolve the artist's official-site URL (the grounding seed).
//  4. Call TicketSaleSearcher.SearchPublishedSales ONCE for the kept series.
//  5. Store each returned sale and publish SALES_PHASE.discovered for newly
//     created ones, then record the search of every kept series.
func (uc *ticketSaleDiscoveryUseCase) DiscoverForArtist(ctx context.Context, artist *entity.Artist) (int, error) {
	attrs := []slog.Attr{
		slog.String("artist_id", artist.ID),
		slog.String("artist_name", artist.Name),
	}
	uc.logger.Info(ctx, "ticket_sale_discovery: starting for artist", attrs...)

	concerts, err := uc.concertRepo.ListByArtist(ctx, artist.ID, true)
	if err != nil {
		return 0, err
	}
	seriesRefs := groupSalesSeries(concerts)
	if len(seriesRefs) == 0 {
		uc.logger.Info(ctx, "ticket_sale_discovery: no upcoming series for artist", attrs...)
		return 0, nil
	}

	now := time.Now()
	seriesRefs, err = uc.selectSeriesToSearch(ctx, seriesRefs, now)
	if err != nil {
		return 0, err
	}
	if len(seriesRefs) == 0 {
		uc.logger.Info(ctx, "ticket_sale_discovery: no series needs a search", attrs...)
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
			uc.logger.Warn(ctx, "ticket_sale_discovery: no official site; skipping artist", attrs...)
			return 0, nil
		}
		return 0, err
	}
	if site.URL == "" {
		uc.logger.Warn(ctx, "ticket_sale_discovery: empty official site URL; skipping artist", attrs...)
		return 0, nil
	}

	searchedTime := time.Now()
	found, err := uc.searcher.SearchPublishedSales(ctx, &entity.PublishedSaleSearchInput{
		ArtistName:      artist.Name,
		OfficialSiteURL: site.URL,
		Series:          seriesRefs,
	})
	if err != nil {
		// Nothing is recorded, so the next daily run searches these series again.
		uc.logger.Error(ctx, "ticket_sale_discovery: searcher failed for artist", err, attrs...)
		return 0, err
	}
	uc.logger.Info(ctx, "ticket_sale_discovery: searcher returned sales",
		append(attrs, slog.Int("series_count", len(seriesRefs)), slog.Int("count", len(found)))...)

	var totalNew int
	for _, sale := range found {
		if ctx.Err() != nil {
			break
		}
		saleID, outcome, err := uc.ticketSaleRepo.UpsertDiscovered(ctx, sale)
		if err != nil {
			uc.logger.Error(ctx, "ticket_sale_discovery: upsert failed", err,
				append(attrs, slog.String("series_id", sale.SeriesID),
					slog.String("sale_start", sale.SaleStartTime.Format(time.RFC3339)))...)
			continue
		}
		if outcome == entity.UpsertOutcomeInserted {
			uc.publishDiscovered(ctx, saleID, sale, attrs)
			totalNew++
		}
	}

	searchedIDs := make([]string, len(seriesRefs))
	for i, ref := range seriesRefs {
		searchedIDs[i] = ref.SeriesID
	}
	if err := uc.searchRepo.Record(ctx, searchedIDs, searchedTime); err != nil {
		return totalNew, err
	}

	uc.logger.Info(ctx, "ticket_sale_discovery: complete for artist",
		append(attrs, slog.Int("series_count", len(seriesRefs)), slog.Int("new_sales", totalNew))...)
	return totalNew, nil
}

// groupSalesSeries groups upcoming concerts of series without an Organizer
// into series refs in first-seen order, collecting each series' upcoming event
// dates. A first-party Series is left out: it sells only through its
// Organizer's own TicketSales.
func groupSalesSeries(concerts []*entity.Concert) []*entity.SalesSeriesRef {
	var refs []*entity.SalesSeriesRef
	bySeriesID := make(map[string]*entity.SalesSeriesRef)
	for _, c := range concerts {
		if c.SeriesID == "" || c.Series == nil || c.Series.IsFirstParty() {
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
// a fan tracks it, none of its stored sales is not yet over, and it was never
// searched or last searched at least 10 Japan-time dates ago.
func (uc *ticketSaleDiscoveryUseCase) selectSeriesToSearch(
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
		sales, err := uc.ticketSaleRepo.ListBySeries(ctx, ref.SeriesID)
		if err != nil {
			return nil, err
		}
		if hasSaleNotYetOver(sales, now) {
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
	searches, err := uc.searchRepo.ListBySeries(ctx, ids)
	if err != nil {
		return nil, err
	}
	searchedAt := make(map[string]time.Time, len(searches))
	for _, search := range searches {
		searchedAt[search.SeriesID] = search.SearchedTime
	}

	out := kept[:0]
	for _, ref := range kept {
		if t, ok := searchedAt[ref.SeriesID]; ok && !ticketSaleSearchDue(t, now) {
			continue
		}
		out = append(out, ref)
	}
	return out, nil
}

// hasSaleNotYetOver reports whether any sale is not yet over at now.
func hasSaleNotYetOver(sales []*entity.TicketSale, now time.Time) bool {
	for _, s := range sales {
		if s.IsNotYetOverAt(now) {
			return true
		}
	}
	return false
}

// publishDiscovered publishes a SALES_PHASE.discovered event. Failure is
// logged as a warning and swallowed — announcement is best-effort; the
// sale is already stored.
func (uc *ticketSaleDiscoveryUseCase) publishDiscovered(
	ctx context.Context,
	saleID entity.TicketSaleID,
	sale *entity.DiscoveredTicketSale,
	attrs []slog.Attr,
) {
	data := entity.TicketSaleDiscoveredData{
		TicketSaleID:  string(saleID),
		SeriesID:      sale.SeriesID,
		Method:        int16(sale.Method),
		SaleStartTime: sale.SaleStartTime,
	}
	if err := uc.publisher.PublishEvent(ctx, entity.SubjectTicketSaleDiscovered, data); err != nil {
		uc.logger.Warn(ctx, "ticket_sale_discovery: failed to publish SALES_PHASE.discovered",
			append(attrs, slog.String("error", err.Error()))...)
	}
}
