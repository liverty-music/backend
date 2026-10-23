package messaging

import (
	"github.com/ThreeDotsLabs/watermill/message"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
)

// traceContextPublisher writes each message's trace context into its metadata
// (W3C traceparent and tracestate) so that it crosses the broker. It sits
// inside the watermill-opentelemetry publisher decorator, which has already
// put the producer span on the message context, so the consumer continues
// the trace as a child of the publish step.
type traceContextPublisher struct {
	pub message.Publisher
}

// Publish injects the trace context into every message and delegates to the
// wrapped publisher.
func (p traceContextPublisher) Publish(topic string, messages ...*message.Message) error {
	propagator := otel.GetTextMapPropagator()
	for _, msg := range messages {
		propagator.Inject(msg.Context(), propagation.MapCarrier(msg.Metadata))
	}
	return p.pub.Publish(topic, messages...)
}

// Close closes the wrapped publisher.
func (p traceContextPublisher) Close() error {
	return p.pub.Close()
}

// extractTraceContext restores the trace context that traceContextPublisher
// wrote into the message metadata, so the consumer span started by the next
// middleware is a child of the producer span. A message without trace context
// keeps its context unchanged and is handled in a new trace.
func extractTraceContext(h message.HandlerFunc) message.HandlerFunc {
	return func(msg *message.Message) ([]*message.Message, error) {
		ctx := otel.GetTextMapPropagator().Extract(msg.Context(), propagation.MapCarrier(msg.Metadata))
		msg.SetContext(ctx)
		return h(msg)
	}
}
