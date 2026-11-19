package entity

import (
	"context"
	"fmt"
	"time"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
)

// TicketSaleSearch records when a series was last searched for published
// ticket sales, so discovery searches each series at most once in a given
// interval whether or not that search found anything.
type TicketSaleSearch struct {
	// SeriesID is the series that was searched. One search per series.
	SeriesID string
	// SearchTime is when the series was last searched successfully.
	SearchTime time.Time
}

// TicketSaleSearchIntervalDays is how many Japan-time dates a searched series
// waits before it is searched again, unless all of its known sales have ended.
const TicketSaleSearchIntervalDays = 10

// ticketSaleSearchZone is Japan time, the zone the daily discovery run is
// scheduled in. JST has no DST, so a fixed zone is exact.
var ticketSaleSearchZone = time.FixedZone("JST", 9*60*60)

// DueAt reports whether the series is due to be searched again at now: at
// least [TicketSaleSearchIntervalDays] Japan-time dates after its last search.
// It compares dates rather than elapsed time, so a search recorded a little
// after 21:00 does not push the next search to the following day's run.
func (s *TicketSaleSearch) DueAt(now time.Time) bool {
	date := func(t time.Time) time.Time {
		local := t.In(ticketSaleSearchZone)
		return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, ticketSaleSearchZone)
	}
	return !date(now).Before(date(s.SearchTime).AddDate(0, 0, TicketSaleSearchIntervalDays))
}

// Validate checks that the searched time is not after now. It fails with
// InvalidArgument, which callers return as it is.
func (s *TicketSaleSearch) Validate(now time.Time) error {
	if s.SearchTime.After(now) {
		return apperr.New(codes.InvalidArgument, fmt.Sprintf("searched time %s is in the future", s.SearchTime.Format(time.RFC3339)))
	}
	return nil
}

// TicketSaleSearchRepository defines the data access interface for
// [TicketSaleSearch].
type TicketSaleSearchRepository interface {
	// ListBySeries returns the search of each given series that has one; a
	// series that was never searched has no entry. The result has no
	// particular order, and an empty input returns an empty result.
	//
	// # Possible errors
	//
	//  - Internal: unexpected database failure.
	ListBySeries(ctx context.Context, seriesIDs []string) ([]*TicketSaleSearch, error)

	// Record sets the searched time of every given series to searchedTime,
	// creating a series' search when it has none and replacing the earlier
	// time otherwise. Either every series is recorded or none is. An empty
	// input records nothing.
	//
	// # Possible errors
	//
	//  - InvalidArgument: searchedTime is in the future.
	//  - FailedPrecondition: any given series does not exist.
	//  - Internal: unexpected database failure.
	Record(ctx context.Context, seriesIDs []string, searchedTime time.Time) error
}
