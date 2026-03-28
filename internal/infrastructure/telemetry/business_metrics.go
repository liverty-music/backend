package telemetry

import (
	"context"

	"github.com/liverty-music/backend/internal/usecase"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// Compile-time interface compliance checks.
var _ usecase.ConcertMetrics = (*BusinessMetrics)(nil)
var _ usecase.FollowMetrics = (*BusinessMetrics)(nil)
var _ usecase.PushMetrics = (*BusinessMetrics)(nil)

// BusinessMetrics provides OTel counters for key business operations.
// It is injected into use cases that need to record business-level metrics.
type BusinessMetrics struct {
	concertSearch metric.Int64Counter
	follow        metric.Int64Counter
	pushSend      metric.Int64Counter
}

// NewBusinessMetrics creates a new BusinessMetrics with registered OTel instruments.
func NewBusinessMetrics() *BusinessMetrics {
	meter := otel.Meter("liverty-music/backend/business")
	concertSearch, _ := meter.Int64Counter("concert.search.count",
		metric.WithDescription("Concert search operations by status"),
	)
	follow, _ := meter.Int64Counter("follow.count",
		metric.WithDescription("Follow/unfollow operations by action"),
	)
	pushSend, _ := meter.Int64Counter("push_notification.send.count",
		metric.WithDescription("Push notification send operations by status"),
	)
	return &BusinessMetrics{
		concertSearch: concertSearch,
		follow:        follow,
		pushSend:      pushSend,
	}
}

// RecordConcertSearch increments the concert search counter.
func (m *BusinessMetrics) RecordConcertSearch(ctx context.Context, status string) {
	m.concertSearch.Add(ctx, 1, metric.WithAttributes(attribute.String("status", status)))
}

// RecordFollow increments the follow counter.
func (m *BusinessMetrics) RecordFollow(ctx context.Context, action string) {
	m.follow.Add(ctx, 1, metric.WithAttributes(attribute.String("action", action)))
}

// RecordPushSend increments the push notification send counter.
func (m *BusinessMetrics) RecordPushSend(ctx context.Context, status string) {
	m.pushSend.Add(ctx, 1, metric.WithAttributes(attribute.String("status", status)))
}
