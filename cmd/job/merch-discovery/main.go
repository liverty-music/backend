// Package main provides the merch-url discovery CronJob entry point.
package main

import (
	"context"
	"log/slog"
	"os/signal"
	"syscall"
	"time"

	"github.com/liverty-music/backend/internal/di"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/liverty-music/backend/pkg/shutdown"
	"github.com/pannpers/go-logging/logging"
)

const (
	// maxConsecutiveErrors stops the job once this many candidates fail in a
	// row, treating it as a systemic problem (API outage, DB down) rather than
	// burning the whole candidate set against the same fault.
	maxConsecutiveErrors = 3
	// fallbackShutdownTimeout is used when DI initialization fails and
	// app.ShutdownTimeout is unavailable.
	fallbackShutdownTimeout = 10 * time.Second
)

func main() {
	if err := run(); err != nil {
		// Bootstrap logger for fatal error reporting.
		logger, _ := logging.New()
		logger.Error(context.Background(), "merch discovery job failed", err)
		// Exit 0 (no os.Exit(1)): a systemic failure should not make the
		// CronJob retry into the same fault. Monitoring relies on ERROR logs.
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	bootLogger, _ := logging.New()
	bootLogger.Info(ctx, "starting merch discovery job")

	// Register shutdown before DI so partially-initialized resources are
	// cleaned up even when initialization fails partway through.
	var app *di.MerchDiscoveryJobApp
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
	app, err = di.InitializeMerchDiscoveryJobApp(ctx)
	if err != nil {
		return err
	}

	candidates, err := app.MerchUC.ListCandidates(ctx)
	if err != nil {
		return err
	}

	app.Logger.Info(ctx, "merch candidates loaded for processing",
		slog.Int("count", len(candidates)),
	)

	var (
		totalFailed       int
		consecutiveErrors int
		filled            int
	)
	outcomes := make(map[usecase.MerchOutcome]int)

	for _, candidate := range candidates {
		// Stop immediately on SIGTERM instead of waiting for the circuit
		// breaker to trip after maxConsecutiveErrors cancelled calls.
		if ctx.Err() != nil {
			break
		}

		outcome, err := app.MerchUC.ResolveMerchURL(ctx, candidate)
		if err != nil {
			totalFailed++
			consecutiveErrors++
			app.Logger.Error(ctx, "failed to resolve merch url for series", err,
				slog.String("series_id", candidate.SeriesID),
				slog.String("series_title", candidate.SeriesTitle),
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
		outcomes[outcome]++
		if outcome == usecase.MerchOutcomeFilled {
			filled++
		}
	}

	if cause := context.Cause(ctx); cause != nil {
		app.Logger.Info(ctx, "job interrupted by signal",
			slog.String("cause", cause.Error()),
		)
	}

	app.Logger.Info(ctx, "merch discovery job complete",
		slog.Int("candidates", len(candidates)),
		slog.Int("filled", filled),
		slog.Int("already_live", outcomes[usecase.MerchOutcomeAlreadyLive]),
		slog.Int("no_source", outcomes[usecase.MerchOutcomeNoSource]),
		slog.Int("invalid_discarded", outcomes[usecase.MerchOutcomeInvalidDiscarded]),
		slog.Int("failures", totalFailed),
	)

	return nil
}
