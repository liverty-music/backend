package posthog

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
	posthogsdk "github.com/posthog/posthog-go"

	"github.com/liverty-music/backend/internal/usecase"
)

// defaultFlagPollingInterval is how often the SDK re-syncs flag
// definitions for local evaluation. Set explicitly rather than relying on
// the SDK zero-value so a misconfigured interval cannot turn into a busy
// poll. Five minutes matches the cadence the feature-flag policy assumes
// for release toggles and gradual rollouts.
const defaultFlagPollingInterval = 5 * time.Minute

// flagEvaluator is the minimal subset of the posthog SDK Client that the
// evaluator depends on. Declaring it here lets tests inject a fake without
// implementing the full posthog.Client surface, mirroring the enqueuer
// pattern in posthog_client.go.
type flagEvaluator interface {
	GetFeatureFlag(posthogsdk.FeatureFlagPayload) (any, error)
	Close() error
}

// FeatureFlagEvaluator is the PostHog-backed implementation of
// usecase.FeatureFlagEvaluator. It evaluates flags locally (no per-call
// network round-trip) using a personal API key to sync flag definitions.
//
// Like AnalyticsClient, it exposes an io.Closer-compatible Close so the DI
// layer can register it with the shutdown manager; Close is not part of the
// usecase contract and stays at the infrastructure layer.
type FeatureFlagEvaluator struct {
	client flagEvaluator
	logger *logging.Logger

	closeOnce sync.Once
	closeErr  error
}

// Compile-time interface compliance check against the usecase contract.
var _ usecase.FeatureFlagEvaluator = (*FeatureFlagEvaluator)(nil)

// NewFeatureFlagEvaluator constructs a FeatureFlagEvaluator backed by a
// PostHog SDK client configured for local evaluation.
//
// projectAPIKey and personalAPIKey MUST both be non-empty after trimming:
// the project key identifies the PostHog project and the personal key
// authorises the periodic flag-definition fetch that local evaluation
// requires. Without the personal key the SDK cannot evaluate locally and
// every call would fall through to its default, so construction fails fast
// instead — the DI layer is expected to skip wiring the evaluator (leaving
// flags on their call-site defaults) when the personal key is absent,
// matching the optional-dependency pattern used for the analytics client.
//
// If apiHost is empty, DefaultAPIHost (PostHog Cloud EU) is used. logger is
// required.
func NewFeatureFlagEvaluator(apiHost, projectAPIKey, personalAPIKey string, logger *logging.Logger) (*FeatureFlagEvaluator, error) {
	if strings.TrimSpace(projectAPIKey) == "" {
		return nil, apperr.New(codes.InvalidArgument, "posthog: project API key must not be empty or whitespace-only")
	}
	if strings.TrimSpace(personalAPIKey) == "" {
		return nil, apperr.New(codes.InvalidArgument, "posthog: personal API key must not be empty (required for local feature-flag evaluation)")
	}
	if apiHost == "" {
		apiHost = DefaultAPIHost
	}
	if logger == nil {
		return nil, apperr.New(codes.InvalidArgument, "posthog: logger must not be nil")
	}

	sdkClient, err := posthogsdk.NewWithConfig(projectAPIKey, posthogsdk.Config{
		Endpoint:                           apiHost,
		PersonalApiKey:                     personalAPIKey,
		DefaultFeatureFlagsPollingInterval: defaultFlagPollingInterval,
	})
	if err != nil {
		return nil, apperr.Wrap(err, codes.Internal, "create posthog feature-flag client")
	}
	return newFeatureFlagEvaluatorWith(sdkClient, logger), nil
}

// newFeatureFlagEvaluatorWith wraps an existing flagEvaluator. Exposed for
// tests via export_test.go.
func newFeatureFlagEvaluatorWith(client flagEvaluator, logger *logging.Logger) *FeatureFlagEvaluator {
	return &FeatureFlagEvaluator{
		client: client,
		logger: logger,
	}
}

// IsEnabled implements usecase.FeatureFlagEvaluator. It returns
// defaultValue whenever the flag cannot be resolved to a boolean — the
// evaluator is unset (typed-nil), inputs are empty, the SDK errors (e.g.
// no flag definitions synced yet), or the flag is multivariate rather than
// boolean.
func (e *FeatureFlagEvaluator) IsEnabled(ctx context.Context, key, distinctID string, defaultValue bool) bool {
	if e == nil || key == "" || distinctID == "" {
		return defaultValue
	}

	result, err := e.client.GetFeatureFlag(posthogsdk.FeatureFlagPayload{
		Key:                 key,
		DistinctId:          distinctID,
		OnlyEvaluateLocally: true,
	})
	if err != nil {
		e.logger.Warn(ctx, "feature flag evaluation failed; using default",
			slog.String("flag", key),
			slog.Bool("default", defaultValue),
			slog.String("error", err.Error()),
		)
		return defaultValue
	}

	enabled, ok := result.(bool)
	if !ok {
		return defaultValue
	}
	return enabled
}

// Variant implements usecase.FeatureFlagEvaluator. It returns defaultValue
// whenever the flag cannot be resolved to a string variant, mirroring
// IsEnabled's degradation contract.
func (e *FeatureFlagEvaluator) Variant(ctx context.Context, key, distinctID, defaultValue string) string {
	if e == nil || key == "" || distinctID == "" {
		return defaultValue
	}

	result, err := e.client.GetFeatureFlag(posthogsdk.FeatureFlagPayload{
		Key:                 key,
		DistinctId:          distinctID,
		OnlyEvaluateLocally: true,
	})
	if err != nil {
		e.logger.Warn(ctx, "feature flag variant evaluation failed; using default",
			slog.String("flag", key),
			slog.String("default", defaultValue),
			slog.String("error", err.Error()),
		)
		return defaultValue
	}

	variant, ok := result.(string)
	if !ok {
		return defaultValue
	}
	return variant
}

// Close releases the SDK's background flag poller. It is idempotent and
// io.Closer-compatible so the DI layer can register it via
// shutdown.AddExternalPhase alongside the other outbound clients.
func (e *FeatureFlagEvaluator) Close() error {
	e.closeOnce.Do(func() {
		if err := e.client.Close(); err != nil {
			e.closeErr = apperr.Wrap(err, codes.Internal, "posthog feature-flag close")
		}
	})
	return e.closeErr
}
