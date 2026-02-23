// Package main provides the concert discovery CronJob entry point.
package main

import (
	"context"
	"log/slog"
	"os/signal"
	"syscall"

	"github.com/liverty-music/backend/internal/di"
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
	defer func() {
		if err := app.Shutdown(ctx); err != nil {
			app.Logger.Error(ctx, "error during shutdown", err)
		}
	}()

	artists, err := app.ArtistRepo.ListAllFollowed(ctx)
	if err != nil {
		return err
	}

	app.Logger.Info(ctx, "followed artists loaded for processing",
		slog.Int("count", len(artists)),
	)

	var totalDiscovered int
	var totalFailed int
	var consecutiveErrors int

	for _, artist := range artists {
		concerts, err := app.ConcertUC.SearchNewConcerts(ctx, artist.ID)
		if err != nil {
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
		totalDiscovered += len(concerts)

		if len(concerts) > 0 {
			app.Logger.Info(ctx, "discovered new concerts",
				slog.String("artist_name", artist.Name),
				slog.Int("count", len(concerts)),
			)
			if err := app.PushNotificationUC.NotifyNewConcerts(ctx, artist, concerts); err != nil {
				app.Logger.Error(ctx, "failed to send push notifications", err,
					slog.String("artist_id", artist.ID),
				)
				// non-fatal: don't increment circuit breaker
			}
		}
	}

	app.Logger.Info(ctx, "concert discovery job complete",
		slog.Int("artists_attempted", len(artists)),
		slog.Int("artists_succeeded", len(artists)-totalFailed),
		slog.Int("concerts_discovered", totalDiscovered),
		slog.Int("failures", totalFailed),
	)

	// Post-step: enrich pending venues via MusicBrainz / Google Maps.
	// Per-venue errors are non-fatal and logged inside EnrichPendingVenues.
	app.Logger.Info(ctx, "starting venue enrichment post-step")
	if err := app.VenueEnrichUC.EnrichPendingVenues(ctx); err != nil {
		app.Logger.Error(ctx, "venue enrichment post-step failed", err)
	}

	return nil
}
