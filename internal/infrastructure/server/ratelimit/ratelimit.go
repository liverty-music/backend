// Package ratelimit provides a Connect-RPC interceptor that enforces per-key
// token bucket rate limiting. Authenticated requests are keyed by JWT subject
// claim; unauthenticated requests are keyed by client IP address.
package ratelimit

import (
	"context"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"connectrpc.com/authn"
	"connectrpc.com/connect"
	"golang.org/x/time/rate"
)

// Config holds rate limiting parameters.
type Config struct {
	// AuthRPS is the sustained request rate for authenticated users.
	AuthRPS float64
	// AuthBurst is the maximum burst size for authenticated users.
	AuthBurst int
	// AnonRPS is the sustained request rate for unauthenticated clients.
	AnonRPS float64
	// AnonBurst is the maximum burst size for unauthenticated clients.
	AnonBurst int
}

// entry tracks a limiter and its last access time for eviction.
type entry struct {
	limiter    *rate.Limiter
	lastAccess time.Time
}

// Limiter manages per-key rate limiters with background eviction.
type Limiter struct {
	mu      sync.Mutex
	entries map[string]*entry
	cfg     Config
	done    chan struct{}
}

// NewLimiter creates a Limiter that evicts idle entries every evictInterval.
func NewLimiter(cfg Config, evictInterval time.Duration) *Limiter {
	l := &Limiter{
		entries: make(map[string]*entry),
		cfg:     cfg,
		done:    make(chan struct{}),
	}
	go l.evictLoop(evictInterval)
	return l
}

const idleTimeout = 10 * time.Minute

func (l *Limiter) evictLoop(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-l.done:
			return
		case now := <-ticker.C:
			l.mu.Lock()
			for key, e := range l.entries {
				if now.Sub(e.lastAccess) > idleTimeout {
					delete(l.entries, key)
				}
			}
			l.mu.Unlock()
		}
	}
}

// Close stops the background eviction goroutine.
// It implements io.Closer for graceful shutdown integration.
func (l *Limiter) Close() error {
	close(l.done)
	return nil
}

// Allow checks whether the given key is within its rate limit.
// authenticated controls which rate parameters are applied.
func (l *Limiter) Allow(key string, authenticated bool) bool {
	l.mu.Lock()
	e, ok := l.entries[key]
	if !ok {
		var lim *rate.Limiter
		if authenticated {
			lim = rate.NewLimiter(rate.Limit(l.cfg.AuthRPS), l.cfg.AuthBurst)
		} else {
			lim = rate.NewLimiter(rate.Limit(l.cfg.AnonRPS), l.cfg.AnonBurst)
		}
		e = &entry{limiter: lim}
		l.entries[key] = e
	}
	e.lastAccess = time.Now()
	l.mu.Unlock()

	return e.limiter.Allow()
}

// NewInterceptor returns a Connect-RPC unary interceptor that rate-limits
// requests. Authenticated callers are keyed by JWT subject; unauthenticated
// callers are keyed by client IP.
func NewInterceptor(limiter *Limiter) connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			key, authenticated := extractKey(ctx, req.Header())
			if !limiter.Allow(key, authenticated) {
				err := connect.NewError(connect.CodeResourceExhausted, nil)
				err.Meta().Set("Retry-After", "1")
				return nil, err
			}
			return next(ctx, req)
		}
	}
}

// SubjectProvider is satisfied by any claims type that has a Sub field accessor.
// This avoids a direct import of the auth package from the ratelimit package.
type SubjectProvider interface {
	Subject() string
}

// extractKey determines the rate limit key and whether the request is authenticated.
// For authenticated requests, the key is "user:<sub>". For unauthenticated
// requests, the key is "ip:<client_ip>".
//
// IP extraction relies on X-Forwarded-For / X-Real-Ip headers. In GKE, the
// GCP load balancer always injects X-Forwarded-For, so RemoteAddr fallback is
// not needed in production. In local development without a proxy, unauthenticated
// requests will be keyed as "ip:" (empty suffix) and share a single bucket.
func extractKey(ctx context.Context, headers http.Header) (string, bool) {
	if info := authn.GetInfo(ctx); info != nil {
		if sp, ok := info.(SubjectProvider); ok {
			if sub := sp.Subject(); sub != "" {
				return "user:" + sub, true
			}
		}
		// Treat any non-nil info as authenticated even without SubjectProvider.
		return "user:unknown", true
	}

	ip := ClientIP(headers)
	return "ip:" + ip, false
}

// ClientIP extracts the client IP address from request headers.
// It uses X-Forwarded-For (leftmost entry) if present, falling back to
// X-Real-Ip, then an empty string.
func ClientIP(headers http.Header) string {
	if xff := headers.Get("X-Forwarded-For"); xff != "" {
		// Leftmost entry is the original client IP.
		if i := strings.IndexByte(xff, ','); i != -1 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	if xri := headers.Get("X-Real-Ip"); xri != "" {
		return strings.TrimSpace(xri)
	}
	return ""
}

// ClientIPFromAddr extracts IP from a net.Addr-style "host:port" string.
func ClientIPFromAddr(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return host
}
