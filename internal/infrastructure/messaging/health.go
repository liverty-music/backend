package messaging

import "sync"

// defaultLivenessGrace is the number of consecutive unhealthy observations the
// liveness check tolerates before reporting unhealthy. It absorbs transient
// blips (e.g. a brief NATS reconnect) so a healthy consumer is not restarted
// for a momentary flap. It composes with — and is deliberately smaller than —
// the Kubernetes probe's own failureThreshold.
const defaultLivenessGrace = 3

// ConsumerHealth tracks, in-process, whether the event consumer is actually
// consuming: the NATS connection is up and every expected JetStream durable is
// bound to an active subscription. The wedge that caused the 2026-07 outage
// left the pod Running while consuming nothing; a plain HTTP-port liveness
// probe could not see it. This tracker lets the liveness probe reflect real
// consumption so Kubernetes restarts a wedged pod.
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
	// routerRunning probes whether the message router is actively running. It
	// is injected after the router is built (nil before then, treated as up so
	// startup readiness — not liveness — gates traffic during initialization).
	routerRunning func() bool
	// failures counts consecutive unhealthy observations for the grace window.
	failures int
	grace    int
}

// NewConsumerHealth returns a ConsumerHealth with the default liveness grace.
func NewConsumerHealth() *ConsumerHealth {
	return &ConsumerHealth{
		expected:  make(map[string]bool),
		connected: true,
		grace:     defaultLivenessGrace,
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

// SetRouterProbe injects a probe reporting whether the message router is
// running. It is called once the router has been constructed.
func (h *ConsumerHealth) SetRouterProbe(probe func() bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.routerRunning = probe
}

// healthy reports the instantaneous health without applying the grace window.
// The caller must hold h.mu.
func (h *ConsumerHealth) healthy() bool {
	if !h.connected {
		return false
	}
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

// Live reports whether the consumer should be considered alive. It applies the
// grace window: an unhealthy observation is only fatal after `grace`
// consecutive occurrences, which prevents restart flapping on transient blips.
// A single healthy observation resets the counter.
func (h *ConsumerHealth) Live() bool {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.healthy() {
		h.failures = 0
		return true
	}
	h.failures++
	return h.failures < h.grace
}
