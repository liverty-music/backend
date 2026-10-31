package messaging_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/ThreeDotsLabs/watermill/pubsub/gochannel"
	"github.com/liverty-music/backend/internal/infrastructure/messaging"
	"github.com/liverty-music/backend/pkg/config"
	"github.com/liverty-music/backend/pkg/telemetry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

// installSpanRecorder makes a fresh in-memory span recorder and the W3C
// propagator the global OpenTelemetry setup. The publisher decorator and the
// router middleware read the globals, so tests that call it must not run in
// parallel. The previous globals are not restored: the next test that needs
// them installs its own, and the parallel tests of this package do not look
// at spans.
func installSpanRecorder(t *testing.T) (*tracetest.SpanRecorder, trace.Tracer) {
	t.Helper()
	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	return recorder, tp.Tracer("test")
}

// producerSpan returns the ended producer span of traceID.
func producerSpan(t *testing.T, recorder *tracetest.SpanRecorder, traceID trace.TraceID) sdktrace.ReadOnlySpan {
	t.Helper()
	for _, s := range recorder.Ended() {
		if s.SpanKind() == trace.SpanKindProducer && s.SpanContext().TraceID() == traceID {
			return s
		}
	}
	t.Fatalf("no producer span in trace %s", traceID)
	return nil
}

// handledSpan is the consumer span a handler ran in.
type handledSpan struct {
	spanContext trace.SpanContext
	parent      trace.SpanContext
}

func spanOf(ctx context.Context) handledSpan {
	span := trace.SpanFromContext(ctx)
	h := handledSpan{spanContext: span.SpanContext()}
	if ro, ok := span.(sdktrace.ReadOnlySpan); ok {
		h.parent = ro.Parent()
	}
	return h
}

// spanCollector records the consumer span of every handler invocation.
type spanCollector struct {
	mu    sync.Mutex
	spans []handledSpan
	ch    chan struct{}
}

func newSpanCollector() *spanCollector {
	return &spanCollector{ch: make(chan struct{}, 16)}
}

func (c *spanCollector) record(ctx context.Context) {
	c.mu.Lock()
	c.spans = append(c.spans, spanOf(ctx))
	c.mu.Unlock()
	c.ch <- struct{}{}
}

// wait blocks until n invocations were recorded and returns them.
func (c *spanCollector) wait(t *testing.T, n int) []handledSpan {
	t.Helper()
	for range n {
		select {
		case <-c.ch:
		case <-time.After(5 * time.Second):
			t.Fatalf("handler was not invoked %d times", n)
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]handledSpan(nil), c.spans...)
}

// newTestPubSub returns a GoChannel broker and the traced publisher in front
// of it, as NewPublisher builds it for local development.
func newTestPubSub(t *testing.T) (*gochannel.GoChannel, message.Publisher) {
	t.Helper()
	logger := watermill.NopLogger{}
	ch := gochannel.NewGoChannel(gochannel.Config{OutputChannelBuffer: 8}, logger)
	pub, err := messaging.NewPublisher(config.NATSConfig{}, logger, ch, nil)
	require.NoError(t, err)
	return ch, pub
}

// runRouter starts a router built by NewRouter after register adds its
// handlers, and stops it when the test ends.
func runRouter(t *testing.T, register func(r *message.Router)) {
	t.Helper()
	router, err := messaging.NewRouter(watermill.NopLogger{}, &recordingPublisher{}, "POISON.queue")
	require.NoError(t, err)
	register(router)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		_ = router.Close()
	})
	go func() { _ = router.Run(ctx) }()
	<-router.Running()
}

// Global tracer provider: not parallel (see installSpanRecorder).
func TestNewPublisher_InjectsProducerSpanAsParent(t *testing.T) {
	recorder, tracer := installSpanRecorder(t)
	ch, pub := newTestPubSub(t)
	received, err := ch.Subscribe(context.Background(), "TEST.subject")
	require.NoError(t, err)

	ctx, caller := tracer.Start(context.Background(), "caller")
	require.NoError(t, messaging.NewEventPublisher(pub).PublishEvent(ctx, "TEST.subject", map[string]string{"k": "v"}))
	caller.End()

	var msg *message.Message
	select {
	case msg = <-received:
		msg.Ack()
	case <-time.After(5 * time.Second):
		t.Fatal("message was not delivered")
	}

	parts := strings.Split(msg.Metadata.Get("traceparent"), "-")
	require.Len(t, parts, 4, "traceparent metadata: %q", msg.Metadata.Get("traceparent"))
	producer := producerSpan(t, recorder, caller.SpanContext().TraceID())
	assert.Equal(t, caller.SpanContext().TraceID().String(), parts[1])
	assert.Equal(t, producer.SpanContext().SpanID().String(), parts[2])
	assert.NotEqual(t, caller.SpanContext().SpanID().String(), parts[2])
	assert.Equal(t, "gochannel.GoChannel", producer.Name(), "the send span keeps the broker's name")
	assert.Equal(t, "1.0", msg.Metadata.Get("ce_specversion"), "CloudEvents attributes stay as they are")
}

