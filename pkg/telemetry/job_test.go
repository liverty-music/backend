package telemetry_test

import (
	"context"
	"testing"

	"github.com/liverty-music/backend/pkg/telemetry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

// installSpanRecorder makes an in-memory span recorder the global tracer
// provider, which the job span helpers read. Tests that call it must not run
// in parallel, and parallel tests in this package replace the global later.
func installSpanRecorder(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	recorder := tracetest.NewSpanRecorder()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder)))
	return recorder
}

// Global tracer provider: not parallel (see installSpanRecorder).
func TestStartJobRun(t *testing.T) {
	recorder := installSpanRecorder(t)

	ctx, run := telemetry.StartJobRun(context.Background(), "concert-discovery")
	run.End()

	ended := recorder.Ended()
	require.Len(t, ended, 1)
	assert.Equal(t, "concert-discovery", ended[0].Name())
	assert.False(t, ended[0].Parent().IsValid(), "a run is the root of its trace")
	assert.Contains(t, ended[0].Attributes(), attribute.String("job.name", "concert-discovery"))
	assert.Equal(t, run.SpanContext(), ended[0].SpanContext())
	assert.Equal(t, run.SpanContext(), trace.SpanContextFromContext(ctx), "the context carries the run span")
}

// Global tracer provider: not parallel (see installSpanRecorder).
func TestStartJobItem(t *testing.T) {
	recorder := installSpanRecorder(t)

	runCtx, run := telemetry.StartJobRun(context.Background(), "concert-discovery")
	_, item := telemetry.StartJobItem(runCtx, "concert_search.artist", attribute.String("artist.id", "a1"))
	item.End()
	run.End()

	var got sdktrace.ReadOnlySpan
	for _, s := range recorder.Ended() {
		if s.Name() == "concert_search.artist" {
			got = s
		}
	}
	require.NotNil(t, got)
	assert.NotEqual(t, run.SpanContext().TraceID(), got.SpanContext().TraceID(), "the item starts a new trace")
	assert.False(t, got.Parent().IsValid(), "the item span is a root")
	require.Len(t, got.Links(), 1)
	assert.Equal(t, run.SpanContext().TraceID(), got.Links()[0].SpanContext.TraceID())
	assert.Equal(t, run.SpanContext().SpanID(), got.Links()[0].SpanContext.SpanID())
	assert.Contains(t, got.Attributes(), attribute.String("artist.id", "a1"))
}
