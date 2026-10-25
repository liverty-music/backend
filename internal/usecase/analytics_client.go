package usecase

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-logging/logging"

	"github.com/liverty-music/backend/internal/entity"
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
// destination. Capture is expected to hand the event off to an internal
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
	// Capture hands one event off for asynchronous delivery to the
	// analytics destination. The caller sets every value of the event (see
	// [AnalyticsEvent]); the destination stores events with the same UUID,
	// name, time and distinct id once, so a redelivered domain event handed
	// off again with the same values is recorded once.
	//
	// ctx is read by implementations to extract the active OpenTelemetry
	// trace ID and inject it into the event payload as the `trace_id`
	// property, providing a one-click bridge from the analytics event to
	// the originating request trace during incident investigation. ctx
	// cancellation is NOT propagated to the underlying SDK because the
	// posthog-go Client interface does not expose a context-aware Enqueue.
	//
	// Capture returns an apperr-coded error for caller-side mistakes
	// (codes.InvalidArgument: an empty or unknown Name, an empty or
	// non-UUID UUID, a zero Time, a non-UUID DistinctID of a fan event) and
	// for the rare SDK queue-overflow case (codes.Internal). Transient
	// destination failures are retried internally by the implementation and
	// never surfaced here.
	Capture(ctx context.Context, event AnalyticsEvent) error
}

// AnalyticsEvent is one product-analytics event.
type AnalyticsEvent struct {
	// Name MUST be one of the AnalyticsEventName constants defined in
	// analytics_events.go; implementations reject unknown names (see
	// IsKnownEvent) so a typo fails fast instead of fragmenting dashboards.
	Name AnalyticsEventName

	// UUID identifies the event at the destination: the CloudEvent id for a
	// forwarded domain event, a new entity.NewID() for an event a use case
	// sends directly. Required.
	UUID string

	// Time is when the fact happened, read from the domain event data (or,
	// for an event a use case sends directly, the time the work ended).
	// Required.
	Time time.Time

	// DistinctID is the fan's platform-internal UserId UUID (never the
	// Zitadel sub). It is ignored for a Personless event.
	DistinctID string

	// Personless marks an event of a pipeline process with no fan (a search
	// outcome, an external model call). The process the implementation runs
	// as (its telemetry service name) is then the distinct id, no person
	// profile is created, and the process and the backend's release version
	// are added as the `process` and `app_version` properties.
	Personless bool

	// Properties is the per-event payload, optional, sanitised per the PII
	// policy. Implementations MUST NOT mutate it.
	Properties AnalyticsProperties
}

// errorCode names the kind of error err is for a pipeline signal: the code
// of the first apperr in its chain (Connect's names, such as
// "deadline_exceeded" or "permission_denied"), or "unknown" when it has
// none. The message is never reported: it can quote page content or model
// output, and the signal's trace_id leads to it in the logs.
func errorCode(err error) string {
	if appErr, ok := errors.AsType[*apperr.AppErr](err); ok {
		return appErr.Code.String()
	}
	return "unknown"
}

// sendSystemSignal sends a personless pipeline signal through client, which
// may be nil (no PostHog key configured: nothing is sent), with a new event
// id and now as the time the work ended. A failure is logged and never
// returned, so a signal can never change the outcome it reports.
func sendSystemSignal(ctx context.Context, client AnalyticsClient, logger *logging.Logger, eventName AnalyticsEventName, properties AnalyticsProperties) {
	if client == nil {
		return
	}
	if err := client.Capture(ctx, AnalyticsEvent{
		Name:       eventName,
		UUID:       entity.NewID(),
		Time:       time.Now(),
		Personless: true,
		Properties: properties,
	}); err != nil {
		logger.Warn(ctx, "failed to send a product-analytics signal",
			slog.String("event", string(eventName)),
			slog.Any("error", err),
		)
	}
}
