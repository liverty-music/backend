package usecase

import (
	"context"
	"log/slog"
	"time"

	"github.com/pannpers/go-logging/logging"
)

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
// analytics-consumer adapter, which subscribes to existing domain event
// subjects. Connect-RPC handlers MUST NOT call AnalyticsClient directly.
//
// Lifecycle (Close, flush) is intentionally NOT part of this interface.
// Implementations expose an io.Closer-compatible Close method on the
// concrete type so the DI layer can register them with the shutdown
// manager via shutdown.AddExternalPhase, matching the pattern used by
// other outbound clients (lastfm, musicbrainz, fanarttv, db).
type AnalyticsClient interface {
	// Enqueue hands an event off for asynchronous delivery to the
	// analytics destination.
	//
	// distinctID is the platform-internal UserId UUID — never the
	// Zitadel sub claim and never an empty string. Implementations MUST
	// reject empty distinctID and SHOULD validate UUID format so that
	// callers cannot accidentally pass an opaque IdP subject identifier.
	//
	// eventName MUST be one of the AnalyticsEventName constants defined
	// in analytics_events.go. Implementations MUST reject unknown event
	// names (use IsKnownEvent for membership checks) so a typo at a
	// call site fails fast instead of silently fragmenting dashboards.
	//
	// ctx is read by implementations to extract the active OpenTelemetry
	// trace ID and inject it into the event payload as the `trace_id`
	// property, providing a one-click bridge from the analytics event to
	// the originating request trace during incident investigation. ctx
	// cancellation is NOT propagated to the underlying SDK because the
	// posthog-go Client interface does not expose a context-aware Enqueue.
	//
	// properties is the per-event payload, optional, sanitised per the
	// PII policy before this call. Implementations MUST NOT mutate the
	// supplied map; trace_id injection, when applied, happens on a
	// defensive copy.
	//
	// Enqueue returns an apperr-coded error for caller-side mistakes
	// (codes.InvalidArgument: empty/non-UUID distinctID, empty/unknown
	// eventName) and for the rare SDK queue-overflow case
	// (codes.Internal). Transient destination failures are retried
	// internally by the implementation and never surfaced here.
	Enqueue(ctx context.Context, distinctID string, eventName AnalyticsEventName, properties AnalyticsProperties) error

	// EnqueueOnce is Enqueue for an event that may be handed off more than
	// once, such as one forwarded from a redelivered domain event. eventID
	// (a UUID, e.g. the Order id) becomes the destination's event id and at
	// its timestamp, so the destination stores the event once however often
	// it is handed off. A zero at means now.
	//
	// It returns codes.InvalidArgument for a non-UUID eventID, in addition to
	// the errors of Enqueue.
	EnqueueOnce(ctx context.Context, distinctID, eventID string, at time.Time, eventName AnalyticsEventName, properties AnalyticsProperties) error

	// EnqueueSystem hands off an event that has no fan: a signal of a
	// pipeline process (a search outcome, an external model call). The
	// process the implementation runs as (e.g. the telemetry service name
	// "liverty-music-concert-discovery") is the distinct id, no person
	// profile is created for it, and the process and the backend's release
	// version are added to the properties as `process` and `app_version`.
	//
	// It returns the errors of Enqueue for eventName.
	EnqueueSystem(ctx context.Context, eventName AnalyticsEventName, properties AnalyticsProperties) error
}

// sendSystemSignal sends a pipeline signal through client, which may be nil
// (no PostHog key configured: nothing is sent). A failure is logged and never
// returned, so a signal can never change the outcome it reports.
func sendSystemSignal(ctx context.Context, client AnalyticsClient, logger *logging.Logger, eventName AnalyticsEventName, properties AnalyticsProperties) {
	if client == nil {
		return
	}
	if err := client.EnqueueSystem(ctx, eventName, properties); err != nil {
		logger.Warn(ctx, "failed to send a product-analytics signal",
			slog.String("event", string(eventName)),
			slog.Any("error", err),
		)
	}
}
