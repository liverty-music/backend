// Package messaging provides Watermill-based event messaging infrastructure.
// It initializes NATS JetStream or GoChannel publishers and subscribers
// depending on configuration, and provides CloudEvents metadata helpers.
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

// NewPublisher creates a Watermill Publisher based on configuration.
// When NATS_URL is set, it returns a NATS JetStream publisher.
// When NATS_URL is empty (local development), it returns a GoChannel publisher
// using the provided GoChannel instance.
func NewPublisher(cfg config.NATSConfig, wmLogger watermill.LoggerAdapter, goChannel *gochannel.GoChannel) (message.Publisher, error) {
	if cfg.URL == "" {
		if goChannel == nil {
			return nil, fmt.Errorf("GoChannel is required when NATS_URL is not set")
		}
		return goChannel, nil
	}

	pub, err := watermillnats.NewPublisher(watermillnats.PublisherConfig{
		URL: cfg.URL,
		NatsOptions: []nats.Option{
			nats.MaxReconnects(-1),
			nats.ReconnectWait(time.Second),
		},
		JetStream: watermillnats.JetStreamConfig{
			AutoProvision: true,
			TrackMsgId:    true,
		},
	}, wmLogger)
	if err != nil {
		return nil, fmt.Errorf("create NATS publisher: %w", err)
	}

	return pub, nil
}
