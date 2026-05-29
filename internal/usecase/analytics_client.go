package usecase

import "context"

// AnalyticsProperties carries the per-event payload sent to the analytics
// destination. Keys MUST be snake_case and MUST NOT include personally
// identifiable information; see specification/docs/analytics/event-catalog.md
// for the per-event property contract and the PII three-layer classification.
type AnalyticsProperties map[string]any

// AnalyticsClient delivers product-analytics events to the configured
// analytics destination (PostHog Cloud EU in production).
//
// Implementations MUST NOT block the caller on network availability of the
// destination. Enqueue is expected to hand the event off to an internal
// buffer or queue and return immediately so that Connect-RPC handlers
// retain their latency contract even when the analytics destination is
// degraded.
//
// Trust-critical events (ticket purchases, ZK proof verification, push
// delivery, account state changes) flow through AnalyticsClient from the
// analytics-consumer adapter, which subscribes to existing NATS event
// subjects. Connect-RPC handlers MUST NOT call AnalyticsClient directly.
type AnalyticsClient interface {
	// Enqueue hands an event off for asynchronous delivery to the
	// analytics destination.
	//
	// distinctID is the platform-internal UserId UUID — never the
	// Zitadel sub claim and never an empty string. Implementations MUST
	// reject empty distinctID and MUST NOT substitute a fallback identifier
	// that could correlate to a real user across sessions.
	//
	// eventName MUST be one of the AnalyticsEventName constants defined
	// in analytics_events.go.
	//
	// properties is the per-event payload, optional, sanitised per the
	// PII policy before this call.
	//
	// Enqueue returns an error only for caller-side mistakes (empty
	// distinctID, unknown eventName). Transient destination failures are
	// retried internally by the implementation and never surfaced here.
	Enqueue(ctx context.Context, distinctID string, eventName AnalyticsEventName, properties AnalyticsProperties) error

	// Close flushes any in-flight events and releases resources. It is
	// invoked at process shutdown. Close MUST be safe to call after a
	// failed initialisation and MUST be idempotent.
	Close(ctx context.Context) error
}
