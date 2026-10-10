package messaging

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/pannpers/go-logging/logging"
)

// natsConnectTimeout is the per-dial TCP timeout for NATS connections.
// Set higher than the default 2s to accommodate kube-proxy rule propagation
// on freshly provisioned GKE Autopilot Spot nodes.
const natsConnectTimeout = 5 * time.Second

// natsStartupBudget is how long a consumer waits for its first NATS
// connection before it gives up. Prod NATS is a single pod on Spot nodes; a
// reschedule takes a few minutes, and the consumer must outlast it instead of
// crash-looping.
const natsStartupBudget = 5 * time.Minute

const (
	natsReconnectInitialDelay = time.Second
	natsReconnectMaxDelay     = 15 * time.Second
)

// baseNATSOptions are the options every NATS connection uses: reconnect
// forever with backoff, and when the broker is unreachable at start, return a
// connection in RECONNECTING state instead of an error. A connection created
// this way buffers publishes until it connects.
func baseNATSOptions() []nats.Option {
	return []nats.Option{
		nats.MaxReconnects(-1),
		nats.CustomReconnectDelay(natsReconnectDelay),
		nats.Timeout(natsConnectTimeout),
		nats.RetryOnFailedConnect(true),
	}
}

// natsReconnectDelay doubles the wait from 1 s up to 15 s over the failed
// attempts of one outage (nats.go counts them from 1), like the database wait,
// so a 5-minute outage costs about 25 attempts instead of 300. Up to 20 %
// jitter keeps pods from retrying in lockstep.
func natsReconnectDelay(attempts int) time.Duration {
	delay := natsReconnectMaxDelay
	if attempts < 5 { // 1 s << 4 = 16 s already exceeds the cap
		delay = min(natsReconnectInitialDelay<<(attempts-1), natsReconnectMaxDelay)
	}
	return delay + rand.N(delay/5)
}

// connectWithRetry connects to NATS and blocks until the first connection is
// established, ctx ends, or budget elapses. Each failed attempt is logged at
// WARNING. Extra options are appended after the baseline options so callers
// may register connection lifecycle handlers on the returned connection; they
// must not set ConnectHandler or ReconnectErrHandler, which this function owns.
func connectWithRetry(ctx context.Context, url string, budget time.Duration, logger *logging.Logger, extra ...nats.Option) (*nats.Conn, error) {
	connected := make(chan struct{})
	var once sync.Once
	var attempts atomic.Int64
	var lastErr atomic.Pointer[error]

	opts := append(baseNATSOptions(),
		nats.ConnectHandler(func(_ *nats.Conn) {
			once.Do(func() { close(connected) })
		}),
		nats.ReconnectErrHandler(func(_ *nats.Conn, err error) {
			if err == nil {
				return
			}
			lastErr.Store(&err)
			logger.Warn(ctx, "NATS connection failed, retrying",
				slog.Int64("attempt", attempts.Add(1)),
				slog.String("error", err.Error()),
			)
		}),
	)
	opts = append(opts, extra...)

	nc, err := nats.Connect(url, opts...)
	if err != nil {
		// With RetryOnFailedConnect, only invalid options fail here.
		return nil, err
	}

	timer := time.NewTimer(budget)
	defer timer.Stop()

	select {
	case <-connected:
		if n := attempts.Load(); n > 0 {
			logger.Info(ctx, "NATS connection established after retry",
				slog.Int64("failed_attempts", n),
			)
		}
		return nc, nil
	case <-ctx.Done():
		nc.Close()
		return nil, fmt.Errorf("wait for NATS: %w", context.Cause(ctx))
	case <-timer.C:
		nc.Close()
		if cause := lastErr.Load(); cause != nil {
			return nil, fmt.Errorf("NATS unreachable for %s (%d attempts, last: %w)", budget, attempts.Load(), *cause)
		}
		return nil, fmt.Errorf("NATS unreachable for %s", budget)
	}
}
