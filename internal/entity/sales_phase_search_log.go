package entity

import (
	"context"
	"fmt"
	"time"
)

// SalesPhaseSearchLog records when a series was last searched for ticket sales
// phases, so discovery searches each series at most once in a given interval
// whether or not that search found anything.
type SalesPhaseSearchLog struct {
	// SeriesID is the series that was searched. One log per series.
	SeriesID string
	// SearchedTime is when the series was last searched successfully.
	SearchedTime time.Time
}

// Validate checks that the searched time is not after now. It returns a stdlib
// error; callers wrap it with the appropriate apperr code.
func (l *SalesPhaseSearchLog) Validate(now time.Time) error {
	if l.SearchedTime.After(now) {
		return fmt.Errorf("searched time %s is in the future", l.SearchedTime.Format(time.RFC3339))
	}
	return nil
}

// SalesPhaseSearchLogRepository defines the data access interface for
// [SalesPhaseSearchLog].
type SalesPhaseSearchLogRepository interface {
	// ListBySeries returns the log of each given series that has one; a series
	// that was never searched has no entry. The result has no particular order,
	// and an empty input returns an empty result.
	//
	// # Possible errors
	//
	//  - Internal: unexpected database failure.
	ListBySeries(ctx context.Context, seriesIDs []string) ([]*SalesPhaseSearchLog, error)

	// Record sets the searched time of every given series to searchedTime,
	// creating a series' log when it has none and replacing the earlier time
	// otherwise. Either every series is recorded or none is. An empty input
	// records nothing.
	//
	// # Possible errors
	//
	//  - InvalidArgument: If searchedTime is in the future.
	//  - FailedPrecondition: If any given series does not exist.
	//  - Internal: unexpected database failure.
	Record(ctx context.Context, seriesIDs []string, searchedTime time.Time) error
}
