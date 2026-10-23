// Package messaging provides Watermill-based event messaging infrastructure.
// It initializes NATS JetStream or GoChannel publishers and subscribers
// depending on configuration, and provides CloudEvents metadata helpers.
package messaging

import (
	"context"
	"fmt"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/ThreeDotsLabs/watermill/pubsub/gochannel"
	"github.com/nats-io/nats.go"
	wotel "github.com/voi-oss/watermill-opentelemetry/pkg/opentelemetry"

	watermillnats "github.com/ThreeDotsLabs/watermill-nats/v2/pkg/nats"

	"github.com/liverty-music/backend/pkg/config"
	"github.com/pannpers/go-logging/logging"
)

// NewPublisher creates a Watermill Publisher based on configuration.
// When NATS_URL is set, it returns a NATS JetStream publisher.
// When NATS_URL is empty (local development), it returns a GoChannel publisher
// using the provided GoChannel instance.
//
// The NATS publisher does not wait for the broker: when it is unreachable, the
// connection keeps retrying in the background and a publish fails after the
// JetStream ack timeout (5 s), so a server starts and serves calls that do not
// need the broker. Connection state changes are logged.
//
// Every published message carries the caller's trace context in its metadata
// (see traceContextPublisher), next to the CloudEvents attributes.
func NewPublisher(cfg config.NATSConfig, wmLogger watermill.LoggerAdapter, goChannel *gochannel.GoChannel, logger *logging.Logger) (message.Publisher, error) {
	if cfg.URL == "" {
		if goChannel == nil {
			return nil, fmt.Errorf("GoChannel is required when NATS_URL is not set")
		}
		return wotel.NewPublisherDecorator(traceContextPublisher{goChannel}), nil
	}

	ctx := context.Background()
	pub, err := watermillnats.NewPublisher(watermillnats.PublisherConfig{
		URL: cfg.URL,
		NatsOptions: append(baseNATSOptions(),
			nats.ConnectHandler(func(_ *nats.Conn) {
				logger.Info(ctx, "NATS publisher connected")
			}),
			nats.DisconnectErrHandler(warnOnDisconnect(ctx, logger, "NATS publisher disconnected")),
			nats.ReconnectHandler(func(_ *nats.Conn) {
				logger.Info(ctx, "NATS publisher reconnected")
			}),
		),
		JetStream: watermillnats.JetStreamConfig{
			TrackMsgID: true,
		},
	}, wmLogger)
	if err != nil {
		return nil, fmt.Errorf("create NATS publisher: %w", err)
	}

	return wotel.NewPublisherDecorator(traceContextPublisher{pub}), nil
}
