package messaging

import (
	"fmt"

	"github.com/ThreeDotsLabs/watermill/message"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"

	wotel "github.com/voi-oss/watermill-opentelemetry/pkg/opentelemetry"
)

// newTracedPublisher records a send (producer) span named name for every
// publish and injects that span's context (W3C traceparent and tracestate)
// into the message metadata, so it crosses the broker and the consumer
// continues the trace as a child of the publish step. This follows the
// OpenTelemetry messaging conventions: the send span's context is the one
// injected. The span name is explicit because the decorator would otherwise
// name the span after the wrapped publisher's type.
func newTracedPublisher(name string, pub message.Publisher) (message.Publisher, error) {
	// The decorator calls the inner publisher after it has put the producer
	// span on the message context, so the transform injects that span.
	injecting, err := message.MessageTransformPublisherDecorator(func(msg *message.Message) {
		otel.GetTextMapPropagator().Inject(msg.Context(), propagation.MapCarrier(msg.Metadata))
	})(pub)
	if err != nil {
		return nil, fmt.Errorf("decorate publisher with trace context: %w", err)
	}
	return wotel.NewNamedPublisherDecorator(name, injecting), nil
}

// extractTraceContext restores the trace context that newTracedPublisher
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
