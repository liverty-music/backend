// Package posthog provides a usecase.AnalyticsClient implementation backed
// by the github.com/posthog/posthog-go SDK. It targets PostHog Cloud EU
// (https://eu.i.posthog.com) by default and posts events asynchronously
// through the SDK's internal worker so that callers never block on
// network availability of the analytics destination.
package posthog

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
	posthogsdk "github.com/posthog/posthog-go"
	"go.opentelemetry.io/otel/trace"

	"github.com/liverty-music/backend/internal/usecase"
)

// DefaultAPIHost is the PostHog Cloud EU ingestion endpoint. Used when the
// caller passes an empty apiHost to New.
const DefaultAPIHost = "https://eu.i.posthog.com"

// tracePropertyKey is the property name under which the active OpenTelemetry
// trace ID is recorded on every emitted event. It matches the catalogue's
// BaseProps.trace_id contract and lets PostHog event payloads correlate to
// Cloud Trace spans during incident investigation.
const tracePropertyKey = "trace_id"

// enqueuer is the minimal subset of the posthog SDK Client that this
// adapter depends on. Declaring it here lets tests inject a fake without
// implementing the dozen feature-flag methods on the full posthog.Client
// interface. Both methods are exposed on the SDK's Client interface, so
// the real posthog SDK client satisfies enqueuer implicitly.
type enqueuer interface {
	Enqueue(posthogsdk.Message) error
	Close() error
}

// AnalyticsClient is the PostHog-backed implementation of
// usecase.AnalyticsClient. Construction is via New for production code
// and via newWithEnqueuer (exposed through export_test.go) for tests.
//
// AnalyticsClient also exposes a Close() error method (io.Closer
// compatible) so the DI layer can register the instance with the
// shutdown manager. Close is NOT part of usecase.AnalyticsClient; it
// stays at the infrastructure layer where lifecycle concerns belong.
type AnalyticsClient struct {
	client enqueuer
	logger *logging.Logger

	closeOnce sync.Once
	closeErr  error
}

// Compile-time interface compliance check against the usecase contract.
var _ usecase.AnalyticsClient = (*AnalyticsClient)(nil)

// New constructs an AnalyticsClient backed by a PostHog SDK client
// configured against the given apiHost and projectAPIKey.
//
// projectAPIKey MUST be non-empty after trimming whitespace; the
// posthog-go SDK trims the key internally and silently returns a no-op
// client when the trimmed value is empty (posthog.go:225-229 in v1.13.1).
// Without an explicit guard here, a misconfigured secret containing only
// whitespace would pass the constructor and cause every subsequent
// Enqueue call to be silently discarded in production.
//
// If apiHost is empty, DefaultAPIHost (PostHog Cloud EU) is used. logger
// is required; tests in the gemini/musicbrainz/lastfm adapters
// demonstrate the project convention of constructing a no-op logger
// instead of passing nil.
//
// The returned client owns a background worker that flushes events
// asynchronously; call Close at process shutdown to flush in-flight
// events.
func New(apiHost, projectAPIKey string, logger *logging.Logger) (*AnalyticsClient, error) {
	if strings.TrimSpace(projectAPIKey) == "" {
		return nil, apperr.New(codes.InvalidArgument, "posthog: project API key must not be empty or whitespace-only")
	}
	if apiHost == "" {
		apiHost = DefaultAPIHost
	}
	if logger == nil {
		return nil, apperr.New(codes.InvalidArgument, "posthog: logger must not be nil")
	}

	sdkClient, err := posthogsdk.NewWithConfig(projectAPIKey, posthogsdk.Config{
		Endpoint: apiHost,
	})
	if err != nil {
		return nil, apperr.Wrap(err, codes.Internal, "create posthog client")
	}
	return newWithEnqueuer(sdkClient, logger), nil
}

// newWithEnqueuer wraps an existing enqueuer. Exposed for tests via
// export_test.go.
func newWithEnqueuer(client enqueuer, logger *logging.Logger) *AnalyticsClient {
	return &AnalyticsClient{
		client: client,
		logger: logger,
	}
}

