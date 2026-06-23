package usecase

import "context"

// FeatureFlagEvaluator resolves PostHog feature flags for backend code.
//
// Per the introduce-analytics-tool design (Decision 9), backend flag
// evaluation MUST degrade gracefully: an unconfigured evaluator, a missing
// flag definition, or a PostHog outage MUST resolve to the caller-supplied
// default rather than block a handler or surface an error. Every method
// therefore REQUIRES a default value at the call site and never returns an
// error — the default IS the failure mode, which makes "PostHog is
// unreachable" indistinguishable from "flag is off" to the caller and keeps
// Connect-RPC handlers non-blocking.
//
// Implementations SHOULD use PostHog local evaluation (periodic
// flag-definition sync via a personal API key) so that a call does not make
// a network round-trip to PostHog on the request path.
//
// ctx is accepted for cancellation-scoped logging and symmetry with the
// rest of the usecase boundary; implementations are NOT required to
// propagate it to the underlying SDK, whose flag API is not context-aware.
type FeatureFlagEvaluator interface {
	// IsEnabled reports whether the boolean flag identified by key is on
	// for distinctID (the platform UserId UUID). It returns defaultValue
	// on any failure: empty key or distinctID, evaluator unconfigured,
	// flag not found, PostHog unreachable, or a non-boolean flag value.
	IsEnabled(ctx context.Context, key, distinctID string, defaultValue bool) bool

	// Variant returns the assigned string variant of a multivariate flag
	// identified by key for distinctID. It returns defaultValue on any
	// failure, mirroring IsEnabled's degradation contract.
	Variant(ctx context.Context, key, distinctID, defaultValue string) string
}
