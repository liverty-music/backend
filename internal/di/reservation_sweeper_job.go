package di

import (
	"context"
	"log/slog"
	"time"

	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-logging/logging"
)

// reservationSweepInterval is how often the checkout sweepers run.
const reservationSweepInterval = 1 * time.Minute

// startReservationSweepers launches the two 1-minute checkout jobs:
// ReleaseExpired ends lapsed holds and gives back the card holds of ended
// checkouts, and IssueDueReservations finishes checkouts committed more than a
// minute ago. Both are idempotent across pods (conditional updates, the
// per-Reservation lock), so an interrupted run is retried on the next tick.
func startReservationSweepers(ctx context.Context, reservationUC usecase.ReservationUseCase, issuanceUC usecase.IssuanceUseCase, logger *logging.Logger) {
	go func() {
		ticker := time.NewTicker(reservationSweepInterval)
		defer ticker.Stop()

		logger.Info(ctx, "reservation sweepers started", slog.Duration("interval", reservationSweepInterval))
		for {
			select {
			case <-ctx.Done():
				logger.Info(ctx, "reservation sweepers stopped")
				return
			case <-ticker.C:
				if err := reservationUC.ReleaseExpired(ctx); err != nil {
					logger.Warn(ctx, "release-expired sweep failed", slog.Any("error", err))
				}
				if err := issuanceUC.IssueDueReservations(ctx); err != nil {
					logger.Warn(ctx, "stalled-checkout sweep failed", slog.Any("error", err))
				}
			}
		}
	}()
}
