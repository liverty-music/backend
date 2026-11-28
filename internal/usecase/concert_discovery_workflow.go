package usecase

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/liverty-music/backend/internal/entity"
)

// WorkflowCreateFromDiscovered is the registered name of
// ConcertCreationUseCase.RunCreateFromDiscovered.
const WorkflowCreateFromDiscovered = "create-from-discovered"

// ConcertDiscoveryQueue is the queue every create-from-discovered run waits
// on. Its concurrency is 1: two Artists of one concert are found by two
// searches, and their batches, decided one after the other, see what the
// earlier one stored (OpenSpec change recognize-same-discovered-concert,
// design D11).
const (
	ConcertDiscoveryQueue            = "concert-discovery"
	ConcertDiscoveryQueueConcurrency = 1
)

// discoveryQueueDeadline bounds a run on [ConcertDiscoveryQueue]. The
// workflow runtime turns the deadline into a DBOS timeout, which DBOS counts
// from when the run leaves the queue, so it bounds how long the run runs,
// retries included, not how long it waits. The queue runs one at a
// time, so a run that keeps retrying an Unavailable step would hold every
// other Artist's batch behind it; at the deadline it ends, frees the queue and
// is reported to an admin. A day's batches take minutes, so 6 hours is far
// more than a healthy run needs.
const discoveryQueueDeadline = 6 * time.Hour

// discoveryQueueStart returns the start options of a run on
// [ConcertDiscoveryQueue]: the queue and a deadline [discoveryQueueDeadline]
// from now. The clock is read in a step, so a replay starts the run with the
// deadline the first execution chose.
func discoveryQueueStart(ctx context.Context, f Flow, clock Clock) ([]StartOption, error) {
	now, err := Step(ctx, f, "discovery deadline", func(context.Context) (time.Time, error) { return clock(), nil })
	if err != nil {
		return nil, err
	}
	return []StartOption{WithQueue(ConcertDiscoveryQueue), WithDeadline(now.Add(discoveryQueueDeadline))}, nil
}

// WorkflowRemoveEmptyDiscoveredSeries is the registered name of
// ConcertCreationUseCase.RunRemoveEmptyDiscoveredSeries. It runs on
// [ConcertDiscoveryQueue], so it never overlaps a batch: a batch may choose an
// old Series and empty it with a Merge before it stores the rest of its
// events there.
const WorkflowRemoveEmptyDiscoveredSeries = "remove-empty-discovered-series"

// WorkflowScheduleEmptySeriesRemoval is the registered name of
// ConcertCreationUseCase.ScheduleEmptySeriesRemoval, the scheduled workflow
// that starts [WorkflowRemoveEmptyDiscoveredSeries].
const WorkflowScheduleEmptySeriesRemoval = "schedule-empty-series-removal"

// RemoveEmptyDiscoveredSeriesSchedule runs
// [WorkflowScheduleEmptySeriesRemoval] once a day, at 04:00 UTC (13:00 JST,
// away from the 18:00 JST discovery run), as a cron expression with seconds.
const RemoveEmptyDiscoveredSeriesSchedule = "0 0 4 * * *"

// emptySeriesAge is how old an empty discovered Series must be before it is
// removed: a batch creates its Series before it stores the events, and a day
// is far longer than any batch.
const emptySeriesAge = 24 * time.Hour

// discoveryWorkflowID names the create-from-discovered workflow of one search:
// the Artist and the search time of its ConcertSearch, so one search starts
// it once.
func discoveryWorkflowID(artistID string, searchTime time.Time) string {
	return WorkflowCreateFromDiscovered + ":" + artistID + ":" + strconv.FormatInt(searchTime.UnixMicro(), 10)
}

// RunCreateFromDiscovered is the create-from-discovered workflow: it
// processes the search's batch through CreateFromDiscovered in one step. A
// step that fails with Unavailable is tried again with the same batch; every
// part of it is safe to repeat.
func (uc *concertCreationUseCase) RunCreateFromDiscovered(ctx context.Context, f Flow, in entity.ConcertDiscoveredData) (struct{}, error) {
	return Step(ctx, f, "create", func(ctx context.Context) (struct{}, error) {
		return struct{}{}, uc.CreateFromDiscovered(ctx, in)
	})
}

// RemoveEmptyDiscoveredSeries implements [ConcertCreationUseCase].
func (uc *concertCreationUseCase) RemoveEmptyDiscoveredSeries(ctx context.Context, now time.Time) error {
	removed, err := uc.seriesRepo.DeleteOrphaned(ctx, now.Add(-emptySeriesAge))
	if err != nil {
		return fmt.Errorf("remove empty discovered series: %w", err)
	}
	uc.logger.Info(ctx, "removed empty discovered series", slog.Int64("removed", removed))
	return nil
}

// ScheduleEmptySeriesRemoval implements [ConcertCreationUseCase].
func (uc *concertCreationUseCase) ScheduleEmptySeriesRemoval(ctx context.Context, f Flow, scheduled time.Time) (struct{}, error) {
	opts, err := discoveryQueueStart(ctx, f, uc.clock)
	if err != nil {
		return struct{}{}, err
	}
	id := WorkflowRemoveEmptyDiscoveredSeries + ":" + strconv.FormatInt(scheduled.Unix(), 10)
	return struct{}{}, f.Start(ctx, WorkflowRemoveEmptyDiscoveredSeries, id, scheduled, opts...)
}

// RunRemoveEmptyDiscoveredSeries implements [ConcertCreationUseCase]. A
// failure ends the run, and the next day's run removes the Series.
func (uc *concertCreationUseCase) RunRemoveEmptyDiscoveredSeries(ctx context.Context, f Flow, scheduled time.Time) (struct{}, error) {
	return Step(ctx, f, "remove", func(ctx context.Context) (struct{}, error) {
		return struct{}{}, uc.RemoveEmptyDiscoveredSeries(ctx, scheduled)
	})
}
