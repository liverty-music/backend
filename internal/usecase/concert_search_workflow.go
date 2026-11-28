package usecase

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"

	"github.com/liverty-music/backend/internal/entity"
)

// WorkflowSearchConcerts is the registered name of ConcertUseCase.RunSearch.
const WorkflowSearchConcerts = "search-concerts"

// ConcertSearchQueue is the queue every search-concerts run waits on. Its
// concurrency caps how many attempts call the external search at once across
// the platform; an attempt waiting to retry is delayed, not running, so it
// holds no place.
const (
	ConcertSearchQueue            = "concert-search"
	ConcertSearchQueueConcurrency = 8
)

const (
	// searchWindow is how long after its search time a search may keep trying.
	// No attempt calls the external search later than that. It is shorter than
	// a day, so a search that gives up frees the Artist before the next daily
	// run at the same hour.
	searchWindow = 20 * time.Hour

	// searchRetryDelay is the wait before the first retry of a busy search;
	// it doubles with each further retry up to searchMaxRetryDelay.
	searchRetryDelay    = time.Minute
	searchMaxRetryDelay = time.Hour
)

// ConcertSearchAttempt is the input of one search-concerts run: one attempt of
// the search of an Artist.
type ConcertSearchAttempt struct {
	ArtistID string `json:"artist_id"`
	// SearchTime is the search time of the Artist's ConcertSearch the search
	// belongs to.
	SearchTime time.Time     `json:"search_time"`
	Trigger    SearchTrigger `json:"trigger"`
	// Attempt counts the search's attempts from 1.
	Attempt int `json:"attempt"`
	// LastErrorKind is the error kind of the previous attempt's call, empty on
	// the first attempt. An attempt too late to call the search ends it with
	// this kind.
	LastErrorKind string `json:"last_error_kind,omitempty"`
}

// searchAttemptID names one attempt of a search, so each attempt runs once.
func searchAttemptID(artistID string, searchTime time.Time, attempt int) string {
	return WorkflowSearchConcerts + ":" + artistID + ":" +
		strconv.FormatInt(searchTime.UnixMicro(), 10) + ":" + strconv.Itoa(attempt)
}

// retryDelay is the wait between the end of the attempt number attempt and the
// start of the next one.
func retryDelay(attempt int) time.Duration {
	d := searchRetryDelay
	for i := 1; i < attempt && d < searchMaxRetryDelay; i++ {
		d *= 2
	}
	return min(d, searchMaxRetryDelay)
}

// searchCheck is what an attempt finds before it searches.
type searchCheck struct {
	// Replaced is true when the ConcertSearch is no longer pending with the
	// attempt's search time.
	Replaced bool `json:"replaced"`
	// Barred is true when the Artist may no longer be searched: its activity
	// has ended, or an active Organizer represents it.
	Barred bool `json:"barred"`
}

// searchCall is the result of the search step. A busy or failed search is a
// result, not an error, so the step is never retried for it.
type searchCall struct {
	// Called is true when this attempt called the external search.
	Called bool `json:"called"`
	// Busy is true when the search is to be tried again: the external search
	// failed with Unavailable or DeadlineExceeded, or it was too late to call
	// it.
	Busy bool `json:"busy"`
	// ErrorKind is the error kind of a busy or failed search; empty for one
	// that succeeded.
	ErrorKind string `json:"error_kind,omitempty"`
	// EndTime is when the step ended, from which the next attempt's start is
	// counted.
	EndTime time.Time `json:"end_time"`
	// ArtistName and Series are the new concerts of a search that succeeded.
	ArtistName string                     `json:"artist_name,omitempty"`
	Series     []*entity.DiscoveredSeries `json:"series,omitempty"`
}

// searchEnd is how a search ended.
type searchEnd struct {
	// Failed ends the ConcertSearch as failed, otherwise completed.
	Failed    bool
	ErrorKind string
	// SeriesCount and EventCount are what the search handed on.
	SeriesCount int
	EventCount  int
	// Called is true when some attempt of the search called the external
	// search; only such a search reports its outcome.
	Called bool
}

