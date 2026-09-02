package di

import (
	"context"
	"log/slog"
	"time"

	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-logging/logging"
)

// lotteryDrawInterval is how often the draw sweeper checks for phases whose
// application window has closed and whose draw has not yet run.
const lotteryDrawInterval = 1 * time.Minute

// startLotteryDrawSweeper launches a background goroutine that periodically
// finds lottery sales phases whose window has closed (close_at <= now) and
// whose draw has not yet run (drawn_at IS NULL), then executes the draw for
// each. Individual phase failures are logged and skipped so one bad phase
// never blocks the rest of the sweep.
//
// The sweep stops when ctx is canceled (the shutdown signal). No separate
// drain phase is needed because the draw is idempotent: if a sweep is
// interrupted mid-draw, the phase's drawn_at remains NULL and the next tick
// retries the full draw safely.
func startLotteryDrawSweeper(ctx context.Context, uc usecase.LotteryUseCase, logger *logging.Logger) {
	go func() {
		ticker := time.NewTicker(lotteryDrawInterval)
		defer ticker.Stop()

		logger.Info(ctx, "lottery draw sweeper started",
			slog.Duration("interval", lotteryDrawInterval),
		)
		for {
			select {
			case <-ctx.Done():
				logger.Info(ctx, "lottery draw sweeper stopped")
				return
			case <-ticker.C:
				if err := uc.DrawDuePhases(ctx, time.Now()); err != nil {
					logger.Warn(ctx, "lottery draw sweep failed",
						slog.Any("error", err),
					)
				}
			}
		}
	}()
}
