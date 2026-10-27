package workflow

import (
	"context"

	"github.com/dbos-inc/dbos-transact-golang/dbos"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

const tracerName = "github.com/liverty-music/backend/internal/infrastructure/workflow"

// traceAttributes returns the caller's trace context (traceparent,
// tracestate) as workflow attributes, so the run continues the starter's
// trace (design "Trace context").
func traceAttributes(ctx context.Context) map[string]any {
	carrier := propagation.MapCarrier{}
	otel.GetTextMapPropagator().Inject(ctx, carrier)
	if len(carrier) == 0 {
		return nil
	}
	attrs := make(map[string]any, len(carrier))
	for k, v := range carrier {
		attrs[k] = v
	}
	return attrs
}

// startRunSpan starts the span of one run of the workflow status describes.
// The first run is a child of the trace context its starter stored; a run
// resumed after a stop starts a new trace linked to it, as a job item does
// (pkg/telemetry StartJobItem), because the starter's trace ended long ago.
func startRunSpan(ctx context.Context, status dbos.WorkflowStatus) (context.Context, trace.Span) {
	carrier := propagation.MapCarrier{}
	for k, v := range status.Attributes {
		if s, ok := v.(string); ok {
			carrier[k] = s
		}
	}
	stored := otel.GetTextMapPropagator().Extract(context.Background(), carrier)

	opts := []trace.SpanStartOption{
		trace.WithAttributes(
			attribute.String("workflow.name", status.Name),
			attribute.String("workflow.id", status.ID),
			attribute.Int("workflow.attempt", status.Attempts),
		),
	}
	parent := ctx
	if status.Attempts > 1 {
		opts = append(opts, trace.WithNewRoot(), trace.WithLinks(trace.LinkFromContext(stored)))
	} else {
		parent = trace.ContextWithRemoteSpanContext(ctx, trace.SpanContextFromContext(stored))
	}
	return otel.Tracer(tracerName).Start(parent, "workflow "+status.Name, opts...)
}

// startStepSpan starts the span of one step, a child of the run span.
func startStepSpan(ctx context.Context, name string, stepID int) (context.Context, trace.Span) {
	return otel.Tracer(tracerName).Start(ctx, "step "+name,
		trace.WithAttributes(
			attribute.String("workflow.step.name", name),
			attribute.Int("workflow.step.id", stepID),
		),
	)
}
