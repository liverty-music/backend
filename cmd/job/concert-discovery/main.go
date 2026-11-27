// Package main provides the concert discovery CronJob entry point.
//
// The -mode flag picks what a run does: search (the default) starts the due
// concert searches; official-page-check reads the official pages of the
// followed artists and logs a digest of each, for the measurement of OpenSpec
// change measure-official-page-change-detection.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/liverty-music/backend/internal/di"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/gcp/gemini"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/liverty-music/backend/pkg/analyticslog"
	"github.com/liverty-music/backend/pkg/shutdown"
	"github.com/liverty-music/backend/pkg/telemetry"
	"github.com/pannpers/go-logging/logging"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

const (
	// maxConsecutiveErrors is the threshold for stopping the job due to systemic failures.
	maxConsecutiveErrors = 3
	// fallbackShutdownTimeout is used when DI initialization fails and
	// app.ShutdownTimeout is unavailable.
	fallbackShutdownTimeout = 10 * time.Second
)

// Modes of a run, chosen by the -mode flag.
const (
	modeSearch = "search"
	// modeOfficialPageCheck is temporary.
	//
	// TODO(measure-official-page-change-detection): remove after the
	// measurement.
	modeOfficialPageCheck = "official-page-check"
)

func main() {
	logger := di.NewBootstrapLogger()
	mode, err := parseMode(os.Args[1:])
	if err != nil {
		logger.Error(context.Background(), "concert discovery job failed", err)
		os.Exit(2)
	}
	run := runSearch
	if mode == modeOfficialPageCheck {
		run = runOfficialPageCheck
	}
	if err := run(logger); err != nil {
		logger.Error(context.Background(), "concert discovery job failed", err)
		// Exit non-zero so the Job is recorded as failed. The CronJob's
		// backoffLimit: 0 keeps a systemic failure from being retried.
		os.Exit(1)
	}
}

