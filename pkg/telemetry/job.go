package telemetry

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

const jobTracerName = "github.com/liverty-music/backend/pkg/telemetry/job"

// StartJobRun starts the span of one scheduled job run, named after the job.
// Work done with the returned context, including the events it publishes, is
// recorded in the run's trace. The caller ends the span when the run ends.
func StartJobRun(ctx context.Context, jobName string) (context.Context, trace.Span) {
	return otel.Tracer(jobTracerName).Start(ctx, jobName,
		trace.WithAttributes(attribute.String("job.name", jobName)),
	)
}

// StartJobItem starts the span of one item a job run processes (for example
// one artist search) as the root of a new trace, linked to the run span in
// runCtx. A run touches hundreds of items, so a trace per item keeps each one
// readable and addressable by its trace id, while the link keeps the run it
// belongs to answerable. The caller ends the span before the next item.
func StartJobItem(runCtx context.Context, spanName string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	return otel.Tracer(jobTracerName).Start(runCtx, spanName,
		trace.WithNewRoot(),
		trace.WithLinks(trace.LinkFromContext(runCtx)),
		trace.WithAttributes(attrs...),
	)
}
