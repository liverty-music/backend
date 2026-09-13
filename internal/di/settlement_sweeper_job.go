package di

import (
	"context"
	"log/slog"
	"time"

	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-logging/logging"
)

// settlementSweepInterval is how often the payout sweeper checks for held
// settlements whose release gate has passed.
const settlementSweepInterval = 5 * time.Minute

// startSettlementSweeper launches a background goroutine that periodically
// finds held settlements whose event start_time + dispute buffer has elapsed
// and whose Organizer's connected account is payout-active, then releases each
// by creating the Stripe Transfer(s) and recording the result.
//
// The sweep stops when ctx is canceled (the shutdown signal). No separate
// drain phase is needed because each release is idempotent: if a sweep is
// interrupted mid-release, the settlement status remains Held and the next
// tick retries safely. A concurrent pod that races on the same settlement will
// find status != Held and skip (the status guard in MarkReleased enforces
// exactly-once release).
func startSettlementSweeper(ctx context.Context, uc usecase.PayoutSweeperUseCase, logger *logging.Logger) {
	go func() {
		ticker := time.NewTicker(settlementSweepInterval)
		defer ticker.Stop()

		logger.Info(ctx, "settlement payout sweeper started",
			slog.Duration("interval", settlementSweepInterval),
		)
		for {
			select {
			case <-ctx.Done():
				logger.Info(ctx, "settlement payout sweeper stopped")
				return
			case <-ticker.C:
				if err := uc.ReleaseDueSettlements(ctx); err != nil {
					logger.Warn(ctx, "settlement sweep failed",
						slog.Any("error", err),
					)
				}
			}
		}
	}()
}
