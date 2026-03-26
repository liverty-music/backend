// Package main provides the concert discovery CronJob entry point.
package main

import (
	"context"
	"log/slog"
	"os/signal"
	"syscall"
	"time"

	"github.com/liverty-music/backend/internal/di"
	"github.com/liverty-music/backend/pkg/shutdown"
	"github.com/pannpers/go-logging/logging"
)

const (
	// maxConsecutiveErrors is the threshold for stopping the job due to systemic failures.
	maxConsecutiveErrors = 3
	// fallbackShutdownTimeout is used when DI initialization fails and
	// app.ShutdownTimeout is unavailable.
	fallbackShutdownTimeout = 10 * time.Second
)

func main() {
	if err := run(); err != nil {
		// Bootstrap logger for fatal error reporting.
		logger, _ := logging.New()
		logger.Error(context.Background(), "concert discovery job failed", err)
		// Design spec requires exit 0 to prevent K8s CronJob from retrying
		// on systemic failures (e.g., API rate limits) that would hit the same issue.
		// Monitoring relies on structured logging at ERROR level.
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Bootstrap logger for pre-initialization messages.
	bootLogger, _ := logging.New()
	bootLogger.Info(ctx, "starting concert discovery job")

	// Register shutdown before DI so partially-initialized resources are
	// cleaned up even when initialization fails partway through.
	var app *di.JobApp
	defer func() {
		timeout := fallbackShutdownTimeout
		if app != nil {
			timeout = app.ShutdownTimeout
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		if err := shutdown.Shutdown(ctx); err != nil {
			bootLogger.Error(context.Background(), "error during shutdown", err)
		}
	}()

	var err error
	app, err = di.InitializeJobApp(ctx)
	if err != nil {
		return err
	}

	artists, err := app.FollowRepo.ListAll(ctx)
	if err != nil {
		return err
	}

	app.Logger.Info(ctx, "followed artists loaded for processing",
		slog.Int("count", len(artists)),
	)

	var totalFailed int
	var consecutiveErrors int

	for _, artist := range artists {
		// Stop immediately on SIGTERM instead of waiting for the circuit
		// breaker to trip after maxConsecutiveErrors cancelled API calls.
		if ctx.Err() != nil {
			break
		}

		// SearchNewConcerts calls the external API, deduplicates, and publishes
		// a concert.discovered.v1 event. Concert persistence, notification, and
		// venue enrichment are handled asynchronously by event consumers.
		if _, err := app.ConcertUC.SearchNewConcerts(ctx, artist.ID); err != nil {
			totalFailed++
			consecutiveErrors++
			app.Logger.Error(ctx, "failed to search concerts for artist", err,
				slog.String("artist_id", artist.ID),
				slog.String("artist_name", artist.Name),
			)

			if consecutiveErrors >= maxConsecutiveErrors {
				app.Logger.Error(ctx, "circuit breaker activated: stopping after consecutive failures", nil,
					slog.Int("consecutive_errors", consecutiveErrors),
				)
				break
			}
			continue
		}

		consecutiveErrors = 0
	}

	// Go 1.26: context.Cause returns the specific OS signal if shutdown was triggered.
	if cause := context.Cause(ctx); cause != nil {
		app.Logger.Info(ctx, "job interrupted by signal",
			slog.String("cause", cause.Error()),
		)
	}

	app.Logger.Info(ctx, "concert discovery job complete",
		slog.Int("artists_attempted", len(artists)),
		slog.Int("artists_succeeded", len(artists)-totalFailed),
		slog.Int("failures", totalFailed),
	)

	return nil
}
