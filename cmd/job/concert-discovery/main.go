// Package main provides the concert discovery CronJob entry point.
package main

import (
	"context"
	"log"
	"log/slog"
	"os/signal"
	"syscall"

	"github.com/liverty-music/backend/internal/di"
)

// maxConsecutiveErrors is the threshold for stopping the job due to systemic failures.
const maxConsecutiveErrors = 3

func main() {
	if err := run(); err != nil {
		log.Printf("Concert discovery job failed: %v", err)
		// Design spec requires exit 0 to prevent K8s CronJob from retrying
		// on systemic failures (e.g., API rate limits) that would hit the same issue.
		// Monitoring relies on structured logging at ERROR level.
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	log.Println("Starting concert discovery job...")

	app, err := di.InitializeJobApp(ctx)
	if err != nil {
		return err
	}
	defer func() {
		if err := app.Shutdown(ctx); err != nil {
			log.Printf("Error during shutdown: %v", err)
		}
	}()

	artists, err := app.ArtistRepo.ListAllFollowed(ctx)
	if err != nil {
		return err
	}

	log.Printf("Found %d followed artists to process", len(artists))

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
		}
	}

	log.Printf("Concert discovery job complete: %d artists attempted, %d succeeded, %d new concerts discovered, %d failures",
		len(artists), len(artists)-totalFailed, totalDiscovered, totalFailed)

	// Post-step: enrich pending venues via MusicBrainz / Google Maps.
	// Per-venue errors are non-fatal and logged inside EnrichPendingVenues.
	app.Logger.Info(ctx, "starting venue enrichment post-step")
	if err := app.VenueEnrichUC.EnrichPendingVenues(ctx); err != nil {
		app.Logger.Error(ctx, "venue enrichment post-step failed", err)
	}

	return nil
}
