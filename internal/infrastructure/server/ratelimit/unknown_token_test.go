package ratelimit_test

import (
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/infrastructure/server/ratelimit"
	"github.com/stretchr/testify/assert"
)

func TestUnknownTokenThrottle(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 11, 20, 18, 0, 0, 0, time.UTC)

	t.Run("blocks after the limit until the window passes", func(t *testing.T) {
		t.Parallel()
		now := start
		th := ratelimit.NewUnknownTokenThrottle(10, 10*time.Minute, func() time.Time { return now })

		for i := range 10 {
			assert.False(t, th.Blocked("a"), "call %d", i+1)
			th.RecordUnknown("a")
			now = now.Add(time.Second)
		}
		assert.True(t, th.Blocked("a"), "the 11th call is refused")
		assert.False(t, th.Blocked("b"), "other clients are unaffected")

		now = start.Add(10*time.Minute - time.Second)
		assert.True(t, th.Blocked("a"), "still within 10 minutes of the first guess")
		now = start.Add(10 * time.Minute)
		assert.False(t, th.Blocked("a"), "the first guess aged out")
	})

	t.Run("guesses spread over more than the window never block", func(t *testing.T) {
		t.Parallel()
		now := start
		th := ratelimit.NewUnknownTokenThrottle(10, 10*time.Minute, func() time.Time { return now })
		for range 30 {
			assert.False(t, th.Blocked("a"))
			th.RecordUnknown("a")
			now = now.Add(time.Minute + time.Second)
		}
	})
}
