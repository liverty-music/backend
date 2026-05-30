package telemetry

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/liverty-music/backend/internal/usecase"
)

// Compile-time interface compliance check.
var _ usecase.AnalyticsConsumerMetrics = (*OTelAnalyticsConsumerMetrics)(nil)

// OTelAnalyticsConsumerMetrics implements
// usecase.AnalyticsConsumerMetrics using the OTel Metrics API. The
// exported metric names map to the Prometheus scrape targets:
//
//   - analytics_consumer_messages_total — counter labelled by status:
//     "forwarded", "skipped_nil_client", "skipped_empty_user_id",
//     "skipped_parse_error", "enqueue_error".
//   - analytics_consumer_lag_seconds — histogram of per-message
//     processing lag in seconds, measured from CloudEvent publish
//     time to the consumer's Handle return.
//
// The "errors_total" task entry in tasks.md is satisfied by filtering
// `analytics_consumer_messages_total` on the error-bucket statuses
// (`skipped_parse_error`, `enqueue_error`) — a single counter with a
// low-cardinality status label is preferred over two parallel
// counters because Prometheus dashboards and alerts can pick the
// slice they need with `sum by (status)`.
type OTelAnalyticsConsumerMetrics struct {
	messages metric.Int64Counter
	lag      metric.Float64Histogram
}

// NewOTelAnalyticsConsumerMetrics creates and registers the
// analytics-consumer OTel instruments.
func NewOTelAnalyticsConsumerMetrics() *OTelAnalyticsConsumerMetrics {
	meter := otel.Meter("adapter/event/analytics_consumer")
	messages, _ := meter.Int64Counter("analytics_consumer.messages.count",
		metric.WithDescription("Analytics-consumer messages processed, partitioned by outcome status"),
	)
	lag, _ := meter.Float64Histogram("analytics_consumer.lag.seconds",
		metric.WithUnit("s"),
		metric.WithDescription("Per-message lag from CloudEvent publish time to consumer Handle return"),
	)
	return &OTelAnalyticsConsumerMetrics{messages: messages, lag: lag}
}

// RecordMessage implements usecase.AnalyticsConsumerMetrics.
func (m *OTelAnalyticsConsumerMetrics) RecordMessage(ctx context.Context, status string) {
	m.messages.Add(ctx, 1, metric.WithAttributes(attribute.String("status", status)))
}

// RecordLag implements usecase.AnalyticsConsumerMetrics.
func (m *OTelAnalyticsConsumerMetrics) RecordLag(ctx context.Context, seconds float64) {
	m.lag.Record(ctx, seconds)
}
