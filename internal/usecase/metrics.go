package usecase

import "context"

// ConcertMetrics records observability signals for concert search operations.
type ConcertMetrics interface {
	RecordConcertSearch(ctx context.Context, status string)
}

// FollowMetrics records observability signals for follow/unfollow operations.
type FollowMetrics interface {
	RecordFollow(ctx context.Context, action string)
}

// PushMetrics records observability signals for push notification send operations.
type PushMetrics interface {
	RecordPushSend(ctx context.Context, status string)
}

// AnalyticsConsumerMetrics records observability signals for the
// analytics-consumer worker that forwards backend domain events to
// the product-analytics destination (PostHog Cloud EU).
//
// Outcome labels (`status`) are kept low-cardinality so the metrics
// behave well as Prometheus exporters; expected values are listed on
// RecordMessage.
type AnalyticsConsumerMetrics interface {
	// RecordMessage increments the per-event counter. status is one of:
	// "forwarded" (Enqueue accepted), "skipped_nil_client",
	// "skipped_empty_user_id", "skipped_parse_error", "enqueue_error".
	RecordMessage(ctx context.Context, status string)
	// RecordLag records the time between the CloudEvent's publish
	// timestamp and the moment the consumer finishes processing.
	RecordLag(ctx context.Context, seconds float64)
}
