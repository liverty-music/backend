package entity

import (
	"context"
	"fmt"
	"time"
)

// TicketSaleSearch records when a series was last searched for published
// ticket sales, so discovery searches each series at most once in a given
// interval whether or not that search found anything.
type TicketSaleSearch struct {
	// SeriesID is the series that was searched. One search per series.
	SeriesID string
	// SearchedTime is when the series was last searched successfully.
	SearchedTime time.Time
}

// Validate checks that the searched time is not after now. It returns a stdlib
// error; callers wrap it with the appropriate apperr code.
func (s *TicketSaleSearch) Validate(now time.Time) error {
	if s.SearchedTime.After(now) {
		return fmt.Errorf("searched time %s is in the future", s.SearchedTime.Format(time.RFC3339))
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
