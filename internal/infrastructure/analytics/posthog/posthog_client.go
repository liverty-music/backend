// Package posthog provides a usecase.AnalyticsClient implementation backed
// by the github.com/posthog/posthog-go SDK. It targets PostHog Cloud EU
// (https://eu.i.posthog.com) by default and posts events asynchronously
// through the SDK's internal worker so that callers never block on
// network availability of the analytics destination.
package posthog

import (
	"context"
	"errors"
	"fmt"

	posthogsdk "github.com/posthog/posthog-go"

	"github.com/liverty-music/backend/internal/usecase"
)

// DefaultAPIHost is the PostHog Cloud EU ingestion endpoint. Used when the
// caller passes an empty apiHost to New.
const DefaultAPIHost = "https://eu.i.posthog.com"

// enqueuer is the minimal subset of the posthog SDK Client that this
// adapter depends on. Declaring it here lets tests inject a fake without
// having to implement the dozen feature-flag methods on the full
// posthog.Client interface.
type enqueuer interface {
	Enqueue(posthogsdk.Message) error
	Close() error
}

// AnalyticsClient is the PostHog-backed implementation of
// usecase.AnalyticsClient. Construction is via New for production code
// and via newWithEnqueuer (exposed through export_test.go) for tests.
type AnalyticsClient struct {
	client enqueuer
}

// Compile-time interface compliance check.
var _ usecase.AnalyticsClient = (*AnalyticsClient)(nil)

// New constructs an AnalyticsClient backed by a PostHog SDK client
// configured against the given apiHost and projectAPIKey. If apiHost is
// empty, DefaultAPIHost (PostHog Cloud EU) is used. The returned client
// owns a background worker that flushes events asynchronously; call Close
// at process shutdown to flush in-flight events.
func New(apiHost, projectAPIKey string) (*AnalyticsClient, error) {
	if projectAPIKey == "" {
		return nil, errors.New("posthog: project API key must not be empty")
	}
	if apiHost == "" {
		apiHost = DefaultAPIHost
	}

	sdkClient, err := posthogsdk.NewWithConfig(projectAPIKey, posthogsdk.Config{
		Endpoint: apiHost,
	})
	if err != nil {
		return nil, fmt.Errorf("create posthog client: %w", err)
	}
	return newWithEnqueuer(sdkClient), nil
}

// newWithEnqueuer wraps an existing enqueuer. Exposed for tests via
// export_test.go.
func newWithEnqueuer(client enqueuer) *AnalyticsClient {
	return &AnalyticsClient{client: client}
}

// Enqueue implements usecase.AnalyticsClient. It hands the event to the
// PostHog SDK's internal queue without blocking on network I/O. Transient
// network failures and retries are absorbed by the SDK worker. Errors
// returned here describe caller-side mistakes (empty distinctID, empty
// eventName) or the SDK's own queue overflow.
func (c *AnalyticsClient) Enqueue(
	_ context.Context,
	distinctID string,
	eventName usecase.AnalyticsEventName,
	properties usecase.AnalyticsProperties,
) error {
	if distinctID == "" {
		return errors.New("posthog: distinctID must not be empty")
	}
	if eventName == "" {
		return errors.New("posthog: eventName must not be empty")
	}

	props := posthogsdk.NewProperties()
	for k, v := range properties {
		props.Set(k, v)
	}

	if err := c.client.Enqueue(posthogsdk.Capture{
		DistinctId: distinctID,
		Event:      string(eventName),
		Properties: props,
	}); err != nil {
		return fmt.Errorf("posthog enqueue %s: %w", eventName, err)
	}
	return nil
}

// Close flushes in-flight events and releases the SDK's background worker.
// The underlying SDK Close blocks for up to its configured ShutdownTimeout
// (unlimited by default) until the queue drains. The supplied context is
// reserved for forwarding to a future CloseWithContext call; the SDK's
// public Close does not currently accept a context.
func (c *AnalyticsClient) Close(_ context.Context) error {
	if err := c.client.Close(); err != nil {
		return fmt.Errorf("posthog close: %w", err)
	}
	return nil
}
