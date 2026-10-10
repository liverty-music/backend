package messaging

import (
	"sync"
	"time"
)

// livenessWindow is how long the consumer may stay unhealthy while connected
// (or closed) before liveness reports it dead. Being time-based, it does not
// depend on the probe period.
const livenessWindow = 2 * time.Minute

// ConsumerHealth tracks, in-process, whether the event consumer is actually
// consuming: the router is running and every expected JetStream durable is
// bound to an active subscription. The wedge that caused the 2026-07 outage
// left the pod Running while consuming nothing; a plain HTTP-port liveness
// probe could not see it. This tracker lets the liveness probe reflect real
// consumption so Kubernetes restarts a wedged pod.
//
// A NATS disconnection that is still reconnecting is degraded, not dead: the
// connection recovers by itself, and restarting the pod would not bring the
// broker back sooner. Only a consumer that is connected (or whose connection
// has given up) and not consuming is reported dead.
//
// ConsumerHealth is safe for concurrent use.
type ConsumerHealth struct {
	mu       sync.Mutex
	expected map[string]bool // topic -> bound
	// connected reports whether the underlying NATS connection is currently up.
	// It defaults to true because the GoChannel (local) transport has no
	// connection to lose and the NATS transport is connected by the time the
	// subscriber is constructed; NATS connection handlers flip it thereafter.
	connected bool
	// closed reports that the NATS connection gave up reconnecting. It never
	// recovers, so it counts against liveness like a wedge.
	closed bool
	// routerRunning probes whether the message router is actively running. It
	// is injected after the router is built (nil before then, treated as up so
	// startup readiness — not liveness — gates traffic during initialization).
	routerRunning func() bool
	// unhealthySince is when the current unhealthy period began; zero while
	// healthy or reconnecting.
	unhealthySince time.Time
	now            func() time.Time
}

// NewConsumerHealth returns a ConsumerHealth using the wall clock.
func NewConsumerHealth() *ConsumerHealth {
	return newConsumerHealth(time.Now)
}

func newConsumerHealth(now func() time.Time) *ConsumerHealth {
	return &ConsumerHealth{
		expected:  make(map[string]bool),
		connected: true,
		now:       now,
	}
}

// Expect registers a topic whose durable must be bound for the consumer to be
// considered healthy. Registering an already-known topic preserves its bound
// state.
func (h *ConsumerHealth) Expect(topic string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.expected[topic]; !ok {
		h.expected[topic] = false
	}
}

// MarkBound records that the durable for topic is bound to an active
// subscription.
func (h *ConsumerHealth) MarkBound(topic string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.expected[topic] = true
}

// MarkUnbound records that the durable for topic is no longer bound.
func (h *ConsumerHealth) MarkUnbound(topic string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.expected[topic]; ok {
		h.expected[topic] = false
	}
}

// SetConnected updates the NATS connection status.
func (h *ConsumerHealth) SetConnected(connected bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.connected = connected
}

// SetClosed records that the NATS connection gave up reconnecting.
func (h *ConsumerHealth) SetClosed() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.connected = false
	h.closed = true
}

// SetRouterProbe injects a probe reporting whether the message router is
// running. It is called once the router has been constructed.
func (h *ConsumerHealth) SetRouterProbe(probe func() bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.routerRunning = probe
}

// consuming reports whether the router runs and every expected durable is
// bound. The caller must hold h.mu.
func (h *ConsumerHealth) consuming() bool {
	if h.routerRunning != nil && !h.routerRunning() {
		return false
	}
	for _, bound := range h.expected {
		if !bound {
			return false
		}
	}
	return true
}

// Live reports whether the consumer should be considered alive. While the NATS
// connection is reconnecting it is alive, whatever the durables report: they
// cannot be bound until the broker is back. Otherwise it is dead once it has
// been closed, or not consuming, continuously for livenessWindow.
func (h *ConsumerHealth) Live() bool {
	h.mu.Lock()
	defer h.mu.Unlock()

	reconnecting := !h.connected && !h.closed
	if reconnecting || (!h.closed && h.consuming()) {
		h.unhealthySince = time.Time{}
		return true
	}

	now := h.now()
	if h.unhealthySince.IsZero() {
		h.unhealthySince = now
	}
	return now.Sub(h.unhealthySince) < livenessWindow
}
