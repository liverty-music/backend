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
var _ usecase.OrganizerMetrics = (*BusinessMetrics)(nil)

// BusinessMetrics provides OTel counters for key business operations.
// It is injected into use cases that need to record business-level metrics.
type BusinessMetrics struct {
	concertSearch        metric.Int64Counter
	follow               metric.Int64Counter
	pushSend             metric.Int64Counter
	deliveryOutcome      metric.Int64Counter
	organizerProvisioned metric.Int64Counter
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
	deliveryOutcome, _ := meter.Int64Counter("notification.delivery.count",
		metric.WithDescription("Per-notification delivery outcomes by outcome and failure reason"),
	)
	organizerProvisioned, _ := meter.Int64Counter("organizer.provisioning.count",
		metric.WithDescription("Organizer tenant provisioning outcomes by status (success/failed)"),
	)
	return &BusinessMetrics{
		concertSearch:        concertSearch,
		follow:               follow,
		pushSend:             pushSend,
		deliveryOutcome:      deliveryOutcome,
		organizerProvisioned: organizerProvisioned,
	}
}

// RecordConcertSearch increments the concert.search.count counter, tagging
// the run outcome via the status attribute. Accepted values are "success"
// (the run discovered at least one new concert), "zero_results" (the run
// completed without error but found no new concerts), and "error" (the run
// failed). The zero_results outcome distinguishes a fruitless-but-healthy
// run — quota burned, nothing found — from a fruitful one.
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

// RecordDeliveryOutcome increments the per-notification delivery-outcome counter,
// tagged by the terminal outcome and a low-cardinality failure-reason category.
func (m *BusinessMetrics) RecordDeliveryOutcome(ctx context.Context, outcome, failureReason string) {
	m.deliveryOutcome.Add(ctx, 1, metric.WithAttributes(
		attribute.String("outcome", outcome),
		attribute.String("failure_reason", failureReason),
	))
}

// RecordOrganizerProvisioning increments the organizer.provisioning.count
// counter, tagging the outcome via the status attribute ("success" / "failed").
func (m *BusinessMetrics) RecordOrganizerProvisioning(ctx context.Context, status string) {
	m.organizerProvisioned.Add(ctx, 1, metric.WithAttributes(attribute.String("status", status)))
}
