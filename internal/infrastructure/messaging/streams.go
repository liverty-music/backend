package messaging

import (
	"errors"
	"fmt"
	"time"

	"github.com/liverty-music/backend/pkg/config"
	"github.com/nats-io/nats.go"
)

// PoisonQueueSubject is the NATS subject for messages that exceeded max retries.
const PoisonQueueSubject = "POISON.queue"

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
// JetStream streams. It is a no-op when NATS_URL is empty (local dev).
func EnsureStreams(cfg config.NATSConfig) error {
	if cfg.URL == "" {
		return nil
	}

	nc, err := nats.Connect(cfg.URL,
		nats.MaxReconnects(-1),
		nats.ReconnectWait(time.Second),
	)
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
