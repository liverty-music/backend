package messaging

import (
	"time"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/ThreeDotsLabs/watermill/message/router/middleware"
	wotel "github.com/voi-oss/watermill-opentelemetry/pkg/opentelemetry"
)

// NewRouter creates a Watermill Router with standard middleware.
// The router manages message handlers and provides retry, poison queue,
// and logging middleware.
func NewRouter(wmLogger watermill.LoggerAdapter, poisonQueuePub message.Publisher, poisonQueueTopic string) (*message.Router, error) {
	router, err := message.NewRouter(message.RouterConfig{}, wmLogger)
	if err != nil {
		return nil, err
	}

	// Poison queue: move messages that exceed max retries.
	pq, err := middleware.PoisonQueue(poisonQueuePub, poisonQueueTopic)
	if err != nil {
		return nil, err
	}
	router.AddMiddleware(pq)

	// Retry failed handler invocations with exponential backoff.
	router.AddMiddleware(middleware.Retry{
		MaxRetries:      3,
		InitialInterval: 500 * time.Millisecond,
		Multiplier:      2.0,
		Logger:          wmLogger,
	}.Middleware)

	// Recoverer: catch panics and nack the message.
	router.AddMiddleware(middleware.Recoverer)

	// OpenTelemetry: propagate trace context via message metadata.
	router.AddMiddleware(wotel.Trace())

	return router, nil
}
