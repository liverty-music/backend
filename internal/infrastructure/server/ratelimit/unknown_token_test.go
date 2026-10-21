package ratelimit_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/liverty-music/backend/internal/infrastructure/server/ratelimit"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	apperrconnect "github.com/pannpers/go-apperr/apperr/connectv2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClientThrottle(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 11, 20, 18, 0, 0, 0, time.UTC)

	t.Run("blocks after the limit until the window passes", func(t *testing.T) {
		t.Parallel()
		now := start
		th := ratelimit.NewClientThrottle(10, 10*time.Minute, func() time.Time { return now })

		for i := range 10 {
			assert.False(t, th.Blocked("a"), "call %d", i+1)
			th.Record("a")
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
		th := ratelimit.NewClientThrottle(10, 10*time.Minute, func() time.Time { return now })
		for range 30 {
			assert.False(t, th.Blocked("a"))
			th.Record("a")
			now = now.Add(time.Minute + time.Second)
		}
	})
}

func TestUnknownTokenInterceptor(t *testing.T) {
	t.Parallel()

	errUnknownToken := apperr.New(codes.NotFound, "unknown link token")
	isUnknownToken := func(err error) bool { return errors.Is(err, apperr.ErrNotFound) }

	t.Run("count unknown-token unary calls and refuse a blocked client", func(t *testing.T) {
		t.Parallel()
		throttle := ratelimit.NewClientThrottle(2, 10*time.Minute, time.Now)
		srv := newTestRPCServer(t, func() error { return errUnknownToken },
			apperrconnect.NewErrorHandlingInterceptor(discardLogger{}),
			ratelimit.NewUnknownTokenInterceptor(throttle, map[string]bool{unaryProcedure: true}, isUnknownToken),
		)
		opts := callOptions{forwardedFor: "203.0.113.1"}

		for i := range 2 {
			_, err := srv.callUnary(t, opts)
			assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err), "call %d reaches the handler", i+1)
		}
		_, err := srv.callUnary(t, opts)
		assert.Equal(t, connect.CodeResourceExhausted, connect.CodeOf(err))
		assert.False(t, throttle.Blocked("203.0.113.2"), "other clients are unaffected")
	})

	t.Run("not count calls that fail for another reason", func(t *testing.T) {
		t.Parallel()
		throttle := ratelimit.NewClientThrottle(1, 10*time.Minute, time.Now)
		srv := newTestRPCServer(t, func() error { return apperr.New(codes.InvalidArgument, "bad") },
			ratelimit.NewUnknownTokenInterceptor(throttle, map[string]bool{unaryProcedure: true}, isUnknownToken),
		)

		_, err := srv.callUnary(t, callOptions{forwardedFor: "203.0.113.1"})
		require.Error(t, err)
		assert.False(t, throttle.Blocked("203.0.113.1"))
	})

	t.Run("ignore procedures outside the list", func(t *testing.T) {
		t.Parallel()
		throttle := ratelimit.NewClientThrottle(1, 10*time.Minute, time.Now)
		srv := newTestRPCServer(t, func() error { return errUnknownToken },
			ratelimit.NewUnknownTokenInterceptor(throttle, map[string]bool{"/test.v1.TestService/Other": true}, isUnknownToken),
		)

		_, err := srv.callUnary(t, callOptions{forwardedFor: "203.0.113.1"})
		require.Error(t, err)
		assert.False(t, throttle.Blocked("203.0.113.1"))
	})

	t.Run("pass streaming calls through, as the v1 unary interceptor did", func(t *testing.T) {
		t.Parallel()
		throttle := ratelimit.NewClientThrottle(1, 10*time.Minute, time.Now)
		throttle.Record("203.0.113.1") // the client is already blocked
		srv := newTestRPCServer(t, nil,
			ratelimit.NewUnknownTokenInterceptor(throttle, map[string]bool{serverStreamProcedure: true}, isUnknownToken),
		)

		assert.NoError(t, srv.callServerStream(t, callOptions{forwardedFor: "203.0.113.1"}))
	})

	t.Run("key a client without forwarding headers by its peer address", func(t *testing.T) {
		t.Parallel()
		throttle := ratelimit.NewClientThrottle(1, 10*time.Minute, time.Now)
		srv := newTestRPCServer(t, func() error { return errUnknownToken },
			ratelimit.NewUnknownTokenInterceptor(throttle, map[string]bool{unaryProcedure: true}, isUnknownToken),
		)

		_, err := srv.callUnary(t, callOptions{})
		require.Error(t, err)
		assert.True(t, throttle.Blocked("127.0.0.1"))
	})
}

func TestClientQuotaInterceptor(t *testing.T) {
	t.Parallel()

	t.Run("allow up to the limit of unary calls, then refuse", func(t *testing.T) {
		t.Parallel()
		throttle := ratelimit.NewClientThrottle(2, time.Hour, time.Now)
		srv := newTestRPCServer(t, nil, ratelimit.NewClientQuotaInterceptor(throttle, map[string]bool{unaryProcedure: true}))
		opts := callOptions{forwardedFor: "203.0.113.1"}

		for i := range 2 {
			_, err := srv.callUnary(t, opts)
			require.NoError(t, err, "call %d", i+1)
		}
		_, err := srv.callUnary(t, opts)
		assert.Equal(t, connect.CodeResourceExhausted, connect.CodeOf(err))

		_, err = srv.callUnary(t, callOptions{forwardedFor: "203.0.113.2"})
		assert.NoError(t, err, "other clients are unaffected")
	})

	t.Run("ignore procedures outside the list", func(t *testing.T) {
		t.Parallel()
		throttle := ratelimit.NewClientThrottle(1, time.Hour, time.Now)
		srv := newTestRPCServer(t, nil, ratelimit.NewClientQuotaInterceptor(throttle, map[string]bool{"/test.v1.TestService/Other": true}))
		opts := callOptions{forwardedFor: "203.0.113.1"}

		for range 3 {
			_, err := srv.callUnary(t, opts)
			require.NoError(t, err)
		}
	})

	t.Run("pass streaming calls through without counting them, as the v1 unary interceptor did", func(t *testing.T) {
		t.Parallel()
		throttle := ratelimit.NewClientThrottle(1, time.Hour, time.Now)
		srv := newTestRPCServer(t, nil, ratelimit.NewClientQuotaInterceptor(throttle,
			map[string]bool{unaryProcedure: true, serverStreamProcedure: true}))
		opts := callOptions{forwardedFor: "203.0.113.1"}

		for range 3 {
			require.NoError(t, srv.callServerStream(t, opts))
		}
		_, err := srv.callUnary(t, opts)
		assert.NoError(t, err, "streams did not use the quota")
	})
}

// discardLogger satisfies the apperr error-handling interceptor's logger and
// drops every record.
type discardLogger struct{}

func (discardLogger) Error(context.Context, string, error, ...slog.Attr) {}
