package di

import (
	"context"
	"log/slog"
	"time"

	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-logging/logging"
)

// resaleRefundSweepInterval is how often the resale refund sweeper runs.
const resaleRefundSweepInterval = 1 * time.Minute

// startResaleRefundSweeper launches the 1-minute job that refunds and emails
// the fans whose resale listings sold. It is idempotent across pods
// (per-purpose refund keys), so an interrupted run is retried on the next
// tick. It goes with resell-tickets-by-lottery.
func startResaleRefundSweeper(ctx context.Context, resaleListingUC usecase.ResaleListingUseCase, logger *logging.Logger) {
	go func() {
		ticker := time.NewTicker(resaleRefundSweepInterval)
		defer ticker.Stop()

		logger.Info(ctx, "resale refund sweeper started", slog.Duration("interval", resaleRefundSweepInterval))
		for {
			select {
			case <-ctx.Done():
				logger.Info(ctx, "resale refund sweeper stopped")
				return
			case <-ticker.C:
				if err := resaleListingUC.RefundSold(ctx); err != nil {
					logger.Warn(ctx, "resale refund sweep failed", slog.Any("error", err))
				}
			}
		}
	}()
}
