package rdb

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/pannpers/go-logging/logging"
)

const (
	// startupBudget is how long New keeps pinging the database before it gives
	// up, so a brief outage at startup (a Cloud SQL maintenance restart, a PSC
	// endpoint not yet programmed on a new node) does not end the process.
	startupBudget = 5 * time.Minute

	startupInitialDelay = time.Second
	startupMaxDelay     = 15 * time.Second
)

// clock is the time source waitForDatabase waits on; tests replace it.
type clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
}

// wallClock is the real time source.
type wallClock struct{}

// Now returns the current time.
func (wallClock) Now() time.Time { return time.Now() }

// After waits for d on the real clock.
func (wallClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

// waitForDatabase calls ping until it succeeds, with exponential backoff from
// 1 s up to 15 s, for startupBudget in total. Each failed attempt is logged at
// WARNING; the final failure is returned, naming the database, for main to log
// once at ERROR. It stops early when ctx ends.
func waitForDatabase(ctx context.Context, ping func(context.Context) error, logger *logging.Logger, clk clock) error {
	start := clk.Now()
	delay := startupInitialDelay
	for attempt := 1; ; attempt++ {
		err := ping(ctx)
		if err == nil {
			if attempt > 1 {
				logger.Info(ctx, "database reachable after retry", slog.Int("attempts", attempt))
			}
			return nil
		}
		if clk.Now().Sub(start) >= startupBudget {
			return fmt.Errorf("database unreachable for %s (%d attempts): %w", startupBudget, attempt, err)
		}
		logger.Warn(ctx, "database unreachable, retrying",
			slog.Int("attempt", attempt),
			slog.Duration("delay", delay),
			slog.String("error", err.Error()),
		)
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for database: %w (last: %w)", context.Cause(ctx), err)
		case <-clk.After(delay):
		}
		delay = min(delay*2, startupMaxDelay)
	}
}
