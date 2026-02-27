// Package main provides the concert discovery CronJob entry point.
package main

import (
	"context"
	"log/slog"
	"os/signal"
	"syscall"

	"github.com/liverty-music/backend/internal/di"
	"github.com/liverty-music/backend/pkg/shutdown"
	"github.com/pannpers/go-logging/logging"
)

// maxConsecutiveErrors is the threshold for stopping the job due to systemic failures.
const maxConsecutiveErrors = 3

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

	app, err := di.InitializeJobApp(ctx)
	if err != nil {
		return err
	}
	// Use a fresh context with a deadline aligned to the K8s termination budget,
	// so that phases are skipped if shutdown runs too long.
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), app.ShutdownTimeout)
		defer cancel()
		if err := shutdown.Shutdown(ctx); err != nil {
			app.Logger.Error(context.Background(), "error during shutdown", err)
		}
	}()

	artists, err := app.ArtistRepo.ListAllFollowed(ctx)
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
		if err := app.ConcertUC.SearchNewConcerts(ctx, artist.ID); err != nil {
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
