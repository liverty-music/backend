package workflow_test

import (
	"context"
	"testing"

	"github.com/liverty-music/backend/internal/infrastructure/workflow"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// A run continues the trace of the caller that started it (design "Trace
// context").
func TestRuntime_StepSpanContinuesTheStartersTrace(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	previousProvider, previousPropagator := otel.GetTracerProvider(), otel.GetTextMapPropagator()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder)))
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() {
		otel.SetTracerProvider(previousProvider)
		otel.SetTextMapPropagator(previousPropagator)
	})

	dsn := setupSystemDB(t)
	traced := func(ctx context.Context, f usecase.Flow, _ string) (string, error) {
		return usecase.Step(ctx, f, "traced", func(context.Context) (string, error) { return "ok", nil })
	}
	startWorker(t, dsn, func(rt *workflow.Runtime) { workflow.Register(rt, "traced", traced) })
	client := newClient(t, dsn)

	ctx, caller := otel.Tracer("test").Start(context.Background(), "caller")
	require.NoError(t, client.Start(ctx, "traced", "traced-run", ""))
	caller.End()
	var got string
	require.NoError(t, client.Await(context.Background(), "traced-run", waitLimit, &got))

	var step sdktrace.ReadOnlySpan
	for _, s := range recorder.Ended() {
		if s.Name() == "step traced" {
			step = s
		}
	}
	require.NotNil(t, step, "the step has a span")
	assert.Equal(t, caller.SpanContext().TraceID(), step.SpanContext().TraceID(), "the step span is in the starter's trace")
}
