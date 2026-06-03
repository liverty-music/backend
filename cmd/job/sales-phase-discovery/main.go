// Package main provides the sales-phase discovery CronJob entry point.
//
// The job enumerates the upcoming series of all followed artists, calls the
// Gemini sales-phase searcher once per series, upserts the discovered phases,
// and publishes a SALES_PHASE.discovered event for each brand-new phase.
// Re-discovery of an already-known phase (UpsertOutcomeUpdated) is silent.
package main

import (
	"context"
	"log/slog"
	"os/signal"
	"syscall"
	"time"
	_ "time/tzdata" // embed IANA timezone DB; distroless/static has no system tzdata

	"github.com/liverty-music/backend/internal/di"
	"github.com/liverty-music/backend/pkg/shutdown"
	"github.com/pannpers/go-logging/logging"
)

const (
	maxConsecutiveErrors    = 3
	fallbackShutdownTimeout = 10 * time.Second
)

func main() {
	if err := run(); err != nil {
		logger, _ := logging.New()
		logger.Error(context.Background(), "sales-phase-discovery job failed", err)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	bootLogger, _ := logging.New()
	bootLogger.Info(ctx, "starting sales-phase-discovery job")

	var app *di.SalesPhaseDiscoveryJobApp
	defer func() {
		timeout := fallbackShutdownTimeout
		if app != nil {
			timeout = app.ShutdownTimeout
		}
		sctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		if err := shutdown.Shutdown(sctx); err != nil {
			bootLogger.Error(context.Background(), "error during shutdown", err)
		}
	}()

	var err error
	app, err = di.InitializeSalesPhaseDiscoveryJobApp(ctx)
	if err != nil {
		return err
	}

	artists, err := app.FollowRepo.ListAll(ctx)
	if err != nil {
		return err
	}

	app.Logger.Info(ctx, "sales-phase-discovery: artists loaded",
		slog.Int("count", len(artists)),
	)

	var totalFailed, consecutiveErrors int
	for _, artist := range artists {
		if ctx.Err() != nil {
			break
		}
		if _, err := app.SalesPhaseDiscUC.DiscoverForArtist(ctx, artist); err != nil {
			totalFailed++
			consecutiveErrors++
			app.Logger.Error(ctx, "sales-phase-discovery: artist failed", err,
				slog.String("artist_id", artist.ID),
				slog.String("artist_name", artist.Name),
			)
			if consecutiveErrors >= maxConsecutiveErrors {
				app.Logger.Error(ctx, "sales-phase-discovery: circuit breaker activated", nil,
					slog.Int("consecutive_errors", consecutiveErrors),
				)
				break
			}
			continue
		}
		consecutiveErrors = 0
	}

	if cause := context.Cause(ctx); cause != nil {
		app.Logger.Info(ctx, "sales-phase-discovery: interrupted by signal",
			slog.String("cause", cause.Error()),
		)
	}

	app.Logger.Info(ctx, "sales-phase-discovery: complete",
		slog.Int("artists_attempted", len(artists)),
		slog.Int("artists_succeeded", len(artists)-totalFailed),
		slog.Int("failures", totalFailed),
	)
	return nil
}