// parseMode returns the mode the command-line arguments choose, modeSearch
// when they name none. Any argument other than the -mode flag is refused.
func parseMode(args []string) (string, error) {
	fs := flag.NewFlagSet("concert-discovery", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	mode := fs.String("mode", modeSearch, "what the run does: search or official-page-check")
	if err := fs.Parse(args); err != nil {
		return "", err
	}
	// A bare word such as "official-page-check" would otherwise fall through
	// to the default and run the search.
	if fs.NArg() > 0 {
		return "", fmt.Errorf("unexpected argument %q; choose the mode with -mode", fs.Arg(0))
	}
	switch *mode {
	case modeSearch, modeOfficialPageCheck:
		return *mode, nil
	default:
		return "", fmt.Errorf("unknown -mode %q (allowed: %q, %q)", *mode, modeSearch, modeOfficialPageCheck)
	}
}

// shutdownAfterRun shuts down the registered resources within the timeout
// that timeout returns, fallbackShutdownTimeout when it returns 0 (DI failed
// before the timeout was read).
func shutdownAfterRun(bootLogger *logging.Logger, timeout func() time.Duration) {
	d := timeout()
	if d == 0 {
		d = fallbackShutdownTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	if err := shutdown.Shutdown(ctx); err != nil {
		bootLogger.Error(context.Background(), "error during shutdown", err)
	}
}

func runSearch(bootLogger *logging.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	bootLogger.Info(ctx, "starting concert discovery job")

	// Register shutdown before DI so partially-initialized resources are
	// cleaned up even when initialization fails partway through.
	var app *di.JobApp
	defer shutdownAfterRun(bootLogger, func() time.Duration {
		if app == nil {
			return 0
		}
		return app.ShutdownTimeout
	})

	var err error
	app, err = di.InitializeJobApp(ctx)
	if err != nil {
		return err
	}

	ctx, runSpan := telemetry.StartJobRun(ctx, "concert-discovery")
	defer runSpan.End()

	artists, err := app.FollowRepo.ListAll(ctx)
	if err != nil {
		return err
	}

	app.Logger.Info(ctx, "followed artists loaded for processing",
		slog.Int("count", len(artists)),
	)

	// SearchNewConcerts records the search and starts it as a workflow, which
	// the workflow worker runs; the job does not wait for the search.
	result := searchArtists(ctx, app.Logger, artists, func(ctx context.Context, artist *entity.Artist) error {
		return app.ConcertUC.SearchNewConcerts(ctx, artist.ID, usecase.SearchTriggerDaily)
	})

	// Go 1.26: context.Cause returns the specific OS signal if shutdown was triggered.
	if cause := context.Cause(ctx); cause != nil {
		app.Logger.Info(ctx, "job interrupted by signal",
			slog.String("cause", cause.Error()),
		)
	}

	logSummary(ctx, app.Logger, len(artists), result)

	return nil
}

// runOfficialPageCheck reads the official pages of the followed artists the
// concert search would search and logs a digest of each (see pageChecker).
func runOfficialPageCheck(bootLogger *logging.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	bootLogger.Info(ctx, "starting official page check")

	var app *di.OfficialPageCheckApp
	defer shutdownAfterRun(bootLogger, func() time.Duration {
		if app == nil {
			return 0
		}
		return app.ShutdownTimeout
	})

	var err error
	app, err = di.InitializeOfficialPageCheckApp(ctx)
	if err != nil {
		return err
	}

	ctx, runSpan := telemetry.StartJobRun(ctx, "official-page-check")
	defer runSpan.End()

	artists, err := app.FollowRepo.ListAll(ctx)
	if err != nil {
		return err
	}

	checker := &pageChecker{
		artists:    app.ArtistRepo,
		organizers: app.OrganizerRepo,
		check: func(ctx context.Context, siteURL string) []gemini.OfficialPageCheck {
			return gemini.CheckOfficialPages(ctx, app.PageClient, siteURL)
		},
		logger: app.Logger,
	}
	result := checker.run(ctx, artists)

	if cause := context.Cause(ctx); cause != nil {
		app.Logger.Info(ctx, "job interrupted by signal", slog.String("cause", cause.Error()))
	}
	logPageCheckSummary(ctx, app.Logger, len(artists), result)
	return nil
}

// searchResult is how far a run got: the artists it reached, the searches
// that failed, and whether it stopped after consecutive failures.
type searchResult struct {
	Attempted     int
	Failed        int
	CircuitBroken bool
}

// logSummary writes the run's summary line, marked for analytics. Artists the
// run did not reach are not counted as succeeded.
func logSummary(ctx context.Context, logger *logging.Logger, followed int, result searchResult) {
	logger.Info(ctx, "concert discovery job complete", analyticslog.Attrs(analyticslog.TopicJobResult,
		slog.Int("followed_artists", followed),
		slog.Int("artists_attempted", result.Attempted),
		slog.Int("artists_succeeded", result.Attempted-result.Failed),
		slog.Int("failures", result.Failed),
		slog.Bool("circuit_broken", result.CircuitBroken),
	)...)
}

// searchArtists runs search for each artist in its own trace, linked to the
// run span in ctx, so the search's log lines and the events it publishes
// carry that artist's trace id. It stops on cancellation or after
// maxConsecutiveErrors failures in a row, and returns how far it got.
func searchArtists(ctx context.Context, logger *logging.Logger, artists []*entity.Artist, search func(context.Context, *entity.Artist) error) searchResult {
	var result searchResult
	var consecutiveErrors int

	for _, artist := range artists {
		// Stop immediately on SIGTERM instead of waiting for the circuit
		// breaker to trip after maxConsecutiveErrors cancelled API calls.
		if ctx.Err() != nil {
			break
		}

		result.Attempted++
		itemCtx, span := telemetry.StartJobItem(ctx, "concert_search.artist",
			attribute.String("artist.id", artist.ID),
		)
		err := search(itemCtx, artist)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, "concert search failed")
			logger.Error(itemCtx, "failed to search concerts for artist", err,
				slog.String("artist_id", artist.ID),
				slog.String("artist_name", artist.Name),
			)
		}
		span.End()

		if err != nil {
			result.Failed++
			consecutiveErrors++
			if consecutiveErrors >= maxConsecutiveErrors {
				logger.Error(ctx, "circuit breaker activated: stopping after consecutive failures", nil,
					slog.Int("consecutive_errors", consecutiveErrors),
				)
				result.CircuitBroken = true
				break
			}
			continue
		}

		consecutiveErrors = 0
	}

	return result
}
