package messaging

import (
	"fmt"
	"time"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/ThreeDotsLabs/watermill/pubsub/gochannel"
	"github.com/nats-io/nats.go"

	watermillnats "github.com/ThreeDotsLabs/watermill-nats/v2/pkg/nats"

	"github.com/liverty-music/backend/pkg/config"
)

// NewSubscriber creates a Watermill Subscriber based on configuration.
// When NATS_URL is set, it returns a NATS JetStream subscriber with durable consumers.
// When NATS_URL is empty (local development), it returns a GoChannel subscriber
// using the provided GoChannel instance.
func NewSubscriber(cfg config.NATSConfig, wmLogger watermill.LoggerAdapter, goChannel *gochannel.GoChannel) (message.Subscriber, error) {
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
		},
		QueueGroupPrefix: "consumer",
		CloseTimeout:     30 * time.Second,
		AckWaitTimeout:   30 * time.Second,
		JetStream: watermillnats.JetStreamConfig{
			AutoProvision: true,
			DurablePrefix: "consumer",
		},
	}, wmLogger)
	if err != nil {
		return nil, fmt.Errorf("create NATS subscriber: %w", err)
	}

	return sub, nil
}
