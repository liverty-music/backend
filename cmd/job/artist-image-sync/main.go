// Package main provides the artist image sync CronJob entry point.
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
	// staleDuration defines how old fanart_synced_at must be to qualify for re-sync.
	staleDuration = 7 * 24 * time.Hour
	// batchLimit caps the number of artists processed per run.
	batchLimit = 500
)

func main() {
	if err := run(); err != nil {
		logger, _ := logging.New()
		logger.Error(context.Background(), "artist image sync job failed", err)
		// Exit 0 to prevent K8s CronJob from retrying on systemic failures.
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	bootLogger, _ := logging.New()
	bootLogger.Info(ctx, "starting artist image sync job")

	app, err := di.InitializeImageSyncJobApp(ctx)
	if err != nil {
		return err
	}

	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), app.ShutdownTimeout)
		defer cancel()
		if err := shutdown.Shutdown(ctx); err != nil {
			app.Logger.Error(context.Background(), "error during shutdown", err)
		}
	}()

	artists, err := app.ArtistRepo.ListStaleOrMissingFanart(ctx, staleDuration, batchLimit)
	if err != nil {
		return err
	}

	app.Logger.Info(ctx, "artists loaded for image sync",
		slog.Int("count", len(artists)),
	)

	var totalAttempted int
	var totalFailed int
	var consecutiveErrors int

	for _, artist := range artists {
		if ctx.Err() != nil {
			break
		}

		totalAttempted++

		if err := app.ImageSyncUC.SyncArtistImage(ctx, artist.ID, artist.MBID); err != nil {
			totalFailed++
			consecutiveErrors++
			app.Logger.Error(ctx, "failed to sync image for artist", err,
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

	if cause := context.Cause(ctx); cause != nil {
		app.Logger.Info(ctx, "job interrupted by signal",
			slog.String("cause", cause.Error()),
		)
	}

	app.Logger.Info(ctx, "artist image sync job complete",
		slog.Int("artists_attempted", totalAttempted),
		slog.Int("artists_succeeded", totalAttempted-totalFailed),
		slog.Int("failures", totalFailed),
	)

	return nil
}
