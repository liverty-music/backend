package messaging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/ThreeDotsLabs/watermill/pubsub/gochannel"
	"github.com/nats-io/nats.go"

	watermillnats "github.com/ThreeDotsLabs/watermill-nats/v2/pkg/nats"

	"github.com/liverty-music/backend/pkg/config"
)

// ConnectNATS opens a shared *nats.Conn for use across all per-behavior
// subscribers. It registers the three connection lifecycle handlers so the
// ConsumerHealth reflects the real connection state immediately when NATS
// disconnects, reconnects, or closes — without waiting for a Subscribe call.
//
// The caller owns the returned connection and must drain/close it during
// shutdown (after all watermill subscribers have been closed).
func ConnectNATS(ctx context.Context, cfg config.NATSConfig, health *ConsumerHealth) (*nats.Conn, error) {
	nc, err := connectWithRetry(ctx, cfg.URL,
		// Reflect the live NATS connection state into the health tracker so a
		// dropped connection (which stops all consumption) makes the liveness
		// probe report unhealthy. These handlers are set once on the shared
		// conn, not repeated per-subscriber.
		nats.DisconnectErrHandler(func(_ *nats.Conn, _ error) {
			health.SetConnected(false)
		}),
		nats.ReconnectHandler(func(_ *nats.Conn) {
			health.SetConnected(true)
		}),
		nats.ClosedHandler(func(_ *nats.Conn) {
			health.SetConnected(false)
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("connect to NATS: %w", err)
	}
	return nc, nil
}

// natsConnDrainer adapts a *nats.Conn to io.Closer via Drain, so the shared
// connection can be registered with the shutdown manager. Drain flushes
// in-flight acks/publishes and unsubscribes before closing, which is the
// graceful counterpart to Close for a consumer connection.
type natsConnDrainer struct{ conn *nats.Conn }

// Close drains the underlying NATS connection (flush + unsubscribe + close).
func (d natsConnDrainer) Close() error { return d.conn.Drain() }

// NATSConnCloser wraps the shared consumer connection as an io.Closer that
// drains on shutdown. Register it in the shutdown External phase so it runs
// after the router has stopped consuming.
func NATSConnCloser(conn *nats.Conn) io.Closer { return natsConnDrainer{conn} }

// NewBehaviorSubscriber creates one watermill Subscriber for a single named
// behavior (handler). The behavior name is used as BOTH the JetStream durable
// name and the deliver (queue) group, so each handler has its own independent
// cursor on the stream. The shared *nats.Conn means no extra TCP connection
// per handler.
//
// The returned subscriber is wrapped in a healthTrackingSubscriber that
// records the behavior's bound state into health when Subscribe is called by
// the router at startup.
func NewBehaviorSubscriber(
	conn *nats.Conn,
	behavior string,
	wmLogger watermill.LoggerAdapter,
	health *ConsumerHealth,
) (message.Subscriber, error) {
	cfg := watermillnats.SubscriberSubscriptionConfig{
		CloseTimeout:   30 * time.Second,
		AckWaitTimeout: 30 * time.Second,
		// The SubjectCalculator returns the behavior name as the queue group
		// for every topic this subscriber is asked to subscribe to. Because
		// each per-behavior subscriber only ever subscribes to one topic, the
		// queue group is always the behavior name — independent of the subject.
		// This gives durable == deliver_group == behavior, which is the
		// property that prevents the shared-group misbind: sibling handlers
		// on the same stream each hold their own durable, so nats.go cannot
		// accidentally bind a new durable to an existing sibling's durable.
		SubjectCalculator: func(_, topic string) *watermillnats.SubjectDetail {
			return &watermillnats.SubjectDetail{
				Primary:    topic,
				QueueGroup: behavior,
			}
		},
		JetStream: watermillnats.JetStreamConfig{
			// DurableCalculator ignores the topic and always returns the
			// behavior name. Combined with SubjectCalculator above this
			// guarantees durable == deliver_group == behavior regardless of
			// which subject is passed to Subscribe.
			DurableCalculator: func(_, _ string) string {
				return behavior
			},
			SubscribeOptions: []nats.SubOpt{
				nats.AckExplicit(),
				nats.DeliverNew(),
			},
		},
	}

	sub, err := watermillnats.NewSubscriberWithNatsConn(conn, cfg, wmLogger)
	if err != nil {
		return nil, fmt.Errorf("create behavior subscriber for %q: %w", behavior, err)
	}

	return &healthTrackingSubscriber{
		Subscriber: sub,
		health:     health,
		behavior:   behavior,
	}, nil
}

// NewSubscriber creates a single Watermill Subscriber based on configuration.
// When NATS_URL is empty it returns the provided GoChannel (local dev path).
// When NATS_URL is set it opens its own connection — used only by the
// publisher and in tests; the consumer app uses ConnectNATS +
// NewBehaviorSubscriber to get per-handler durable isolation instead.
//
// The returned NATS subscriber wraps with healthTrackingSubscriber that
// records per-topic bound state into health. The GoChannel path has no
// connection to lose and is returned unwrapped.
func NewSubscriber(cfg config.NATSConfig, wmLogger watermill.LoggerAdapter, goChannel *gochannel.GoChannel, health *ConsumerHealth) (message.Subscriber, error) {
	if cfg.URL == "" {
		if goChannel == nil {
			return nil, fmt.Errorf("GoChannel is required when NATS_URL is not set")
		}
		return goChannel, nil
	}

	sub, err := watermillnats.NewSubscriber(watermillnats.SubscriberConfig{
		URL: cfg.URL,
		NatsOptions: []nats.Option{
			nats.MaxReconnects(-1),
			nats.ReconnectWait(time.Second),
			nats.DisconnectErrHandler(func(_ *nats.Conn, _ error) {
				health.SetConnected(false)
			}),
			nats.ReconnectHandler(func(_ *nats.Conn) {
				health.SetConnected(true)
			}),
			nats.ClosedHandler(func(_ *nats.Conn) {
				health.SetConnected(false)
			}),
		},
		CloseTimeout:   30 * time.Second,
		AckWaitTimeout: 30 * time.Second,
		JetStream: watermillnats.JetStreamConfig{
			SubscribeOptions: []nats.SubOpt{
				nats.AckExplicit(),
				nats.DeliverNew(),
			},
		},
	}, wmLogger)
	if err != nil {
		return nil, fmt.Errorf("create NATS subscriber: %w", err)
	}

	return &healthTrackingSubscriber{Subscriber: sub, health: health}, nil
}

// jsConsumerLister is the minimal JetStream surface consumed by
// ReconcileConsumers. Accepting an interface rather than a concrete
// *nats.JetStreamContext makes the reconcile logic unit-testable without a
// live NATS server.
type jsConsumerLister interface {
	// ConsumersInfo returns a channel of *nats.ConsumerInfo for the named stream.
	ConsumersInfo(stream string, opts ...nats.JSOpt) <-chan *nats.ConsumerInfo
	// DeleteConsumer removes the named consumer from the named stream.
	DeleteConsumer(stream, consumer string, opts ...nats.JSOpt) error
}

// reconcileReason encodes why a stale durable was deleted, for structured
// logging at INFO level.
type reconcileReason string

const (
	reasonNotDesired         reconcileReason = "not in desired set"
	reasonSharedDeliverGroup reconcileReason = "deliver_group != name (shared group)"
	reasonWrongDeliverPolicy reconcileReason = "deliver_policy != DeliverNew"
)

// ReconcileConsumers deletes JetStream durables that no longer match the
// desired behavior-named set. It is a no-op when NATS_URL is empty (local
// dev). It must be called after EnsureStreams and before the watermill
// router subscribes, so stale durables are cleared before new per-behavior
// durables are created.
//
// A consumer is deleted when ANY of the following hold:
//   - its name is not in desired (removes old consumer_* prefixed durables,
//     old subject-named durables, dead orphans such as ACCOUNT.login);
//   - its Config.DeliverGroup does not equal its own name (shared "consumer"
//     group or any other drift that causes the misbind bug);
//   - its Config.DeliverPolicy is not nats.DeliverNewPolicy.
//
// Consumers in the desired set that already satisfy all three conditions are
// left untouched — watermill will bind to them on Subscribe. Consumers on
// streams not in the configured stream list are never touched.
func ReconcileConsumers(ctx context.Context, cfg config.NATSConfig, desired []string, logger *slog.Logger) error {
	if cfg.URL == "" {
		return nil
	}

	nc, err := connectWithRetry(ctx, cfg.URL)
	if err != nil {
		return fmt.Errorf("connect to NATS for reconcile: %w", err)
	}
	defer nc.Drain() //nolint:errcheck

	js, err := nc.JetStream()
	if err != nil {
		return fmt.Errorf("get JetStream context for reconcile: %w", err)
	}

	return reconcileConsumers(ctx, js, desired, logger)
}

// reconcileConsumers is the testable inner function that accepts the
// jsConsumerLister interface.
func reconcileConsumers(ctx context.Context, js jsConsumerLister, desired []string, logger *slog.Logger) error {
	desiredSet := make(map[string]struct{}, len(desired))
	for _, name := range desired {
		desiredSet[name] = struct{}{}
	}

	for _, stream := range streams {
		ch := js.ConsumersInfo(stream.Name)
		for info := range ch {
			if info == nil {
				continue
			}

			reason, stale := classifyConsumer(info, desiredSet)
			if !stale {
				continue
			}

			logger.InfoContext(ctx, "deleting stale JetStream consumer",
				slog.String("stream", stream.Name),
				slog.String("consumer", info.Name),
				slog.String("reason", string(reason)),
			)

			if err := js.DeleteConsumer(stream.Name, info.Name); err != nil {
				// Log but do not abort: one unremovable consumer should not
				// prevent the others from being cleaned up, and the subscriber
				// will fail loudly at startup if a durable it needs is broken.
				logger.ErrorContext(ctx, "failed to delete stale JetStream consumer",
					slog.String("stream", stream.Name),
					slog.String("consumer", info.Name),
					slog.String("error", err.Error()),
				)
			}
		}
	}
	return nil
}

// classifyConsumer reports whether a JetStream consumer is stale and why.
// A consumer is stale when it is not in the desired set, has a deliver group
// that differs from its own name (shared-group misbind), or uses a deliver
// policy other than DeliverNew.
func classifyConsumer(info *nats.ConsumerInfo, desired map[string]struct{}) (reconcileReason, bool) {
	if _, ok := desired[info.Name]; !ok {
		return reasonNotDesired, true
	}
	if info.Config.DeliverGroup != info.Name {
		return reasonSharedDeliverGroup, true
	}
	if info.Config.DeliverPolicy != nats.DeliverNewPolicy {
		return reasonWrongDeliverPolicy, true
	}
	return "", false
}

// healthTrackingSubscriber wraps a Watermill subscriber and records each
// topic's bound state into a ConsumerHealth. watermill establishes every
// subscription synchronously at router startup, so a topic is marked bound
// the moment its Subscribe succeeds and marked unbound if it fails — letting
// the liveness probe distinguish a fully-consuming pod from a wedged one.
//
// When behavior is non-empty (per-behavior subscriber created by
// NewBehaviorSubscriber) the health key is the behavior name. When behavior
// is empty (legacy single-subscriber path) the key falls back to the topic
// passed to Subscribe — preserving the original semantics.
type healthTrackingSubscriber struct {
	message.Subscriber
	health   *ConsumerHealth
	behavior string
}

// Subscribe delegates to the wrapped subscriber and records the topic's bound
// state. On failure it marks the topic unbound (and the router startup fails
// loud); on success it marks the topic bound.
func (s *healthTrackingSubscriber) Subscribe(ctx context.Context, topic string) (<-chan *message.Message, error) {
	key := s.healthKey(topic)
	s.health.Expect(key)

	msgs, err := s.Subscriber.Subscribe(ctx, topic)
	if err != nil {
		s.health.MarkUnbound(key)
		return nil, err
	}

	s.health.MarkBound(key)
	return msgs, nil
}

// SubscribeInitialize forwards subscription pre-provisioning to the wrapped
// subscriber when it supports it, keeping this decorator transparent so it
// does not hide capabilities the underlying watermill subscriber exposes.
func (s *healthTrackingSubscriber) SubscribeInitialize(topic string) error {
	if init, ok := s.Subscriber.(message.SubscribeInitializer); ok {
		return init.SubscribeInitialize(topic)
	}
	return nil
}

// healthKey returns the key used to track this subscription in ConsumerHealth.
// Per-behavior subscribers use the behavior name (so health reflects handler
// identity, not subject). The legacy single-subscriber path falls back to topic.
func (s *healthTrackingSubscriber) healthKey(topic string) string {
	if s.behavior != "" {
		return s.behavior
	}
	return topic
}
