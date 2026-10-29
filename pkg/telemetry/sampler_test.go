package telemetry_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/liverty-music/backend/pkg/config"
	"github.com/liverty-music/backend/pkg/telemetry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

// newRecordedTracerProvider builds the production tracer provider (without an
// exporter) and records every span it samples in memory.
func newRecordedTracerProvider(t *testing.T) (*sdktrace.TracerProvider, *tracetest.SpanRecorder) {
	t.Helper()
	tp, err := telemetry.SetupTracerProvider(context.Background(), config.TelemetryConfig{SamplerRatio: 1.0}, resource.Empty())
	require.NoError(t, err)
	recorder := tracetest.NewSpanRecorder()
	tp.RegisterSpanProcessor(recorder)
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	return tp, recorder
}

func TestTracerProvider_ClientRootIsNotRecorded(t *testing.T) {
	t.Parallel()

	// @spec components/infrastructure/backend/process/trace-propagation "Background check that finds nothing"
	tp, recorder := newRecordedTracerProvider(t)

	ctx, span := tp.Tracer("test").Start(context.Background(), "SELECT", trace.WithSpanKind(trace.SpanKindClient))
	span.End()

	assert.Empty(t, recorder.Ended())
	assert.False(t, span.IsRecording())
	assert.True(t, trace.SpanContextFromContext(ctx).TraceID().IsValid(), "log lines written with ctx keep a trace id")
}

func TestTracerProvider_OtelhttpCallWithoutParentIsNotRecorded(t *testing.T) {
	t.Parallel()

	// @spec components/infrastructure/backend/process/trace-propagation "Database credential refresh"
	tp, recorder := newRecordedTracerProvider(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	client := &http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport, otelhttp.WithTracerProvider(tp))}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, srv.URL, http.NoBody)
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())

	assert.Empty(t, recorder.Ended())
}

func TestTracerProvider_ClientSpanUnderServerSpanIsRecorded(t *testing.T) {
	t.Parallel()

	// @spec components/infrastructure/backend/process/trace-propagation "Same query inside a request"
	tp, recorder := newRecordedTracerProvider(t)
	tracer := tp.Tracer("test")

	ctx, request := tracer.Start(context.Background(), "ListConcerts", trace.WithSpanKind(trace.SpanKindServer))
	_, query := tracer.Start(ctx, "SELECT", trace.WithSpanKind(trace.SpanKindClient))
	query.End()
	request.End()

	ended := recorder.Ended()
	require.Len(t, ended, 2)
	assert.Equal(t, "SELECT", ended[0].Name())
	assert.Equal(t, request.SpanContext().TraceID(), ended[0].SpanContext().TraceID())
	assert.Equal(t, request.SpanContext().SpanID(), ended[0].Parent().SpanID())
	assert.Equal(t, "ListConcerts", ended[1].Name())
}

func TestTracerProvider_NonClientRootIsRecorded(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		kind trace.SpanKind
	}{
		{name: "producer", kind: trace.SpanKindProducer},
		{name: "internal", kind: trace.SpanKindInternal},
		{name: "server", kind: trace.SpanKindServer},
		{name: "consumer", kind: trace.SpanKindConsumer},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tp, recorder := newRecordedTracerProvider(t)

			_, span := tp.Tracer("test").Start(context.Background(), tt.name, trace.WithSpanKind(tt.kind))
			span.End()

			ended := recorder.Ended()
			require.Len(t, ended, 1)
			assert.False(t, ended[0].Parent().IsValid())
			assert.Equal(t, tt.kind, ended[0].SpanKind())
		})
	}
}
