package di

import (
	"context"
	"log/slog"
	"time"

	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-logging/logging"
)

// checkoutSweepInterval is how often the checkout sweepers run.
const checkoutSweepInterval = 1 * time.Minute

// startCheckoutSweepers launches the two 1-minute checkout jobs:
// ReleaseExpired ends lapsed holds and gives back the card holds of ended
// checkouts, and IssueDueCheckouts finishes checkouts committed more than a
// minute ago. Both are idempotent across pods (conditional updates, the
// per-Checkout lock), so an interrupted run is retried on the next tick.
func startCheckoutSweepers(ctx context.Context, checkoutUC usecase.CheckoutUseCase, issuanceUC usecase.IssuanceUseCase, logger *logging.Logger) {
	go func() {
		ticker := time.NewTicker(checkoutSweepInterval)
		defer ticker.Stop()

		logger.Info(ctx, "checkout sweepers started", slog.Duration("interval", checkoutSweepInterval))
		for {
			select {
			case <-ctx.Done():
				logger.Info(ctx, "checkout sweepers stopped")
				return
			case <-ticker.C:
				if err := checkoutUC.ReleaseExpired(ctx); err != nil {
					logger.Warn(ctx, "release-expired sweep failed", slog.Any("error", err))
				}
				if err := issuanceUC.IssueDueCheckouts(ctx); err != nil {
					logger.Warn(ctx, "stalled-checkout sweep failed", slog.Any("error", err))
				}
			}
		}
	}()
}
