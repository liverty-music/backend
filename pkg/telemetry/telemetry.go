// Package telemetry provides OpenTelemetry tracing and metrics setup.
package telemetry

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/liverty-music/backend/pkg/config"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// SetupTelemetry initializes OpenTelemetry tracing and metrics, then returns
// a closer that shuts down both providers. When the OTLP endpoint is empty,
// providers are created without exporters (local development).
func SetupTelemetry(ctx context.Context, telCfg config.TelemetryConfig, environment string, shutdownTimeout time.Duration) (io.Closer, error) {
	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceNameKey.String(telCfg.ServiceName),
			semconv.ServiceVersionKey.String(telCfg.ServiceVersion),
			semconv.DeploymentEnvironmentKey.String(environment),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("create telemetry resource: %w", err)
	}

	// Propagator — explicit W3C TraceContext.
	otel.SetTextMapPropagator(propagation.TraceContext{})

	// TracerProvider
	tp, err := setupTracerProvider(ctx, telCfg, res)
	if err != nil {
		return nil, err
	}
	otel.SetTracerProvider(tp)

	// MeterProvider
	mp, err := setupMeterProvider(ctx, telCfg, res)
	if err != nil {
		return nil, err
	}
	otel.SetMeterProvider(mp)

	return &telemetryCloser{
		tracerProvider:  tp,
		meterProvider:   mp,
		shutdownTimeout: shutdownTimeout,
	}, nil
}

func setupTracerProvider(ctx context.Context, telCfg config.TelemetryConfig, res *resource.Resource) (*sdktrace.TracerProvider, error) {
	sampler := sdktrace.ParentBased(sdktrace.TraceIDRatioBased(telCfg.SamplerRatio))

	opts := []sdktrace.TracerProviderOption{
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sampler),
	}

	if telCfg.OTLPEndpoint != "" {
		exporter, err := otlptracehttp.New(ctx,
			otlptracehttp.WithEndpoint(telCfg.OTLPEndpoint),
			otlptracehttp.WithInsecure(),
		)
		if err != nil {
			return nil, fmt.Errorf("create OTLP trace exporter: %w", err)
		}
		opts = append(opts, sdktrace.WithBatcher(exporter))
	}

	return sdktrace.NewTracerProvider(opts...), nil
}

func setupMeterProvider(ctx context.Context, telCfg config.TelemetryConfig, res *resource.Resource) (*sdkmetric.MeterProvider, error) {
	opts := []sdkmetric.Option{
		sdkmetric.WithResource(res),
	}

	if telCfg.OTLPEndpoint != "" {
		exporter, err := otlpmetrichttp.New(ctx,
			otlpmetrichttp.WithEndpoint(telCfg.OTLPEndpoint),
			otlpmetrichttp.WithInsecure(),
		)
		if err != nil {
			return nil, fmt.Errorf("create OTLP metric exporter: %w", err)
		}
		opts = append(opts, sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exporter)))
	}

	return sdkmetric.NewMeterProvider(opts...), nil
}

// telemetryCloser shuts down both the TracerProvider and MeterProvider.
type telemetryCloser struct {
	tracerProvider  *sdktrace.TracerProvider
	meterProvider   *sdkmetric.MeterProvider
	shutdownTimeout time.Duration
}

// Close flushes pending telemetry data and shuts down both providers.
func (tc *telemetryCloser) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), tc.shutdownTimeout)
	defer cancel()

	return errors.Join(
		tc.tracerProvider.Shutdown(ctx),
		tc.meterProvider.Shutdown(ctx),
	)
}
