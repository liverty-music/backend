// Package telemetry provides OTel-based implementations of metrics interfaces.
package telemetry

import (
	"context"

	"github.com/liverty-music/backend/internal/usecase"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// Compile-time interface compliance check.
var _ usecase.MintMetrics = (*OTelMintMetrics)(nil)

// OTelMintMetrics implements usecase.MintMetrics using the OTel Metrics API.
type OTelMintMetrics struct {
	duration metric.Float64Histogram
	total    metric.Int64Counter
}

// NewOTelMintMetrics creates a new OTelMintMetrics with registered instruments.
func NewOTelMintMetrics() *OTelMintMetrics {
	meter := otel.Meter("usecase/ticket")
	duration, _ := meter.Float64Histogram("blockchain.mint.duration",
		metric.WithUnit("s"),
		metric.WithDescription("Duration of ticket mint operations including retries"),
	)
	total, _ := meter.Int64Counter("blockchain.mint.total",
		metric.WithDescription("Total ticket mint operations by outcome"),
	)
	return &OTelMintMetrics{duration: duration, total: total}
}

// RecordDuration records the mint operation duration with the given outcome.
func (m *OTelMintMetrics) RecordDuration(ctx context.Context, seconds float64, outcome string) {
	m.duration.Record(ctx, seconds, metric.WithAttributes(attribute.String("outcome", outcome)))
}

// RecordTotal increments the mint operation counter with the given outcome.
func (m *OTelMintMetrics) RecordTotal(ctx context.Context, outcome string) {
	m.total.Add(ctx, 1, metric.WithAttributes(attribute.String("outcome", outcome)))
}
