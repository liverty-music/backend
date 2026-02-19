// Package main provides the concert discovery CronJob entry point.
package main

import (
	"context"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/liverty-music/backend/internal/di"
)

// maxConsecutiveErrors is the threshold for stopping the job due to systemic failures.
const maxConsecutiveErrors = 3

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log.Println("Starting concert discovery job...")

	app, err := di.InitializeJobApp(ctx)
	if err != nil {
		log.Printf("Failed to initialize job: %v", err)
		return
	}
	defer func() {
		if err := app.Shutdown(ctx); err != nil {
			log.Printf("Error during shutdown: %v", err)
		}
	}()

	artists, err := app.ArtistRepo.ListAllFollowed(ctx)
	if err != nil {
		log.Printf("Failed to list followed artists: %v", err)
		return
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

	log.Printf("Concert discovery job complete: %d artists processed, %d new concerts discovered, %d failures",
		len(artists)-totalFailed, totalDiscovered, totalFailed)
}
