package workflow_test

import (
	"context"
	"net"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/liverty-music/backend/internal/infrastructure/messaging"
	"github.com/liverty-music/backend/internal/infrastructure/workflow"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/liverty-music/backend/pkg/config"
	natsserver "github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// startJetStream starts an embedded NATS server with JetStream and a stream
// on subject, with the 2-minute duplicate window of the production streams,
// and returns its URL and a JetStream context to read the stream.
func startJetStream(t *testing.T, stream, subject string) (string, nats.JetStreamContext) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := ln.Addr().(*net.TCPAddr).Port
	require.NoError(t, ln.Close())

	s, err := natsserver.NewServer(&natsserver.Options{
		Host: "127.0.0.1", Port: port, NoLog: true, NoSigs: true,
		JetStream: true, StoreDir: t.TempDir(),
	})
	require.NoError(t, err)
	s.Start()
	t.Cleanup(s.Shutdown)
	require.True(t, s.ReadyForConnections(10*time.Second))

	url := "nats://127.0.0.1:" + strconv.Itoa(port)
	nc, err := nats.Connect(url)
	require.NoError(t, err)
	t.Cleanup(nc.Close)
	js, err := nc.JetStream()
	require.NoError(t, err)
	_, err = js.AddStream(&nats.StreamConfig{
		Name: stream, Subjects: []string{subject}, Storage: nats.MemoryStorage, Duplicates: 2 * time.Minute,
	})
	require.NoError(t, err)
	return url, js
}

// @spec components/infrastructure/backend/process/durable-workflows "Step runs again after a stop"
func TestRuntime_StepRunsAgainAfterAStop(t *testing.T) {
	dsn := setupSystemDB(t)
	ctx := context.Background()
	url, js := startJetStream(t, "WFTEST", "WFTEST.*")
	logger, _ := newLogger(t)
	pub, err := messaging.NewPublisher(config.NATSConfig{URL: url}, watermill.NopLogger{}, nil, logger)
	require.NoError(t, err)
	t.Cleanup(func() { _ = pub.Close() })
	events := messaging.NewEventPublisher(pub)

	var publishes atomic.Int32
	var stopping atomic.Bool
	stopping.Store(true)
	published := make(chan struct{}, 1)
	announce := func(ctx context.Context, f usecase.Flow, in string) (string, error) {
		return usecase.Step(ctx, f, "announce", func(ctx context.Context) (string, error) {
			if err := events.PublishEvent(ctx, "WFTEST.announced", map[string]string{"id": in}); err != nil {
				return "", err
			}
			publishes.Add(1)
			if stopping.Load() {
				// The worker stops after the publish and before the step is
				// recorded.
				published <- struct{}{}
				<-ctx.Done()
				return "", ctx.Err()
			}
			return "announced", nil
		})
	}
	register := func(rt *workflow.Runtime) { workflow.Register(rt, "announce", announce) }

	old := startWorker(t, dsn, register)
	client := newClient(t, dsn)
	require.NoError(t, client.Start(ctx, "announce", "announce-once", "event-1"))
	waitFor(t, published, "the first publish")
	require.NoError(t, old.Close())

	stopping.Store(false)
	startWorker(t, dsn, register)
	var got string
	require.NoError(t, client.Await(ctx, "announce-once", waitLimit, &got))

	assert.Equal(t, int32(2), publishes.Load(), "the step publishes again")
	info, err := js.StreamInfo("WFTEST")
	require.NoError(t, err)
	assert.Equal(t, uint64(1), info.State.Msgs, "JetStream keeps one message: both publishes had one id")
}

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
