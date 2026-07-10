package messaging_test

import (
	"testing"

	"github.com/liverty-music/backend/internal/infrastructure/messaging"
	"github.com/stretchr/testify/assert"
)

// liveAfterGrace calls Live enough times to exhaust the grace window and
// returns the final verdict. A healthy consumer stays live across the window;
// an unhealthy one is reported dead once the grace is exhausted.
func liveAfterGrace(h *messaging.ConsumerHealth) bool {
	var live bool
	// The grace tolerates a few consecutive unhealthy observations, so probe
	// generously to reach the steady-state verdict.
	for range 10 {
		live = h.Live()
	}
	return live
}

func TestConsumerHealth_HealthyWhenAllExpectedBound(t *testing.T) {
	t.Parallel()

	h := messaging.NewConsumerHealth()
	h.Expect("CONCERT.created")
	h.Expect("ARTIST.followed")
	h.MarkBound("CONCERT.created")
	h.MarkBound("ARTIST.followed")

	assert.True(t, liveAfterGrace(h), "all durables bound and connected should be live")
}

func TestConsumerHealth_NoExpectationsIsLive(t *testing.T) {
	t.Parallel()

	// A freshly constructed tracker (e.g. before any subscription, or the
	// GoChannel local path) has no expectations and is connected by default.
	h := messaging.NewConsumerHealth()

	assert.True(t, liveAfterGrace(h))
}

func TestConsumerHealth_UnboundDurableIsUnhealthy(t *testing.T) {
	t.Parallel()

	h := messaging.NewConsumerHealth()
	h.Expect("CONCERT.created")
	h.Expect("NOTIFICATION.delivered")
	// Only one of the two expected durables is bound — the consumer is not
	// fully consuming.
	h.MarkBound("CONCERT.created")

	assert.False(t, liveAfterGrace(h), "a missing subscription must report unhealthy")
}

func TestConsumerHealth_ConnectionDownIsUnhealthy(t *testing.T) {
	t.Parallel()

	h := messaging.NewConsumerHealth()
	h.Expect("CONCERT.created")
	h.MarkBound("CONCERT.created")
	h.SetConnected(false)

	assert.False(t, liveAfterGrace(h), "a downed NATS connection must report unhealthy")
}

func TestConsumerHealth_StoppedRouterIsUnhealthy(t *testing.T) {
	t.Parallel()

	h := messaging.NewConsumerHealth()
	h.Expect("CONCERT.created")
	h.MarkBound("CONCERT.created")
	h.SetRouterProbe(func() bool { return false })

	assert.False(t, liveAfterGrace(h), "a stopped router must report unhealthy")
}

func TestConsumerHealth_UnbindThenRebindRecovers(t *testing.T) {
	t.Parallel()

	h := messaging.NewConsumerHealth()
	h.Expect("CONCERT.created")
	h.MarkBound("CONCERT.created")

	h.MarkUnbound("CONCERT.created")
	assert.False(t, liveAfterGrace(h))

	// Rebinding restores health and the grace counter resets, so a single
	// healthy observation is enough to report live again.
	h.MarkBound("CONCERT.created")
	assert.True(t, h.Live())
}

func TestConsumerHealth_GraceAbsorbsTransientBlip(t *testing.T) {
	t.Parallel()

	h := messaging.NewConsumerHealth()
	h.Expect("CONCERT.created")
	h.MarkBound("CONCERT.created")

	// A single unhealthy observation (a transient blip) is absorbed by the
	// grace window and does not immediately report unhealthy.
	h.SetConnected(false)
	assert.True(t, h.Live(), "one unhealthy observation is within grace")

	// Recovering before the grace is exhausted keeps the consumer live.
	h.SetConnected(true)
	assert.True(t, h.Live())
}
