package ratelimit

import (
	"context"
	"errors"
	"sync"
	"time"

	"connectrpc.com/connect"
)

// ClientThrottle counts calls per client, such as the calls made with unknown
// secrets (Scanner link tokens) or all calls to a public procedure, and
// blocks a client that has made limit of them within window, until the oldest
// of those calls is older than window. It is in-memory and per process.
type ClientThrottle struct {
	mu     sync.Mutex
	limit  int
	window time.Duration
	now    func() time.Time
	calls  map[string][]time.Time
}

// sweepThreshold is the number of tracked clients above which a record call
// drops every client whose calls have all aged out, bounding memory.
const sweepThreshold = 10000

// NewClientThrottle returns a throttle that blocks a client after limit
// counted calls within window. now is the clock (time.Now in production).
func NewClientThrottle(limit int, window time.Duration, now func() time.Time) *ClientThrottle {
	return &ClientThrottle{
		limit:  limit,
		window: window,
		now:    now,
		calls:  make(map[string][]time.Time),
	}
}

// Blocked reports whether client has already made limit counted calls within
// the window, so its next call must be refused.
func (t *ClientThrottle) Blocked(client string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.recentLocked(client, t.now())) >= t.limit
}

// Record counts one call by client.
func (t *ClientThrottle) Record(client string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.recordLocked(client, t.now())
}

// Allow reports whether client is not blocked and, when it is not, counts
// this call, in one step.
func (t *ClientThrottle) Allow(client string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	if len(t.recentLocked(client, now)) >= t.limit {
		return false
	}
	t.recordLocked(client, now)
	return true
}

// recordLocked counts one call by client at now. The caller holds t.mu.
func (t *ClientThrottle) recordLocked(client string, now time.Time) {
	t.calls[client] = append(t.recentLocked(client, now), now)
	if len(t.calls) > sweepThreshold {
		for c := range t.calls {
			t.recentLocked(c, now)
		}
	}
}

// recentLocked drops client's calls older than the window and returns the
// rest. The caller holds t.mu.
func (t *ClientThrottle) recentLocked(client string, now time.Time) []time.Time {
	calls := t.calls[client]
	keep := 0
	for keep < len(calls) && now.Sub(calls[keep]) >= t.window {
		keep++
	}
	calls = calls[keep:]
	if len(calls) == 0 {
		delete(t.calls, client)
		return nil
	}
	t.calls[client] = calls
	return calls
}

// NewUnknownTokenInterceptor returns a unary interceptor that applies throttle
// to the given procedures: a client already blocked is refused with
// ResourceExhausted before the handler runs, and a call whose error satisfies
// isUnknownToken counts as one unknown-token call of its client. The client
// is keyed by its IP as seen by the load balancer (see [ClientIP]), else by
// the peer address. Place it inside the error-handling interceptor so it sees
// the handler's own error.
func NewUnknownTokenInterceptor(throttle *ClientThrottle, procedures map[string]bool, isUnknownToken func(error) bool) connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			if !procedures[req.Spec().Procedure] {
				return next(ctx, req)
			}
			client := clientKey(req)
			if throttle.Blocked(client) {
				return nil, connect.NewError(connect.CodeResourceExhausted,
					errors.New("too many calls with unknown links; try again later"))
			}
			resp, err := next(ctx, req)
			if err != nil && isUnknownToken(err) {
				throttle.Record(client)
			}
			return resp, err
		}
	}
}

// NewClientQuotaInterceptor returns a unary interceptor that applies throttle
// to every call of the given procedures: a client already blocked is refused
// with ResourceExhausted before the handler runs, and every other call counts
// as one call of its client. The client is keyed as in
// [NewUnknownTokenInterceptor].
func NewClientQuotaInterceptor(throttle *ClientThrottle, procedures map[string]bool) connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			if !procedures[req.Spec().Procedure] {
				return next(ctx, req)
			}
			if !throttle.Allow(clientKey(req)) {
				return nil, connect.NewError(connect.CodeResourceExhausted,
					errors.New("too many requests; try again later"))
			}
			return next(ctx, req)
		}
	}
}

// clientKey identifies the client of req by its IP as seen by the load
// balancer (see [ClientIP]), else by the peer address.
func clientKey(req connect.AnyRequest) string {
	if client := ClientIP(req.Header()); client != "" {
		return client
	}
	return ClientIPFromAddr(req.Peer().Addr)
}
