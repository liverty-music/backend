package workflow

import "time"

// ShortenStepRetries makes failed steps retry after base, at most max apart,
// for tests that cannot wait the production intervals. It returns a function
// that restores them; tests that call it do not run in parallel.
func ShortenStepRetries(base, maxInterval time.Duration) (restore func()) {
	oldBase, oldMax := stepBaseInterval, stepMaxInterval
	stepBaseInterval, stepMaxInterval = base, maxInterval
	return func() { stepBaseInterval, stepMaxInterval = oldBase, oldMax }
}

// SetClock replaces the clock delete-old-workflows reads, so a test can move
// it forward instead of waiting 30 days.
func SetClock(r *Runtime, now func() time.Time) {
	r.admin.mu.Lock()
	defer r.admin.mu.Unlock()
	r.admin.now = now
}
