package ratelimit_test

import (
	"net/http"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/liverty-music/backend/internal/infrastructure/server/ratelimit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestLimiter() *ratelimit.Limiter {
	return ratelimit.NewLimiter(ratelimit.Config{
		AuthRPS:   10,
		AuthBurst: 2,
		AnonRPS:   5,
		AnonBurst: 1,
	}, time.Hour) // long eviction interval — tests control timing
}

func TestLimiter_Allow(t *testing.T) {
	t.Parallel()

	t.Run("authenticated user within burst", func(t *testing.T) {
		t.Parallel()
		l := newTestLimiter()
		defer func() { _ = l.Close() }()

		assert.True(t, l.Allow("user:alice", true))
		assert.True(t, l.Allow("user:alice", true))
	})

	t.Run("authenticated user exceeds burst", func(t *testing.T) {
		t.Parallel()
		l := newTestLimiter()
		defer func() { _ = l.Close() }()

		// Consume the burst (2).
		l.Allow("user:bob", true)
		l.Allow("user:bob", true)

		// Third request should be rejected (no time for token refill).
		assert.False(t, l.Allow("user:bob", true))
	})

	t.Run("unauthenticated client within burst", func(t *testing.T) {
		t.Parallel()
		l := newTestLimiter()
		defer func() { _ = l.Close() }()

		assert.True(t, l.Allow("ip:1.2.3.4", false))
	})

	t.Run("unauthenticated client exceeds burst", func(t *testing.T) {
		t.Parallel()
		l := newTestLimiter()
		defer func() { _ = l.Close() }()

		// Consume the burst (1).
		l.Allow("ip:5.6.7.8", false)

		// Second request rejected.
		assert.False(t, l.Allow("ip:5.6.7.8", false))
	})

	t.Run("different users have independent limits", func(t *testing.T) {
		t.Parallel()
		l := newTestLimiter()
		defer func() { _ = l.Close() }()

		// Exhaust alice's burst.
		l.Allow("user:alice", true)
		l.Allow("user:alice", true)
		assert.False(t, l.Allow("user:alice", true))

		// Bob is unaffected.
		assert.True(t, l.Allow("user:bob", true))
	})
}

func TestLimiter_NewKeyGetsFullBurst(t *testing.T) {
	t.Parallel()

	l := newTestLimiter()
	defer func() { _ = l.Close() }()

	// Exhaust burst for one key.
	l.Allow("user:alice", true)
	l.Allow("user:alice", true)
	assert.False(t, l.Allow("user:alice", true))

	// A completely new key still gets full burst.
	assert.True(t, l.Allow("user:fresh", true))
	assert.True(t, l.Allow("user:fresh", true))
	assert.False(t, l.Allow("user:fresh", true))
}

func TestClientIP(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		headers http.Header
		want    string
	}{
		{
			name:    "X-Forwarded-For with single IP",
			headers: http.Header{"X-Forwarded-For": []string{"203.0.113.50"}},
			want:    "203.0.113.50",
		},
		{
			name:    "X-Forwarded-For with multiple IPs returns rightmost (GCP LB entry)",
			headers: http.Header{"X-Forwarded-For": []string{"203.0.113.50, 70.41.3.18, 150.172.238.178"}},
			want:    "150.172.238.178",
		},
		{
			name:    "X-Real-Ip fallback",
			headers: http.Header{"X-Real-Ip": []string{"10.0.0.1"}},
			want:    "10.0.0.1",
		},
		{
			name:    "no headers returns empty",
			headers: http.Header{},
			want:    "",
		},
		{
			name:    "X-Forwarded-For with spaces returns rightmost trimmed",
			headers: http.Header{"X-Forwarded-For": []string{"  192.168.1.1 , 10.0.0.1 "}},
			want:    "10.0.0.1",
		},
		{
			name:    "X-Forwarded-For spoofed prefix ignored — rightmost is GCP LB entry",
			headers: http.Header{"X-Forwarded-For": []string{"1.2.3.4, 5.6.7.8, 203.0.113.99"}},
			want:    "203.0.113.99",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, ratelimit.ClientIP(tt.headers))
		})
	}
}

func TestClientIPFromAddr(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		addr string
		want string
	}{
		{name: "host:port", addr: "192.168.1.1:8080", want: "192.168.1.1"},
		{name: "ipv6 with port", addr: "[::1]:8080", want: "::1"},
		{name: "no port", addr: "192.168.1.1", want: "192.168.1.1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, ratelimit.ClientIPFromAddr(tt.addr))
		})
	}
}

func TestInterceptor(t *testing.T) {
	t.Parallel()

	t.Run("reject an anonymous unary call over the burst with ResourceExhausted and Retry-After", func(t *testing.T) {
		t.Parallel()
		l := newTestLimiter()
		defer func() { _ = l.Close() }()
		srv := newTestRPCServer(t, nil, ratelimit.NewInterceptor(l))
		opts := callOptions{forwardedFor: "203.0.113.1"}

		// The anon burst is 1: the first call passes, the second is refused.
		_, err := srv.callUnary(t, opts)
		require.NoError(t, err)

		header, err := srv.callUnary(t, opts)
		assert.Equal(t, connect.CodeResourceExhausted, connect.CodeOf(err))
		assert.Equal(t, "1", header.Get("Retry-After"))
	})

	t.Run("key anonymous callers by client IP", func(t *testing.T) {
		t.Parallel()
		l := newTestLimiter()
		defer func() { _ = l.Close() }()
		srv := newTestRPCServer(t, nil, ratelimit.NewInterceptor(l))

		_, err := srv.callUnary(t, callOptions{forwardedFor: "203.0.113.1"})
		require.NoError(t, err)
		_, err = srv.callUnary(t, callOptions{forwardedFor: "203.0.113.2"})
		assert.NoError(t, err, "another IP has its own bucket")
	})

	t.Run("key authenticated callers by subject with the authenticated burst", func(t *testing.T) {
		t.Parallel()
		l := newTestLimiter()
		defer func() { _ = l.Close() }()
		srv := newTestRPCServer(t, nil, ratelimit.NewInterceptor(l))
		opts := callOptions{forwardedFor: "203.0.113.1", subject: "user-1"}

		// The auth burst is 2.
		for i := range 2 {
			_, err := srv.callUnary(t, opts)
			require.NoError(t, err, "call %d", i+1)
		}
		_, err := srv.callUnary(t, opts)
		assert.Equal(t, connect.CodeResourceExhausted, connect.CodeOf(err))

		_, err = srv.callUnary(t, callOptions{forwardedFor: "203.0.113.1", subject: "user-2"})
		assert.NoError(t, err, "another subject has its own bucket")
	})

	t.Run("pass streaming calls through without counting them, as the v1 unary interceptor did", func(t *testing.T) {
		t.Parallel()
		l := newTestLimiter()
		defer func() { _ = l.Close() }()
		srv := newTestRPCServer(t, nil, ratelimit.NewInterceptor(l))
		opts := callOptions{forwardedFor: "203.0.113.1"}

		for i := range 3 {
			require.NoError(t, srv.callServerStream(t, opts), "stream %d", i+1)
		}
		_, err := srv.callUnary(t, opts)
		assert.NoError(t, err, "streams did not use the unary burst")
	})
}
