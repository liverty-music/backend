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
	}
	return sale
}

// Validate reports whether d follows the TicketSale rules for a discovered
// sale (see [TicketSale.Validate]). It returns a stdlib error describing the
// first failure; callers wrap it with an apperr code.
func (d *DiscoveredTicketSale) Validate() error {
	return d.TicketSale(time.Now()).Validate()
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