// RunSearch is the search-concerts workflow: one attempt of an Artist's
// concert search. It checks that the search is still current and the Artist
// may still be searched, calls the external search once, and then either
// starts the next attempt (the search was busy and there is time left), hands
// the new concerts on to create-from-discovered, or records how the search
// ended. Every outcome of the search is a successful run.
func (uc *concertUseCase) RunSearch(ctx context.Context, f Flow, in ConcertSearchAttempt) (struct{}, error) {
	check, err := Step(ctx, f, "check", func(ctx context.Context) (searchCheck, error) {
		return uc.checkSearch(ctx, in)
	})
	if err != nil || check.Replaced {
		return struct{}{}, err
	}
	// An attempt after the first follows one that called the external search.
	calledBefore := in.Attempt > 1
	if check.Barred {
		return uc.endSearch(ctx, f, in, searchEnd{Called: calledBefore})
	}

	call, err := Step(ctx, f, "search", func(ctx context.Context) (searchCall, error) {
		return uc.searchOnce(ctx, in)
	})
	if err != nil {
		return struct{}{}, err
	}
	called := calledBefore || call.Called

	if call.Busy {
		delay := retryDelay(in.Attempt)
		if call.EndTime.Add(delay).Before(in.SearchTime.Add(searchWindow)) {
			next := in
			next.Attempt++
			next.LastErrorKind = call.ErrorKind
			return struct{}{}, f.Start(ctx, WorkflowSearchConcerts,
				searchAttemptID(in.ArtistID, in.SearchTime, next.Attempt), next,
				WithQueue(ConcertSearchQueue), WithDelay(delay))
		}
	}
	if call.ErrorKind != "" || call.Busy {
		return uc.endSearch(ctx, f, in, searchEnd{Failed: true, ErrorKind: call.ErrorKind, Called: called})
	}
	return uc.handOn(ctx, f, in, call, called)
}

// checkSearch reads the Artist's ConcertSearch and the Artist again, because
// an attempt can run hours after the search was requested. A failure to check
// the Artist's representation is logged and the search goes on, as in
// SearchNewConcerts.
func (uc *concertUseCase) checkSearch(ctx context.Context, in ConcertSearchAttempt) (searchCheck, error) {
	search, err := uc.concertSearchRepo.GetByArtistID(ctx, in.ArtistID)
	if errors.Is(err, apperr.ErrNotFound) {
		return searchCheck{Replaced: true}, nil
	}
	if err != nil {
		return searchCheck{}, fmt.Errorf("failed to get concert search: %w", err)
	}
	if search.Status != entity.ConcertSearchStatusPending || search.SearchTime.UnixMicro() != in.SearchTime.UnixMicro() {
		return searchCheck{Replaced: true}, nil
	}
	artist, err := uc.artistRepo.Get(ctx, in.ArtistID)
	if err != nil {
		return searchCheck{}, fmt.Errorf("failed to get artist: %w", err)
	}
	return searchCheck{Barred: !artist.IsActive() || uc.isRepresented(ctx, in.ArtistID)}, nil
}

// searchOnce reads what the search needs, calls the external search once and
// returns the series left after leaving out the events already published or
// pending review. It does not call the search once the search window has
// passed. A read that fails with Unavailable or DeadlineExceeded is returned
// as an error, so the step is tried again before anything was called; every
// other failure is part of the result.
//
// Deduplication uses `(local_event_date, listed_venue_name)` matching via
// `entity.FilterNewSeries`, aligned with the DB-level natural key
// `UNIQUE (series_id, local_event_date, venue_id)` enforced by the events
// table. It avoids unnecessary hand-over/UPSERT round-trips for re-scrapes;
// the DB natural key is the source of truth and uses the resolved `venue_id`
// instead of the raw listed name, so the application key is a best-effort
// upstream filter.
func (uc *concertUseCase) searchOnce(ctx context.Context, in ConcertSearchAttempt) (searchCall, error) {
	artist, err := uc.artistRepo.Get(ctx, in.ArtistID)
	if err != nil {
		return uc.readFailed(ctx, in, fmt.Errorf("failed to get artist: %w", err))
	}
	// A missing official site is not an error; the search runs without one.
	site, err := uc.artistRepo.GetOfficialSite(ctx, in.ArtistID)
	if errors.Is(err, apperr.ErrNotFound) {
		site, err = nil, nil
	}
	if err != nil {
		return uc.readFailed(ctx, in, fmt.Errorf("failed to get official site: %w", err))
	}

	// One instant for the whole attempt: the search leaves out events dated
	// before the current date at their Venue at now, and the concerts it is
	// compared against are the upcoming ones at the same instant.
	now := uc.clock()

	existing, err := uc.concertRepo.ListUpcomingByArtist(ctx, in.ArtistID, now)
	if err != nil {
		return uc.readFailed(ctx, in, fmt.Errorf("failed to list existing concerts: %w", err))
	}
	// Pending concert candidates are left out so they are not staged again.
	pendingKeys, err := uc.concertCandidateRepo.ListPendingDedupKeysByArtist(ctx, in.ArtistID)
	if err != nil {
		return uc.readFailed(ctx, in, fmt.Errorf("failed to list pending concert candidate keys: %w", err))
	}
	// A listing an admin settled (rejected, or kept as a duplicate) is left
	// out while it is found unchanged; a corrected listing is staged again.
	rejected, err := uc.rejectedConcertRepo.ListByArtist(ctx, in.ArtistID)
	if err != nil {
		return uc.readFailed(ctx, in, fmt.Errorf("failed to list rejected concerts: %w", err))
	}

	if !uc.clock().Before(in.SearchTime.Add(searchWindow)) {
		return searchCall{Busy: true, ErrorKind: in.LastErrorKind, EndTime: uc.clock()}, nil
	}

	scraped, err := uc.concertSearcher.Search(ctx, artist, site, now)
	if err != nil {
		// The run itself was cancelled: not an outcome of the search, so the
		// engine gets the error and a resumed run searches again.
		if ctx.Err() != nil {
			return searchCall{}, err
		}
		uc.logSearchFailure(ctx, in, err)
		return searchCall{
			Called:    true,
			Busy:      isTransient(err),
			ErrorKind: errorCode(err),
			EndTime:   uc.clock(),
		}, nil
	}
	return searchCall{
		Called:     true,
		EndTime:    uc.clock(),
		ArtistName: artist.Name,
		Series:     uc.filterNewSeries(ctx, in.ArtistID, scraped, existing, pendingKeys, rejected),
	}, nil
}

