// Package main provides the sales-phase discovery CronJob entry point.
//
// The job enumerates the upcoming series of all followed artists, calls the
// Gemini sales-phase searcher once per artist (grounded across all of that
// artist's upcoming series), upserts the discovered phases, and publishes a
// SALES_PHASE.discovered event for each brand-new phase.
// Re-discovery of an already-known phase (UpsertOutcomeUpdated) is silent.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"
	_ "time/tzdata" // embed IANA timezone DB; distroless/static has no system tzdata

	"github.com/liverty-music/backend/internal/di"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/pkg/analyticslog"
	"github.com/liverty-music/backend/pkg/shutdown"
	"github.com/liverty-music/backend/pkg/telemetry"
	"github.com/pannpers/go-logging/logging"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

const (
	maxConsecutiveErrors    = 3
	fallbackShutdownTimeout = 10 * time.Second
)

func main() {
	logger := di.NewBootstrapLogger()
	if err := run(logger); err != nil {
		logger.Error(context.Background(), "sales-phase-discovery job failed", err)
		// Exit non-zero so the Job is recorded as failed. The CronJob's
		// backoffLimit: 0 keeps a systemic failure from being retried.
		os.Exit(1)
	}
}

func run(bootLogger *logging.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	bootLogger.Info(ctx, "starting sales-phase-discovery job")

	var app *di.TicketSaleDiscoveryJobApp
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
	app, err = di.InitializeTicketSaleDiscoveryJobApp(ctx)
	if err != nil {
		return err
	}

	ctx, runSpan := telemetry.StartJobRun(ctx, "sales-phase-discovery")
	defer runSpan.End()

	artists, err := app.FollowRepo.ListAll(ctx)
	if err != nil {
		return err
	}

	app.Logger.Info(ctx, "sales-phase-discovery: artists loaded",
		slog.Int("count", len(artists)),
	)

	totalFailed := discoverArtists(ctx, app.Logger, artists, func(ctx context.Context, artist *entity.Artist) error {
		_, err := app.TicketSaleDiscUC.DiscoverForArtist(ctx, artist)
		return err
	})

	if cause := context.Cause(ctx); cause != nil {
		app.Logger.Info(ctx, "sales-phase-discovery: interrupted by signal",
			slog.String("cause", cause.Error()),
		)
	}

	app.Logger.Info(ctx, "sales-phase-discovery: complete", analyticslog.Attrs(analyticslog.TopicJobResult,
		slog.Int("artists_attempted", len(artists)),
		slog.Int("artists_succeeded", len(artists)-totalFailed),
		slog.Int("failures", totalFailed),
	)...)
	return nil
}

// discoverArtists runs discover for each artist in its own trace, linked to
// the run span in ctx, so the search's log lines and the events it publishes
// carry that artist's trace id. It stops on cancellation or after
// maxConsecutiveErrors failures in a row, and returns the number of failures.
func discoverArtists(ctx context.Context, logger *logging.Logger, artists []*entity.Artist, discover func(context.Context, *entity.Artist) error) int {
	var totalFailed, consecutiveErrors int
	for _, artist := range artists {
		if ctx.Err() != nil {
			break
		}

		itemCtx, span := telemetry.StartJobItem(ctx, "ticket_sale_search.artist",
			attribute.String("artist.id", artist.ID),
		)
		err := discover(itemCtx, artist)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, "ticket sale search failed")
			logger.Error(itemCtx, "sales-phase-discovery: artist failed", err,
				slog.String("artist_id", artist.ID),
				slog.String("artist_name", artist.Name),
			)
		}
		span.End()

		if err != nil {
			totalFailed++
			consecutiveErrors++
			if consecutiveErrors >= maxConsecutiveErrors {
				logger.Error(ctx, "sales-phase-discovery: circuit breaker activated", nil,
					slog.Int("consecutive_errors", consecutiveErrors),
				)
				break
			}
			continue
		}
		consecutiveErrors = 0
	}
	return totalFailed
}
