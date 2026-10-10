package messaging_test

import (
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/infrastructure/messaging"
	"github.com/stretchr/testify/assert"
)

// fakeClock is a manually advanced clock for ConsumerHealth.
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

// newBoundHealth returns a tracker whose topics are all expected and bound.
func newBoundHealth(clock *fakeClock, topics ...string) *messaging.ConsumerHealth {
	h := messaging.NewConsumerHealthWithClock(clock.now)
	for _, topic := range topics {
		h.Expect(topic)
		h.MarkBound(topic)
	}
	return h
}

// liveFor probes h every 20 s (the kubelet period) for d and reports whether
// every probe said alive.
func liveFor(h *messaging.ConsumerHealth, clock *fakeClock, d time.Duration) bool {
	for elapsed := time.Duration(0); elapsed <= d; elapsed += 20 * time.Second {
		if !h.Live() {
			return false
		}
		clock.advance(20 * time.Second)
	}
	return true
}

func TestConsumerHealth_Live(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// degrade puts h into the state under test.
		degrade func(h *messaging.ConsumerHealth)
		// probeFor is how long the state lasts while liveness is probed.
		probeFor time.Duration
		wantLive bool
	}{
		{
			name:     "all durables bound and connected",
			degrade:  func(*messaging.ConsumerHealth) {},
			probeFor: 10 * time.Minute,
			wantLive: true,
		},
		{
			// @spec components/infrastructure/backend/process/health-probes "Broker restarts"
			name: "disconnected and reconnecting for 3 minutes stays alive",
			degrade: func(h *messaging.ConsumerHealth) {
				h.SetConnected(false)
				// The durables cannot be bound while the broker is gone.
				h.MarkUnbound("CONCERT.created")
			},
			probeFor: 3 * time.Minute,
			wantLive: true,
		},
		{
			// @spec components/infrastructure/backend/process/health-probes "Connected but not consuming"
			name: "connected with a durable unbound for 2 minutes is not alive",
			degrade: func(h *messaging.ConsumerHealth) {
				h.MarkUnbound("CONCERT.created")
			},
			probeFor: 2 * time.Minute,
			wantLive: false,
		},
		{
			name: "connected with a durable unbound for under 2 minutes stays alive",
			degrade: func(h *messaging.ConsumerHealth) {
				h.MarkUnbound("CONCERT.created")
			},
			probeFor: 100 * time.Second,
			wantLive: true,
		},
		{
			name: "router stopped for 2 minutes is not alive",
			degrade: func(h *messaging.ConsumerHealth) {
				h.SetRouterProbe(func() bool { return false })
			},
			probeFor: 2 * time.Minute,
			wantLive: false,
		},
		{
			name: "connection closed for 2 minutes is not alive",
			degrade: func(h *messaging.ConsumerHealth) {
				h.SetClosed()
			},
			probeFor: 2 * time.Minute,
			wantLive: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			clock := &fakeClock{t: time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)}
			h := newBoundHealth(clock, "CONCERT.created", "ARTIST.followed")

			tt.degrade(h)

			assert.Equal(t, tt.wantLive, liveFor(h, clock, tt.probeFor))
		})
	}
}

func TestConsumerHealth_NoExpectationsIsLive(t *testing.T) {
	t.Parallel()

	// A freshly constructed tracker (e.g. before any subscription, or the
	// GoChannel local path) has no expectations and is connected by default.
	clock := &fakeClock{}
	h := messaging.NewConsumerHealthWithClock(clock.now)

	assert.True(t, liveFor(h, clock, 10*time.Minute))
}

func TestConsumerHealth_RecoveryResetsWindow(t *testing.T) {
	t.Parallel()

	clock := &fakeClock{}
	h := newBoundHealth(clock, "CONCERT.created")

	// Unbound for 100 s, rebound, then unbound again for 100 s: neither period
	// reaches the window on its own, so the consumer stays alive.
	h.MarkUnbound("CONCERT.created")
	assert.True(t, liveFor(h, clock, 100*time.Second))
	h.MarkBound("CONCERT.created")
	assert.True(t, h.Live())
	h.MarkUnbound("CONCERT.created")
	assert.True(t, liveFor(h, clock, 100*time.Second))
}

func TestConsumerHealth_ReconnectThenUnboundStartsWindowAfresh(t *testing.T) {
	t.Parallel()

	clock := &fakeClock{}
	h := newBoundHealth(clock, "CONCERT.created")

	// 3 minutes reconnecting with the durable unbound do not count; once
	// connected again, the window starts from the reconnect.
	h.SetConnected(false)
	h.MarkUnbound("CONCERT.created")
	assert.True(t, liveFor(h, clock, 3*time.Minute))
	h.SetConnected(true)
	assert.True(t, liveFor(h, clock, 100*time.Second))
}
