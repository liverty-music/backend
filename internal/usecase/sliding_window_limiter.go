package usecase

import (
	"sync"
	"time"
)

// slidingWindowSweepThreshold is the number of tracked keys above which an
// allow call drops every key whose events have all aged out, bounding memory
// when keys come from unauthenticated callers.
const slidingWindowSweepThreshold = 10000

// slidingWindowLimiter allows at most limit events per key within any rolling
// window. It is in-memory and per process.
type slidingWindowLimiter struct {
	mu     sync.Mutex
	limit  int
	window time.Duration
	now    func() time.Time
	events map[string][]time.Time
}

// newSlidingWindowLimiter returns a limiter that allows limit events per key
// within window. now is the clock (time.Now in production).
func newSlidingWindowLimiter(limit int, window time.Duration, now func() time.Time) *slidingWindowLimiter {
	return &slidingWindowLimiter{
		limit:  limit,
		window: window,
		now:    now,
		events: make(map[string][]time.Time),
	}
}

// allow reports whether key has had fewer than limit events within the
// window, and records this event when it has.
func (l *slidingWindowLimiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	recent := l.recentLocked(key, now)
	if len(recent) >= l.limit {
		return false
	}
	l.events[key] = append(recent, now)
	if len(l.events) > slidingWindowSweepThreshold {
		for k := range l.events {
			l.recentLocked(k, now)
		}
	}
	return true
}

// recentLocked drops key's events older than the window and returns the rest.
// The caller holds l.mu.
func (l *slidingWindowLimiter) recentLocked(key string, now time.Time) []time.Time {
	events := l.events[key]
	keep := 0
	for keep < len(events) && now.Sub(events[keep]) >= l.window {
		keep++
	}
	events = events[keep:]
	if len(events) == 0 {
		delete(l.events, key)
		return nil
	}
	l.events[key] = events
	return events
}
