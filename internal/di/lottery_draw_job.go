package di

import (
	"context"
	"log/slog"
	"time"

	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-logging/logging"
)

// lotteryDrawInterval is how often the draw sweeper checks for Lottery sales
// whose window has closed and that are not drawn.
const lotteryDrawInterval = 1 * time.Minute

// startLotteryDrawSweeper launches a background goroutine that periodically
// draws every Lottery sale whose window has closed and that is not drawn
// (LotteryEntryUseCase.DrawDueSales). A failed sale is logged and skipped so it
// never blocks the rest of the sweep.
//
// The sweep stops when ctx is canceled (the shutdown signal). No separate
// drain phase is needed because the draw is idempotent: if a sweep is
// interrupted mid-draw, the sale's draw_at remains NULL and the next tick
// retries the full draw safely.
func startLotteryDrawSweeper(ctx context.Context, uc usecase.LotteryEntryUseCase, logger *logging.Logger) {
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
				if err := uc.DrawDueSales(ctx); err != nil {
					logger.Warn(ctx, "lottery draw sweep failed",
						slog.Any("error", err),
					)
				}
			}
		}
	}()
}