// logSearchFailure records a failed call of the external search. A transient
// failure is expected while the service is busy, so it is information, also on
// the attempt after which the search gives up: it raises no alert, and the
// count of failed outcomes shows how often it stayed busy for the whole window. Any other failure fails the
// search and is an error, which alerts: a refused account or a broken request
// needs an admin.
func (uc *concertUseCase) logSearchFailure(ctx context.Context, in ConcertSearchAttempt, err error) {
	attrs := []slog.Attr{
		slog.String("artist_id", in.ArtistID),
		slog.Int("attempt", in.Attempt),
	}
	if isTransient(err) {
		uc.logger.Info(ctx, "concert search attempt failed", append(attrs, slog.Any("error", err))...)
		return
	}
	uc.logger.Error(ctx, "concert search failed", err, attrs...)
}

// readFailed turns the failure of a read before the external search into the
// search step's outcome: a transient one, and any failure of a cancelled run,
// is the step's error; any other fails the search.
func (uc *concertUseCase) readFailed(ctx context.Context, in ConcertSearchAttempt, err error) (searchCall, error) {
	if isTransient(err) || ctx.Err() != nil {
		return searchCall{}, err
	}
	uc.logger.Error(ctx, "concert search could not read what it needs", err,
		slog.String("artist_id", in.ArtistID),
	)
	return searchCall{ErrorKind: errorCode(err), EndTime: uc.clock()}, nil
}

// filterNewSeries leaves out of scraped the events already published
// (existing), pending review (pendingKeys) or settled by an admin with the
// same listing (rejected), and the series that empties.
func (uc *concertUseCase) filterNewSeries(
	ctx context.Context,
	artistID string,
	scraped []*entity.DiscoveredSeries,
	existing []*entity.Concert,
	pendingKeys []entity.ConcertCandidateDedupKey,
	rejected []*entity.RejectedConcert,
) []*entity.DiscoveredSeries {
	scrapedEvents := entity.SumSeriesEvents(scraped)

	// Deduplicate against published concerts, per event while preserving grouping.
	_, filterSpan := otel.Tracer("usecase/concert").Start(ctx, "FilterNewConcerts")
	newSeries := entity.FilterNewSeries(scraped, existing)
	filterSpan.SetAttributes(
		attribute.Int("filter.scraped_count", scrapedEvents),
		attribute.Int("filter.new_count", entity.SumSeriesEvents(newSeries)),
	)
	filterSpan.End()

	// Further deduplicate against pending concert candidates: events pending
	// review are dropped and a series emptied by that is removed entirely.
	newSeries = entity.DropPending(newSeries, pendingKeys)
	newSeries = entity.DropRejected(artistID, newSeries, rejected)

	if filtered := scrapedEvents - entity.SumSeriesEvents(newSeries); filtered > 0 {
		uc.logger.Debug(ctx, "filtered existing/duplicate events (same date)",
			slog.String("artist_id", artistID),
			slog.Int("filtered_count", filtered),
		)
	}
	return newSeries
}

