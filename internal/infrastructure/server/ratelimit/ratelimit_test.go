package ratelimit_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"connectrpc.com/connect"
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
		defer l.Close()

		assert.True(t, l.Allow("user:alice", true))
		assert.True(t, l.Allow("user:alice", true))
	})

	t.Run("authenticated user exceeds burst", func(t *testing.T) {
		t.Parallel()
		l := newTestLimiter()
		defer l.Close()

		// Consume the burst (2).
		l.Allow("user:bob", true)
		l.Allow("user:bob", true)

		// Third request should be rejected (no time for token refill).
		assert.False(t, l.Allow("user:bob", true))
	})

	t.Run("unauthenticated client within burst", func(t *testing.T) {
		t.Parallel()
		l := newTestLimiter()
		defer l.Close()

		assert.True(t, l.Allow("ip:1.2.3.4", false))
	})

	t.Run("unauthenticated client exceeds burst", func(t *testing.T) {
		t.Parallel()
		l := newTestLimiter()
		defer l.Close()

		// Consume the burst (1).
		l.Allow("ip:5.6.7.8", false)

		// Second request rejected.
		assert.False(t, l.Allow("ip:5.6.7.8", false))
	})

	t.Run("different users have independent limits", func(t *testing.T) {
		t.Parallel()
		l := newTestLimiter()
		defer l.Close()

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
	defer l.Close()

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
			name:    "X-Forwarded-For with multiple IPs",
			headers: http.Header{"X-Forwarded-For": []string{"203.0.113.50, 70.41.3.18, 150.172.238.178"}},
			want:    "203.0.113.50",
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
			name:    "X-Forwarded-For with spaces",
			headers: http.Header{"X-Forwarded-For": []string{"  192.168.1.1 , 10.0.0.1 "}},
			want:    "192.168.1.1",
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

func TestInterceptor_RetryAfterHeader(t *testing.T) {
	t.Parallel()

	l := newTestLimiter()
	defer l.Close()

	interceptor := ratelimit.NewInterceptor(l)

	// Exhaust the anon burst (1) for a fixed IP key.
	handler := interceptor(func(_ context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		return nil, nil
	})

	req := connect.NewRequest(&struct{}{})
	req.Header().Set("X-Forwarded-For", "203.0.113.1")

	// First request is within burst — must succeed.
	_, err := handler(context.Background(), req)
	require.NoError(t, err)

	// Second request exceeds burst — must return ResourceExhausted with Retry-After.
	_, err = handler(context.Background(), req)
	require.Error(t, err)

	var connectErr *connect.Error
	require.ErrorAs(t, err, &connectErr)
	assert.Equal(t, connect.CodeResourceExhausted, connectErr.Code())
	assert.Equal(t, "1", connectErr.Meta().Get("Retry-After"))
}
