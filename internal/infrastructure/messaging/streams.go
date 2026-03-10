package messaging

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/liverty-music/backend/pkg/config"
	"github.com/nats-io/nats.go"
)

// PoisonQueueSubject is the NATS subject for messages that exceeded max retries.
const PoisonQueueSubject = "POISON.queue"

// natsConnectTimeout is the per-dial TCP timeout for NATS connections.
// Set higher than the default 2s to accommodate kube-proxy rule propagation
// on freshly provisioned GKE Autopilot Spot nodes.
const natsConnectTimeout = 5 * time.Second

// connectBackoff defines the exponential backoff intervals between
// NATS connection retry attempts during stream setup.
var connectBackoff = []time.Duration{
	1 * time.Second,
	2 * time.Second,
	4 * time.Second,
	8 * time.Second,
	15 * time.Second,
}

// streams defines the JetStream streams that must exist before
// publishers and subscribers can operate. Each stream captures
// all subjects matching <STREAM>.* for its domain aggregate.
var streams = []nats.StreamConfig{
	{
		Name:       "CONCERT",
		Subjects:   []string{"CONCERT.*"},
		Retention:  nats.LimitsPolicy,
		MaxAge:     7 * 24 * time.Hour,
		Storage:    nats.FileStorage,
		Discard:    nats.DiscardOld,
		Replicas:   1, // overridden per environment
		Duplicates: 2 * time.Minute,
	},
	{
		Name:       "VENUE",
		Subjects:   []string{"VENUE.*"},
		Retention:  nats.LimitsPolicy,
		MaxAge:     7 * 24 * time.Hour,
		Storage:    nats.FileStorage,
		Discard:    nats.DiscardOld,
		Replicas:   1,
		Duplicates: 2 * time.Minute,
	},
	{
		Name:       "ARTIST",
		Subjects:   []string{"ARTIST.*"},
		Retention:  nats.LimitsPolicy,
		MaxAge:     7 * 24 * time.Hour,
		Storage:    nats.FileStorage,
		Discard:    nats.DiscardOld,
		Replicas:   1,
		Duplicates: 2 * time.Minute,
	},
	{
		Name:       "POISON",
		Subjects:   []string{"POISON.*"},
		Retention:  nats.LimitsPolicy,
		MaxAge:     30 * 24 * time.Hour,
		Storage:    nats.FileStorage,
		Discard:    nats.DiscardOld,
		Replicas:   1,
		Duplicates: 2 * time.Minute,
	},
}

// EnsureStreams connects to NATS and creates or updates the required
// JetStream streams. The connection attempt is retried with exponential
// backoff when NATS is temporarily unreachable (e.g. during node
// provisioning). It is a no-op when NATS_URL is empty (local dev).
func EnsureStreams(ctx context.Context, cfg config.NATSConfig) error {
	if cfg.URL == "" {
		return nil
	}

	nc, err := connectWithRetry(ctx, cfg.URL)
	if err != nil {
		return fmt.Errorf("connect to NATS for stream setup: %w", err)
	}
	defer nc.Close()

	js, err := nc.JetStream()
	if err != nil {
		return fmt.Errorf("get JetStream context: %w", err)
	}

	for _, s := range streams {
		_, err := js.StreamInfo(s.Name)
		if err != nil && !errors.Is(err, nats.ErrStreamNotFound) {
			return fmt.Errorf("check stream %s: %w", s.Name, err)
		}
		if errors.Is(err, nats.ErrStreamNotFound) {
			// Stream does not exist, create it.
			if _, err := js.AddStream(&s); err != nil {
				return fmt.Errorf("create stream %s: %w", s.Name, err)
			}
			continue
		}
		// Stream exists, update it to match desired config.
		if _, err := js.UpdateStream(&s); err != nil {
			return fmt.Errorf("update stream %s: %w", s.Name, err)
		}
	}

	return nil
}

// connectWithRetry attempts to connect to NATS with exponential backoff.
// It returns the first successful connection or the last error if the
// context is cancelled or all attempts are exhausted.
func connectWithRetry(ctx context.Context, url string) (*nats.Conn, error) {
	var lastErr error

	for attempt := range connectBackoff {
		nc, err := nats.Connect(url,
			nats.MaxReconnects(-1),
			nats.ReconnectWait(time.Second),
			nats.Timeout(natsConnectTimeout),
		)
		if err == nil {
			if attempt > 0 {
				slog.Info("NATS connection established after retry",
					slog.Int("attempts", attempt+1),
				)
			}
			return nc, nil
		}

		lastErr = err
		delay := connectBackoff[attempt]

		slog.Warn("NATS connection failed, retrying",
			slog.Int("attempt", attempt+1),
			slog.Duration("delay", delay),
			slog.String("error", err.Error()),
		)

		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("%w (after %d attempts, last: %w)", context.Cause(ctx), attempt+1, lastErr)
		case <-time.After(delay):
		}
	}

	// Final attempt after exhausting backoff schedule.
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("%w (after %d attempts, last: %w)", context.Cause(ctx), len(connectBackoff), lastErr)
	}
	nc, err := nats.Connect(url,
		nats.MaxReconnects(-1),
		nats.ReconnectWait(time.Second),
		nats.Timeout(natsConnectTimeout),
	)
	if err != nil {
		return nil, fmt.Errorf("%w (after %d attempts)", err, len(connectBackoff)+1)
	}
	return nc, nil
}
