// Package telemetry provides OpenTelemetry tracing setup and configuration.
package telemetry

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/pannpers/go-backend-scaffold/pkg/config"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// SetupTelemetry initializes OpenTelemetry tracing and returns a closer for shutdown.
// If telemetry OTLP endpoint is not configured, tracer is initialized without exporter
// to disable sending trace info to OTEL collector.
func SetupTelemetry(ctx context.Context, cfg *config.Config) (io.Closer, error) {
	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceNameKey.String(cfg.Telemetry.ServiceName),
			semconv.ServiceVersionKey.String(cfg.Telemetry.ServiceVersion),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create telemetry resource: %w", err)
	}

	tracerProviderOpts := []trace.TracerProviderOption{
		trace.WithResource(res),
		trace.WithSampler(trace.AlwaysSample()),
	}

	// disable to export traces to OTEL collector for local development
	if cfg.Telemetry.OTLPEndpoint != "" {
		exporter, err := otlptracehttp.New(ctx,
			otlptracehttp.WithEndpoint(cfg.Telemetry.OTLPEndpoint),
		)
		if err != nil {
			return nil, fmt.Errorf("failed to create OTLP exporter: %w", err)
		}

		tracerProviderOpts = append(tracerProviderOpts, trace.WithBatcher(exporter))
	}

	tracerProvider := trace.NewTracerProvider(tracerProviderOpts...)

	// Set the global tracer provider
	otel.SetTracerProvider(tracerProvider)

	return &tracerCloser{provider: tracerProvider, shutdownTimeout: cfg.ShutdownTimeout}, nil
}

// tracerCloser implements io.Closer for shutting down the tracer provider
type tracerCloser struct {
	provider        *trace.TracerProvider
	shutdownTimeout time.Duration
}

// Close shuts down the tracer provider and flushes any remaining spans
func (tc *tracerCloser) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), tc.shutdownTimeout)
	defer cancel()

	if err := tc.provider.Shutdown(ctx); err != nil {
		return fmt.Errorf("failed to shutdown tracer provider: %w", err)
	}

	return nil
}
