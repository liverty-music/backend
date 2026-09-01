package messaging

import (
	"context"
	"fmt"
	"io"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/message"
	natsgo "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/liverty-music/backend/pkg/config"
)

// ConnectNATS opens a shared *nats.Conn for use across all per-behavior
// subscribers. It registers the three connection lifecycle handlers so the
// ConsumerHealth reflects the real connection state immediately when NATS
// disconnects, reconnects, or closes — without waiting for a Subscribe call.
//
// The caller owns the returned connection and must drain/close it during
// shutdown (after all watermill subscribers have been closed).
func ConnectNATS(ctx context.Context, cfg config.NATSConfig, health *ConsumerHealth) (*natsgo.Conn, error) {
	nc, err := connectWithRetry(ctx, cfg.URL,
		// Reflect the live NATS connection state into the health tracker so a
		// dropped connection (which stops all consumption) makes the liveness
		// probe report unhealthy. These handlers are set once on the shared
		// conn, not repeated per-subscriber.
		natsgo.DisconnectErrHandler(func(_ *natsgo.Conn, _ error) {
			health.SetConnected(false)
		}),
		natsgo.ReconnectHandler(func(_ *natsgo.Conn) {
			health.SetConnected(true)
		}),
		natsgo.ClosedHandler(func(_ *natsgo.Conn) {
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
type natsConnDrainer struct{ conn *natsgo.Conn }

// Close drains the underlying NATS connection (flush + unsubscribe + close).
func (d natsConnDrainer) Close() error { return d.conn.Drain() }

// NATSConnCloser wraps the shared consumer connection as an io.Closer that
// drains on shutdown. Register it in the shutdown External phase so it runs
// after the router has stopped consuming.
func NATSConnCloser(conn *natsgo.Conn) io.Closer { return natsConnDrainer{conn} }

// pullSubscriber is a watermill message.Subscriber backed by the nats.go
// jetstream pull API. It binds to a pre-existing durable created by NACK —
// it never creates or updates durables. One pullSubscriber is constructed per
// behavior; Subscribe is called exactly once by the watermill router at
// startup.
//
// Bind-only guarantee: only jetstream.Consumer (lookup) is called; never
// CreateConsumer / CreateOrUpdateConsumer / AddConsumer.
type pullSubscriber struct {
	conn     *natsgo.Conn
	behavior string
	wmLogger watermill.LoggerAdapter
	health   *ConsumerHealth

	// consumeCtx is the handle returned by Consumer.Consume. It is stored so
	// Close can stop delivery regardless of whether Subscribe has been called.
	consumeCtx jetstream.ConsumeContext
}

// NewBehaviorSubscriber creates a watermill Subscriber for a single named
// behavior. The behavior name is used as BOTH the JetStream durable name and
// the deliver (queue) group; it must already exist in NATS (created by NACK).
// The subscriber binds to the durable via a pull consumer — it never creates
// or updates durables.
//
// The returned subscriber is wrapped in a healthTrackingSubscriber that
// records the behavior's bound state into health when Subscribe is called by
// the router at startup.
func NewBehaviorSubscriber(
	conn *natsgo.Conn,
	behavior string,
	wmLogger watermill.LoggerAdapter,
	health *ConsumerHealth,
) (message.Subscriber, error) {
	sub := &pullSubscriber{
		conn:     conn,
		behavior: behavior,
		wmLogger: wmLogger,
		health:   health,
	}
	return &healthTrackingSubscriber{
		Subscriber: sub,
		health:     health,
		behavior:   behavior,
	}, nil
}

// Subscribe binds to the pre-existing durable named s.behavior on the stream
// that covers topic, then starts a pull-based continuous delivery loop.
// Incoming JetStream messages are forwarded to the returned channel; each
// message waits for an Ack or Nack from the watermill handler before the next
// delivery window opens.
//
// Subscribe must be called exactly once (watermill router calls it at startup).
func (s *pullSubscriber) Subscribe(ctx context.Context, topic string) (<-chan *message.Message, error) {
	stream, ok := StreamForSubject(topic)
	if !ok {
		return nil, fmt.Errorf("no JetStream stream covers subject %q; add it to the streams registry", topic)
	}

	js, err := jetstream.New(s.conn)
	if err != nil {
		return nil, fmt.Errorf("create jetstream context for behavior %q: %w", s.behavior, err)
	}

	// Bind to the pre-existing durable. js.Consumer errors loudly when the
	// durable is absent — which is the desired fail-fast behaviour.
	cons, err := js.Consumer(ctx, stream, s.behavior)
	if err != nil {
		return nil, fmt.Errorf("bind durable %q on stream %q: %w", s.behavior, stream, err)
	}

	out := make(chan *message.Message)

	cc, err := cons.Consume(func(m jetstream.Msg) {
		// Build a watermill message. The payload stays equal to m.Data() so
		// handlers that call messaging.ParseCloudEventData receive the raw bytes.
		// Copy JetStream headers into watermill Metadata for completeness.
		wmMsg := message.NewMessage(watermill.NewUUID(), m.Data())
		for k, vals := range m.Headers() {
			if len(vals) > 0 {
				wmMsg.Metadata.Set(k, vals[0])
			}
		}

		select {
		case out <- wmMsg:
		case <-ctx.Done():
			// Context cancelled before the message could be dispatched; nack so
			// it is redelivered to the next consumer instance.
			_ = m.Nak() //nolint:errcheck
			return
		}

		// Wait for the watermill handler to ack or nack.
		select {
		case <-wmMsg.Acked():
			_ = m.Ack() //nolint:errcheck
		case <-wmMsg.Nacked():
			_ = m.Nak() //nolint:errcheck
		case <-ctx.Done():
			_ = m.Nak() //nolint:errcheck
		}
	})
	if err != nil {
		close(out)
		return nil, fmt.Errorf("start pull consume for behavior %q: %w", s.behavior, err)
	}

	s.consumeCtx = cc

	// Stop the consume loop when the context is cancelled so the router can
	// shut down cleanly. Drain (not Stop) so any in-flight callback finishes
	// before we close(out) — Stop returns without waiting for a running
	// callback, which could then send on a closed channel and panic. The
	// callbacks observe the same ctx.Done() and nak promptly, so Drain returns
	// quickly on shutdown.
	go func() {
		<-ctx.Done()
		cc.Drain()
		close(out)
	}()

	return out, nil
}

// Close stops the pull consume loop and releases the NATS subscription.
// It is safe to call Close before Subscribe (no-op in that case).
func (s *pullSubscriber) Close() error {
	if s.consumeCtx != nil {
		s.consumeCtx.Stop()
	}
	return nil
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