// Global tracer provider: not parallel (see installSpanRecorder).
func TestRouter_TracePropagation(t *testing.T) {
	// @spec components/infrastructure/backend/process/trace-propagation "Consumer continues the publisher's trace"
	t.Run("Consumer continues the publisher's trace", func(t *testing.T) {
		recorder, tracer := installSpanRecorder(t)
		ch, pub := newTestPubSub(t)
		got := newSpanCollector()
		runRouter(t, func(r *message.Router) {
			r.AddConsumerHandler("store-concerts", "CONCERT.discovered", ch, func(msg *message.Message) error {
				got.record(msg.Context())
				return nil
			})
		})

		ctx, search := tracer.Start(context.Background(), "concert_search.artist")
		require.NoError(t, messaging.NewEventPublisher(pub).PublishEvent(ctx, "CONCERT.discovered", map[string]string{}))
		search.End()

		spans := got.wait(t, 1)
		producer := producerSpan(t, recorder, search.SpanContext().TraceID())
		assert.Equal(t, search.SpanContext().TraceID(), spans[0].spanContext.TraceID())
		assert.Equal(t, producer.SpanContext().SpanID(), spans[0].parent.SpanID())
		assert.True(t, spans[0].parent.IsRemote())
	})

	// @spec components/infrastructure/backend/process/trace-propagation "Two hops"
	t.Run("Two hops", func(t *testing.T) {
		_, tracer := installSpanRecorder(t)
		ch, pub := newTestPubSub(t)
		events := messaging.NewEventPublisher(pub)
		first, second := newSpanCollector(), newSpanCollector()
		runRouter(t, func(r *message.Router) {
			r.AddConsumerHandler("first", "TEST.first", ch, func(msg *message.Message) error {
				first.record(msg.Context())
				return events.PublishEvent(msg.Context(), "TEST.second", map[string]string{})
			})
			r.AddConsumerHandler("second", "TEST.second", ch, func(msg *message.Message) error {
				second.record(msg.Context())
				return nil
			})
		})

		ctx, origin := tracer.Start(context.Background(), "origin")
		require.NoError(t, events.PublishEvent(ctx, "TEST.first", map[string]string{}))
		origin.End()

		hop1 := first.wait(t, 1)[0]
		hop2 := second.wait(t, 1)[0]
		assert.Equal(t, origin.SpanContext().TraceID(), hop1.spanContext.TraceID())
		assert.Equal(t, origin.SpanContext().TraceID(), hop2.spanContext.TraceID())
		assert.NotEqual(t, hop1.spanContext.SpanID(), hop2.parent.SpanID(),
			"the second consumer hangs off the second producer span, not the first consumer span")
	})

	// @spec components/infrastructure/backend/process/trace-propagation "Job that publishes events"
	t.Run("Job that publishes events", func(t *testing.T) {
		installSpanRecorder(t)
		ch, pub := newTestPubSub(t)
		got := newSpanCollector()
		runRouter(t, func(r *message.Router) {
			r.AddConsumerHandler("deliver-reminder", "SALES_PHASE.discovered", ch, func(msg *message.Message) error {
				got.record(msg.Context())
				return nil
			})
		})

		runCtx, run := telemetry.StartJobRun(context.Background(), "sales-reminders")
		require.NoError(t, messaging.NewEventPublisher(pub).PublishEvent(runCtx, "SALES_PHASE.discovered", map[string]string{}))
		run.End()

		span := got.wait(t, 1)[0]
		assert.Equal(t, run.SpanContext().TraceID(), span.spanContext.TraceID())
	})

	// @spec components/infrastructure/backend/process/trace-propagation "Event without trace context"
	t.Run("Event without trace context", func(t *testing.T) {
		installSpanRecorder(t)
		ch, _ := newTestPubSub(t)
		got := newSpanCollector()
		runRouter(t, func(r *message.Router) {
			r.AddConsumerHandler("legacy", "TEST.legacy", ch, func(msg *message.Message) error {
				got.record(msg.Context())
				return nil
			})
		})

		// Published straight to the broker, as a message from before the rollout.
		require.NoError(t, ch.Publish("TEST.legacy", message.NewMessage("m", []byte("{}"))))

		span := got.wait(t, 1)[0]
		assert.True(t, span.spanContext.IsValid(), "the consumer records the event in a new trace")
		assert.False(t, span.parent.IsValid(), "the consumer span is a root")
	})

	// @spec components/infrastructure/backend/process/trace-propagation "Redelivered event"
	t.Run("Redelivered event", func(t *testing.T) {
		tests := []struct {
			name string
			fail func(msg *message.Message) error
		}{
			{
				name: "in-process retry after a failed attempt",
				fail: func(*message.Message) error { return errors.New("transient") },
			},
			{
				name: "broker redelivery after a nack",
				fail: func(msg *message.Message) error {
					return messaging.RedeliverLater(msg, errors.New("not yet"), time.Millisecond)
				},
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				recorder, tracer := installSpanRecorder(t)
				ch, pub := newTestPubSub(t)
				got := newSpanCollector()
				var calls atomic.Int32
				runRouter(t, func(r *message.Router) {
					r.AddConsumerHandler("flaky", "TEST.flaky", ch, func(msg *message.Message) error {
						got.record(msg.Context())
						if calls.Add(1) == 1 {
							return tt.fail(msg)
						}
						return nil
					})
				})

				ctx, origin := tracer.Start(context.Background(), "origin")
				require.NoError(t, messaging.NewEventPublisher(pub).PublishEvent(ctx, "TEST.flaky", map[string]string{}))
				origin.End()

				attempts := got.wait(t, 2)
				producer := producerSpan(t, recorder, origin.SpanContext().TraceID())
				for i, a := range attempts {
					assert.Equal(t, origin.SpanContext().TraceID(), a.spanContext.TraceID(), "attempt %d", i+1)
					assert.Equal(t, producer.SpanContext().SpanID(), a.parent.SpanID(), "attempt %d", i+1)
				}
				assert.NotEqual(t, attempts[0].spanContext.SpanID(), attempts[1].spanContext.SpanID())
			})
		}
	})
}
