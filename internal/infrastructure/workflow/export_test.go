package workflow

import (
	"encoding/json"
	"time"

	"github.com/dbos-inc/dbos-transact-golang/dbos"
)

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

// TriggerSchedule runs the named schedule now instead of at its next tick and
// returns the run's workflow id.
func TriggerSchedule(c *Client, name string) (string, error) {
	handle, err := dbos.TriggerSchedule[json.RawMessage](c.c, name)
	if err != nil {
		return "", toAppErr(err, "trigger schedule "+name)
	}
	return handle.GetWorkflowID(), nil
}