// handOn starts create-from-discovered with the new concerts of a successful
// search, once per search, records the discovery and ends the search. With no
// new concert it starts nothing.
func (uc *concertUseCase) handOn(ctx context.Context, f Flow, in ConcertSearchAttempt, call searchCall, called bool) (struct{}, error) {
	events := entity.SumSeriesEvents(call.Series)
	if events == 0 {
		uc.logger.Debug(ctx, "no new concerts after deduplication",
			slog.String("artist_id", in.ArtistID),
		)
		return uc.endSearch(ctx, f, in, searchEnd{Called: called})
	}

	// A run started before the discovery queue replays without its deadline
	// step and starts the batch as it did then, on the default queue.
	patched, err := f.Patched(ctx, "discovery-queue")
	if err != nil {
		return struct{}{}, err
	}
	var opts []StartOption
	if patched {
		if opts, err = discoveryQueueStart(ctx, f, uc.clock); err != nil {
			return struct{}{}, err
		}
	}
	err = f.Start(ctx, WorkflowCreateFromDiscovered, discoveryWorkflowID(in.ArtistID, in.SearchTime),
		entity.ConcertDiscoveredData{ArtistID: in.ArtistID, ArtistName: call.ArtistName, Series: call.Series}, opts...)
	if err != nil {
		// A conflict or a cancellation of this run is the engine's to handle.
		if errors.Is(err, apperr.ErrAborted) || errors.Is(err, apperr.ErrCanceled) {
			return struct{}{}, err
		}
		uc.logger.Error(ctx, "failed to start create-from-discovered", err,
			slog.String("artist_id", in.ArtistID),
			slog.Int("concert_count", events),
		)
		return uc.endSearch(ctx, f, in, searchEnd{Failed: true, ErrorKind: errorCode(err), Called: called})
	}
	uc.logger.Info(ctx, "started create-from-discovered",
		slog.String("artist_id", in.ArtistID),
		slog.String("artist_name", call.ArtistName),
		slog.Int("series_count", len(call.Series)),
		slog.Int("concert_count", events),
	)

	// Record the discovery so the discoveryWindow skip suppresses redundant
	// re-searches until the next announcement cycle is likely.
	if _, err := Step(ctx, f, "hand-on", func(ctx context.Context) (struct{}, error) {
		uc.markSearchFound(ctx, in.ArtistID)
		return struct{}{}, nil
	}); err != nil {
		return struct{}{}, err
	}
	return uc.endSearch(ctx, f, in, searchEnd{SeriesCount: len(call.Series), EventCount: events, Called: called})
}

// endSearch records the end of a search in one step: the ConcertSearch's
// status, then the search metric and, for a search that called the external
// search, its concert.search.completed signal. The status of a later search
// that has replaced this one is left alone (ConcertSearch.UpdateStatus). A
// search that neither called the external search nor failed records no metric.
//
// A write cut short by the run's cancellation is the step's error too, for the
// resumed run to record. A transient failure to write the status is the step's
// error, so it is tried again: a ConcertSearch left pending would keep the Artist from being
// searched for the whole search window. Nothing else is recorded until then,
// and no failure to record changes the outcome.
func (uc *concertUseCase) endSearch(ctx context.Context, f Flow, in ConcertSearchAttempt, end searchEnd) (struct{}, error) {
	return Step(ctx, f, "record-end", func(ctx context.Context) (struct{}, error) {
		outcome, metric, status := concertSearchOutcomeFound, "success", entity.ConcertSearchStatusCompleted
		switch {
		case end.Failed:
			outcome, metric, status = concertSearchOutcomeFailed, "error", entity.ConcertSearchStatusFailed
		case end.EventCount == 0:
			outcome, metric = concertSearchOutcomeNothingNew, "zero_results"
		}
		if err := uc.concertSearchRepo.UpdateStatus(ctx, in.ArtistID, in.SearchTime, status); err != nil {
			if isTransient(err) || ctx.Err() != nil {
				return struct{}{}, err
			}
			uc.logger.Error(ctx, "failed to update the concert search status", err,
				slog.String("artist_id", in.ArtistID),
				slog.String("status", string(status)),
			)
		}
		if end.Called || end.Failed {
			uc.metrics.RecordConcertSearch(ctx, metric)
		}
		if end.Called {
			uc.reportSearchOutcome(ctx, in, outcome, end)
		}
		return struct{}{}, nil
	})
}

// isTransient reports whether err is a failure that may pass: Unavailable or
// DeadlineExceeded.
func isTransient(err error) bool {
	return errors.Is(err, apperr.ErrUnavailable) || errors.Is(err, apperr.ErrDeadlineExceeded)
}

// reportSearchOutcome sends the concert.search.completed signal of a search
// that ended with outcome.
func (uc *concertUseCase) reportSearchOutcome(ctx context.Context, in ConcertSearchAttempt, outcome string, end searchEnd) {
	props := AnalyticsProperties{
		"artist_id":    in.ArtistID,
		"trigger":      string(in.Trigger),
		"outcome":      outcome,
		"series_count": end.SeriesCount,
		"event_count":  end.EventCount,
	}
	if end.Failed {
		kind := end.ErrorKind
		if kind == "" {
			kind = codes.Unknown.String()
		}
		props["error_code"] = kind
	}
	sendSystemSignal(ctx, uc.analytics, uc.logger, EventConcertSearchCompleted, uc.clock(), props)
}
