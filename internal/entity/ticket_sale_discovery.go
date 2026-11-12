package entity

import (
	"context"
	"time"
)

// DiscoveredTicketSale is a sale found on an artist's official pages before it
// is stored: the values the search read for one discovered [TicketSale].
type DiscoveredTicketSale struct {
	// SeriesID is the series the sale is attributed to.
	SeriesID string
	// Name is the sale's name as its page gives it. Empty when the pages do not
	// say.
	Name string
	// Method is how tickets are allocated.
	Method TicketSaleMethod
	// SaleStartTime is when the sale opens. Zero when the pages do not say.
	SaleStartTime time.Time
	// SaleEndTime is when the sale closes. Zero when the pages do not say.
	SaleEndTime time.Time
	// ResultDate is the calendar day on which a lottery's results are
	// announced, as [TicketSale.ResultDate]. Nil when the pages do not say.
	ResultDate *time.Time
}

// TicketSale returns the discovered TicketSale with the given discovered time
// that stores d's values.
func (d *DiscoveredTicketSale) TicketSale(discovered time.Time) *TicketSale {
	sale := &TicketSale{
		SeriesID:       d.SeriesID,
		Name:           d.Name,
		Method:         d.Method,
		SaleStartTime:  d.SaleStartTime,
		SaleEndTime:    d.SaleEndTime,
		DiscoveredTime: &discovered,
		ResultDate:     d.ResultDate,
	}
	return sale
}

// Validate reports whether d follows the TicketSale rules for a discovered
// sale (see [TicketSale.Validate]). It fails with InvalidArgument on the first
// failure. now stands in for the discovered time, which the rules only
// require to be set.
func (d *DiscoveredTicketSale) Validate(now time.Time) error {
	return d.TicketSale(now).Validate()
}

// FoundReason decides whether a sale a search found is kept, given the time
// of the search and the series ref the search attributed it to, and returns
// the reason it is dropped, or "" when it is kept. A sale is dropped when:
//
//   - its start time is not after now ("already started"): a sale already open
//     cannot be announced as an upcoming one;
//   - it has no name ("no name") or no end time ("no apply_end_time");
//   - it breaks the TicketSale rules for a discovered sale ([TicketSale.Validate]),
//     in which case the reason is that error's text;
//   - it starts after the day following the series' last event ("apply_start_time
//     after the series' last event"), when the series has events with dates.
func (d *DiscoveredTicketSale) FoundReason(now time.Time, ref *SalesSeriesRef) string {
	switch {
	case !d.SaleStartTime.After(now):
		return "already started"
	case d.Name == "":
		return "no name"
	case d.SaleEndTime.IsZero():
		return "no apply_end_time"
	}
	if err := d.Validate(now); err != nil {
		return err.Error()
	}
	if _, last, ok := ref.EventPeriod(); ok && d.SaleStartTime.After(last.AddDate(0, 0, 1)) {
		return "apply_start_time after the series' last event"
	}
	return ""
}

// EventPeriod returns the first and last dates of the series' events in Japan
// time, and false when it has none with a date.
func (r *SalesSeriesRef) EventPeriod() (first, last time.Time, ok bool) {
	for _, d := range r.EventDates {
		if d.IsZero() {
			continue
		}
		local := d.In(japanTime)
		day := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, japanTime)
		if !ok || day.Before(first) {
			first = day
		}
		if !ok || day.After(last) {
			last = day
		}
		ok = true
	}
	return first, last, ok
}

// UpsertOutcome tells whether UpsertDiscovered created a sale, updated a known
// one or stored nothing. Only a created sale is announced.
type UpsertOutcome int8

const (
	// UpsertOutcomeSkipped means nothing was stored (the start time is
	// unknown).
	UpsertOutcomeSkipped UpsertOutcome = 0
	// UpsertOutcomeInserted means a new discovered sale was created.
	UpsertOutcomeInserted UpsertOutcome = 1
	// UpsertOutcomeUpdated means the known discovered sale with the same
	// series, method and start date in Japan time was updated in place.
	UpsertOutcomeUpdated UpsertOutcome = 2
)

// SalesSeriesRef identifies one of an artist's upcoming series, given to the
// searcher so it can attribute a sale to the right series. The event period
// helps the model tell concurrent tours apart and bounds plausible sale dates.
type SalesSeriesRef struct {
	// SeriesID is the stable identifier of the series.
	SeriesID string
	// Title is the display title of the series (tour).
	Title string
	// EventDates are the known local dates of the series' upcoming events.
	EventDates []time.Time
}

// PublishedSaleSearchInput is the per-artist input of
// [TicketSaleSearcher.SearchPublishedSales]. Discovery issues one search per
// artist, not per series, to cut search cost and to let the model see all of
// the artist's tours at once.
type PublishedSaleSearchInput struct {
	// ArtistID is the id of the performing artist; it identifies the search
	// in its usage report and is not sent to the search service.
	ArtistID string
	// ArtistName is the performing artist whose sales are searched.
	ArtistName string
	// OfficialSiteURL seeds the page-reading tool so the model reads the real
	// page instead of answering from memory. Always present.
	OfficialSiteURL string
	// Series are the artist's upcoming series to search; each sale found is
	// attributed to one of them, and one that cannot be is discarded.
	Series []*SalesSeriesRef
}

// TicketSaleSearcher finds the ticket sales published on an artist's official
// pages, using an external grounded search, one call per artist.
type TicketSaleSearcher interface {
	// SearchPublishedSales issues a single search for the artist in the input
	// and returns the sales that have not opened yet, each attributed to one of
	// the input's series. Every returned sale passes
	// [DiscoveredTicketSale.Validate]. It does not resolve which events of a
	// series a sale covers.
	//
	// An empty result with a nil error means the search succeeded and found no
	// sale. No series given returns no sales without searching.
	//
	// # Possible errors
	//
	//  - InvalidArgument: the service rejects the request.
	//  - Unavailable: the external search service is unreachable or fails.
	//  - ResourceExhausted: the service's quota or spend cap is reached.
	//  - DeadlineExceeded: the call times out.
	//  - Internal: the response is empty, cut off or not valid JSON.
	SearchPublishedSales(ctx context.Context, in *PublishedSaleSearchInput) ([]*DiscoveredTicketSale, error)
}
