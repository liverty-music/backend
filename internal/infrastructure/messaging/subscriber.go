package messaging

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/ThreeDotsLabs/watermill/pubsub/gochannel"
	"github.com/nats-io/nats.go"

	watermillnats "github.com/ThreeDotsLabs/watermill-nats/v2/pkg/nats"

	"github.com/liverty-music/backend/pkg/config"
)

// consumerQueueGroupPrefix is the prefix for the per-topic JetStream deliver
// (queue) group and durable name. Bumped from the historical bare "consumer"
// group to segregate consumers per subject (see SubjectCalculator in
// NewSubscriber for the collision this avoids).
const consumerQueueGroupPrefix = "consumer"

// consumerName derives a per-topic durable / deliver-group name from a subject,
// e.g. "NOTIFICATION.delivered" -> "consumer_NOTIFICATION_delivered". Keeping
// the durable and deliver group identical and unique per topic guarantees each
// subject on a shared stream provisions its own JetStream consumer.
func consumerName(topic string) string {
	return consumerQueueGroupPrefix + "_" + strings.ReplaceAll(topic, ".", "_")
}

// NewSubscriber creates a Watermill Subscriber based on configuration.
// When NATS_URL is set, it returns a NATS JetStream subscriber with durable consumers.
// When NATS_URL is empty (local development), it returns a GoChannel subscriber
// using the provided GoChannel instance.
//
// The returned NATS subscriber reports its connection and per-topic bound state
// into health so the consumer's liveness probe reflects real consumption. The
// GoChannel (local) path has no connection to lose and is returned unwrapped.
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
			// Reflect the live NATS connection state into the health tracker so
			// a dropped connection (which stops all consumption) makes the
			// liveness probe report unhealthy.
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
		QueueGroupPrefix: consumerQueueGroupPrefix,
		CloseTimeout:     30 * time.Second,
		AckWaitTimeout:   30 * time.Second,
		// Derive BOTH the JetStream deliver (queue) group and the durable name
		// per topic. This is required — not cosmetic — because several subjects
		// share one stream (e.g. NOTIFICATION.subscribed/.unsubscribed/.delivered
		// all live in the NOTIFICATION stream). nats.go's QueueSubscribe, when a
		// durable does not yet exist, looks up an existing consumer on the stream
		// by deliver group; with a single shared group it FINDS a sibling and
		// binds to it instead of creating the new consumer — so the second and
		// third subjects on a stream silently never get a consumer and their
		// messages pile up unconsumed. A per-topic deliver group removes the
		// collision so every subject provisions its own consumer.
		SubjectCalculator: func(_, topic string) *watermillnats.SubjectDetail {
			return &watermillnats.SubjectDetail{
				Primary:    topic,
				QueueGroup: consumerName(topic),
			}
		},
		JetStream: watermillnats.JetStreamConfig{
			DurableCalculator: func(_, topic string) string {
				return consumerName(topic)
			},
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

// healthTrackingSubscriber wraps a Watermill subscriber and records each
// topic's bound state into a ConsumerHealth. watermill establishes every
// subscription synchronously at router startup, so a topic is marked bound the
// moment its Subscribe succeeds and marked unbound if it fails — letting the
// liveness probe distinguish a fully-consuming pod from a wedged one.
type healthTrackingSubscriber struct {
	message.Subscriber
	health *ConsumerHealth
}

// Subscribe delegates to the wrapped subscriber and records the topic's bound
// state. On failure it marks the topic unbound (and the router startup fails
// loud); on success it marks the topic bound.
func (s *healthTrackingSubscriber) Subscribe(ctx context.Context, topic string) (<-chan *message.Message, error) {
	s.health.Expect(topic)

	msgs, err := s.Subscriber.Subscribe(ctx, topic)
	if err != nil {
		s.health.MarkUnbound(topic)
		return nil, err
	}

	s.health.MarkBound(topic)
	return msgs, nil
}

// SubscribeInitialize forwards subscription pre-provisioning to the wrapped
// subscriber when it supports it, keeping this decorator transparent so it does
// not hide capabilities the underlying watermill subscriber exposes.
func (s *healthTrackingSubscriber) SubscribeInitialize(topic string) error {
	if init, ok := s.Subscriber.(message.SubscribeInitializer); ok {
		return init.SubscribeInitialize(topic)
	}
	return nil
}
