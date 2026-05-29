package posthog

// NewWithEnqueuer exposes the unexported newWithEnqueuer constructor to
// tests in the posthog_test package so they can inject a fake enqueuer.
var NewWithEnqueuer = newWithEnqueuer

// Enqueuer re-exports the unexported enqueuer interface so tests can
// declare a fake that satisfies it.
type Enqueuer = enqueuer
