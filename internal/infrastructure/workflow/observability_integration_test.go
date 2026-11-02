package workflow_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/infrastructure/workflow"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
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

// recordParentBased records the spans of a tracer provider whose sampler
// follows the parent, as pkg/telemetry configures it, until the test ends.
func recordParentBased(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	recorder := tracetest.NewSpanRecorder()
	previousProvider, previousPropagator := otel.GetTracerProvider(), otel.GetTextMapPropagator()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.AlwaysSample())),
		sdktrace.WithSpanProcessor(recorder),
	))
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() {
		otel.SetTracerProvider(previousProvider)
		otel.SetTextMapPropagator(previousPropagator)
	})
	return recorder
}

// A scheduled run records no span, and a workflow it starts is a trace of
// its own (design "Traces: a schedule run that finds no sale records nothing").
func TestRuntime_ScheduledRunTraces(t *testing.T) {
	recorder := recordParentBased(t)
	dsn := setupSystemDB(t)

	var due atomic.Bool
	ran := make(chan string, 4)
	drawn := make(chan struct{}, 1)
	scheduled := func(ctx context.Context, f usecase.Flow, at time.Time) (int, error) {
		found, err := usecase.Step(ctx, f, "list due sales", func(context.Context) (bool, error) { return due.Load(), nil })
		if err != nil {
			return 0, err
		}
		if found {
			if err := f.Start(ctx, "draw", "draw:"+at.Format(time.RFC3339Nano), "sale"); err != nil {
				return 0, err
			}
		}
		ran <- trace.SpanContextFromContext(ctx).TraceID().String()
		return 0, nil
	}
	draw := func(ctx context.Context, f usecase.Flow, _ string) (string, error) {
		defer func() { drawn <- struct{}{} }()
		return usecase.Step(ctx, f, "persist outcome", func(context.Context) (string, error) { return "ok", nil })
	}
	startWorker(t, dsn, func(rt *workflow.Runtime) {
		// A yearly schedule, so only the triggered runs happen in the test.
		workflow.RegisterSchedule(rt, "draw-due-sales", "0 0 0 1 1 *", scheduled)
		workflow.Register(rt, "draw", draw)
	})
	client := newClient(t, dsn)

	t.Run("scheduled run that finds no due sale", func(t *testing.T) {
		// @spec components/infrastructure/backend/process/trace-propagation "Scheduled run that finds no due sale"
		recorder.Reset()
		_, err := workflow.TriggerSchedule(client, "draw-due-sales")
		require.NoError(t, err)

		var traceID string
		select {
		case traceID = <-ran:
		case <-time.After(waitLimit):
			t.Fatal("timed out waiting for the scheduled run")
		}
		assert.NotEqual(t, trace.TraceID{}.String(), traceID, "the run's log lines carry a trace id")
		assert.Empty(t, recorder.Ended(), "the run records no span")
	})

	t.Run("scheduled run that finds a due sale", func(t *testing.T) {
		// @spec components/infrastructure/backend/process/trace-propagation "Scheduled run that finds a due sale"
		recorder.Reset()
		due.Store(true)
		_, err := workflow.TriggerSchedule(client, "draw-due-sales")
		require.NoError(t, err)
		var runTrace string
		select {
		case runTrace = <-ran:
		case <-time.After(waitLimit):
			t.Fatal("timed out waiting for the scheduled run")
		}
		waitFor(t, drawn, "the draw")
		// The draw's run span ends when its function returns, after the step.
		require.Eventually(t, func() bool {
			for _, s := range recorder.Ended() {
				if s.Name() == "workflow draw" {
					return true
				}
			}
			return false
		}, waitLimit, 10*time.Millisecond)

		var run, step sdktrace.ReadOnlySpan
		for _, s := range recorder.Ended() {
			assert.NotEqual(t, runTrace, s.SpanContext().TraceID().String(), "no span of the scheduled run is recorded: %s", s.Name())
			switch s.Name() {
			case "workflow draw":
				run = s
			case "step persist outcome":
				step = s
			}
		}
		require.NotNil(t, run)
		assert.False(t, run.Parent().IsValid(), "the draw is a new root")
		require.NotNil(t, step)
		assert.Equal(t, run.SpanContext().TraceID(), step.SpanContext().TraceID(), "the draw's steps are in its trace")
	})
}