// Enqueue implements usecase.AnalyticsClient.
//
// Validation:
//   - distinctID must be a valid UUID; the platform `UserId` is a UUID
//     (see entity/v1/user.proto). This catches the misuse of passing
//     the Zitadel `sub` claim (also a UUID-like string but issued by a
//     different namespace) when it differs from a UUID format. Even
//     when sub happens to be a UUID, this validation enforces format
//     hygiene at the boundary.
//   - eventName must be registered in usecase.IsKnownEvent. A typo'd
//     constant (e.g. AnalyticsEventName("ticket.purcase.completed"))
//     would otherwise silently fragment dashboards.
//
// Context handling:
//   - ctx is inspected for an active OpenTelemetry span; if found, its
//     trace ID is injected as the `trace_id` property unless the caller
//     already supplied one.
//   - ctx cancellation does NOT abort the SDK send: posthog-go's public
//     Client interface does not expose a context-aware Enqueue
//     (EnqueueWithContext is unexported), so the wrapper falls back to
//     the SDK's synchronous-into-async-queue Enqueue.
//
// The supplied properties map is never mutated; trace_id injection,
// when applied, happens on a defensive copy.
func (c *AnalyticsClient) Enqueue(
	ctx context.Context,
	distinctID string,
	eventName usecase.AnalyticsEventName,
	properties usecase.AnalyticsProperties,
) error {
	if distinctID == "" {
		return apperr.New(codes.InvalidArgument, "posthog: distinctID must not be empty")
	}
	if _, err := uuid.Parse(distinctID); err != nil {
		return apperr.New(codes.InvalidArgument, fmt.Sprintf("posthog: distinctID must be a UUID (got %q)", distinctID))
	}
	if eventName == "" {
		return apperr.New(codes.InvalidArgument, "posthog: eventName must not be empty")
	}
	if !usecase.IsKnownEvent(eventName) {
		return apperr.New(codes.InvalidArgument, fmt.Sprintf("posthog: unknown eventName %q (not in usecase.IsKnownEvent allowlist)", eventName))
	}

	sdkProps := buildSDKProperties(ctx, properties)

	if err := c.client.Enqueue(posthogsdk.Capture{
		DistinctId: distinctID,
		Event:      string(eventName),
		Properties: sdkProps,
	}); err != nil {
		return apperr.Wrap(err, codes.Internal, fmt.Sprintf("posthog enqueue %s", eventName))
	}
	return nil
}

// buildSDKProperties prepares the property bag handed to the SDK.
//
// Fast path (no active OTel span, caller already supplied trace_id, or
// caller passed nil): zero-cost type conversion since
// usecase.AnalyticsProperties and posthogsdk.Properties are both
// map[string]any with the same underlying type — no allocation, no
// rehash.
//
// Slow path (active span and no caller-supplied trace_id): allocate
// a copy with cap len(properties)+1 and inject trace_id. The caller's
// map is never mutated.
func buildSDKProperties(ctx context.Context, properties usecase.AnalyticsProperties) posthogsdk.Properties {
	span := trace.SpanFromContext(ctx)
	hasSpan := span.SpanContext().IsValid()
	_, callerSetTrace := properties[tracePropertyKey]

	if !hasSpan || callerSetTrace {
		// Zero-cost conversion. Nil properties yields nil sdkProps,
		// which the SDK treats as an empty payload.
		if properties == nil {
			return nil
		}
		return posthogsdk.Properties(properties)
	}

	// Defensive copy to inject trace_id without mutating the caller's map.
	out := make(posthogsdk.Properties, len(properties)+1)
	for k, v := range properties {
		out[k] = v
	}
	out[tracePropertyKey] = span.SpanContext().TraceID().String()
	return out
}

// Close flushes in-flight events and releases the SDK's background worker.
//
// Close is idempotent: subsequent calls return the same error result as
// the first call (typically nil). This satisfies the io.Closer
// contract and prevents shutdown sequences that invoke Close twice
// (signal handler + main defer, for instance) from spuriously reporting
// the SDK's "client was already closed" error.
//
// Close signature is intentionally io.Closer-compatible (no context.Context)
// so the DI layer can register the instance via shutdown.AddExternalPhase
// alongside the other outbound clients (lastfm, musicbrainz, etc.). If
// callers need shutdown deadlines, they wrap this call in a
// context.WithTimeout goroutine; the underlying SDK Close blocks until
// in-flight events drain.
func (c *AnalyticsClient) Close() error {
	c.closeOnce.Do(func() {
		if err := c.client.Close(); err != nil {
			c.closeErr = apperr.Wrap(err, codes.Internal, "posthog close")
		}
	})
	return c.closeErr
}
