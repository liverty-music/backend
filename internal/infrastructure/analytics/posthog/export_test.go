package posthog

// NewWithEnqueuer exposes the unexported newWithEnqueuer constructor to
// tests in the posthog_test package so they can inject a fake enqueuer.
var NewWithEnqueuer = newWithEnqueuer

// Enqueuer re-exports the unexported enqueuer interface so tests can
// declare a fake that satisfies it.
type Enqueuer = enqueuer

// TracePropertyKey re-exports the unexported tracePropertyKey constant
// so tests can assert that injected trace_id properties land under the
// expected key without duplicating the literal.
const TracePropertyKey = tracePropertyKey

// NewFeatureFlagEvaluatorWith exposes the unexported
// newFeatureFlagEvaluatorWith constructor to tests so they can inject a
// fake flag evaluator.
var NewFeatureFlagEvaluatorWith = newFeatureFlagEvaluatorWith

// FlagEvaluator re-exports the unexported flagEvaluator interface so tests
// can declare a fake that satisfies it.
type FlagEvaluator = flagEvaluator
