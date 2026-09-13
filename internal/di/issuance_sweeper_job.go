package di

import (
	"context"
	"log/slog"
	"time"

	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-logging/logging"
)

// issuanceSweepInterval is how often the issuance sweeper checks for
// Won-captured applications that do not yet have an Order.
const issuanceSweepInterval = 1 * time.Minute

// startIssuanceSweeper launches a background goroutine that periodically finds
// Won-captured applications without an Order and issues each (Order + N
// account-bound tickets), mirroring the lottery draw sweeper. This is the MVP
// ④→⑤ handoff: ⑤ polls Won rows rather than consuming an event.
//
// The sweep stops when ctx is canceled. No drain is needed because issuance is
// idempotent (one Order per application, enforced by a unique index): an
// interrupted sweep is safely retried on the next tick.
func startIssuanceSweeper(ctx context.Context, uc usecase.IssuanceUseCase, logger *logging.Logger) {
	go func() {
		ticker := time.NewTicker(issuanceSweepInterval)
		defer ticker.Stop()

		logger.Info(ctx, "issuance sweeper started",
			slog.Duration("interval", issuanceSweepInterval),
		)
		for {
			select {
			case <-ctx.Done():
				logger.Info(ctx, "issuance sweeper stopped")
				return
			case <-ticker.C:
				if err := uc.IssueDueWins(ctx); err != nil {
					logger.Warn(ctx, "issuance sweep failed",
						slog.Any("error", err),
					)
				}
			}
		}
	}()
}
