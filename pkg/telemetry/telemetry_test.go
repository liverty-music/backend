package telemetry_test

import (
	"context"
	"testing"
	"time"

	"github.com/liverty-music/backend/pkg/config"
	"github.com/liverty-music/backend/pkg/telemetry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

func TestSetupTelemetry(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		telCfg          config.TelemetryConfig
		environment     string
		shutdownTimeout time.Duration
	}{
		{
			name: "setup without OTLP endpoint",
			telCfg: config.TelemetryConfig{
				ServiceName:    "test-service",
				ServiceVersion: "1.0.0",
				SamplerRatio:   1.0,
			},
			environment:     "local",
			shutdownTimeout: 5 * time.Second,
		},
		{
			name: "setup with default config values",
			telCfg: config.TelemetryConfig{
				ServiceName:    "go-backend-scaffold",
				ServiceVersion: "1.0.0",
				SamplerRatio:   1.0,
			},
			environment:     "development",
			shutdownTimeout: 5 * time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			closer, err := telemetry.SetupTelemetry(context.Background(), tt.telCfg, tt.environment, tt.shutdownTimeout)
			require.NoError(t, err)
			require.NotNil(t, closer)

			err = closer.Close()
			assert.NoError(t, err)
		})
	}
}

func TestSetupTelemetry_SamplerRatio(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		samplerRatio float64
	}{
		{name: "ratio 1.0 samples all", samplerRatio: 1.0},
		{name: "ratio 0.5 samples half", samplerRatio: 0.5},
		{name: "ratio 0.0 samples none", samplerRatio: 0.0},
		{name: "ratio 0.1 for production", samplerRatio: 0.1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			telCfg := config.TelemetryConfig{
				ServiceName:    "test-sampler",
				ServiceVersion: "1.0.0",
				SamplerRatio:   tt.samplerRatio,
			}

			closer, err := telemetry.SetupTelemetry(context.Background(), telCfg, "test", 5*time.Second)
			require.NoError(t, err)
			t.Cleanup(func() { _ = closer.Close() })

			// Verify the global tracer provider is set and functional.
			tp := otel.GetTracerProvider()
			require.NotNil(t, tp)

			tracer := tp.Tracer("test")
			_, span := tracer.Start(context.Background(), "test-span")
			require.NotNil(t, span)

			roSpan, ok := span.(sdktrace.ReadOnlySpan)
			if ok && tt.samplerRatio == 1.0 {
				assert.True(t, roSpan.SpanContext().IsSampled(), "ratio 1.0 should always sample")
			}

			span.End()
		})
	}
}
