package httpx_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cenkalti/backoff/v7"
	"github.com/liverty-music/backend/pkg/api"
	"github.com/liverty-music/backend/pkg/httpx"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// shortRetryOpts returns options that minimize backoff delays for fast tests.
func shortRetryOpts(maxRetries uint) []httpx.Option {
	return []httpx.Option{
		httpx.WithMaxRetries(maxRetries),
		httpx.WithInitialInterval(1 * time.Millisecond),
		httpx.WithMaxInterval(5 * time.Millisecond),
	}
}

func TestRetryTransport_RetryOnTransientStatus(t *testing.T) {
	t.Parallel()

	type args struct {
		statusCode int
	}
	tests := []struct {
		name string
		args args
	}{
		{name: "retry on 429", args: args{statusCode: http.StatusTooManyRequests}},
		{name: "retry on 503", args: args{statusCode: http.StatusServiceUnavailable}},
		{name: "retry on 504", args: args{statusCode: http.StatusGatewayTimeout}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if calls.Add(1) == 1 {
					w.WriteHeader(tt.args.statusCode)
					return
				}
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("ok"))
			}))
			defer srv.Close()

			client := &http.Client{
				Transport: httpx.NewRetryTransport(nil, shortRetryOpts(3)...),
			}

			resp, err := client.Get(srv.URL)
			require.NoError(t, err)
			defer func() { _ = resp.Body.Close() }()

			assert.Equal(t, http.StatusOK, resp.StatusCode)
			assert.Equal(t, int32(2), calls.Load())
		})
	}
}

func TestRetryTransport_NoRetryOnClientErrors(t *testing.T) {
	t.Parallel()

	type args struct {
		statusCode int
	}
	tests := []struct {
		name string
		args args
	}{
		{name: "no retry on 400", args: args{statusCode: http.StatusBadRequest}},
		{name: "no retry on 401", args: args{statusCode: http.StatusUnauthorized}},
		{name: "no retry on 404", args: args{statusCode: http.StatusNotFound}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.WriteHeader(tt.args.statusCode)
			}))
			defer srv.Close()

			client := &http.Client{
				Transport: httpx.NewRetryTransport(nil, shortRetryOpts(3)...),
			}

			resp, err := client.Get(srv.URL)
			require.NoError(t, err)
			defer func() { _ = resp.Body.Close() }()

			assert.Equal(t, tt.args.statusCode, resp.StatusCode)
			assert.Equal(t, int32(1), calls.Load())
		})
	}
}

func TestRetryTransport_AllRetriesExhausted(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	client := &http.Client{
		Transport: httpx.NewRetryTransport(nil, shortRetryOpts(3)...),
	}

	resp, err := client.Get(srv.URL)

	// When all retries are exhausted, the last error is returned.
	// The response is nil because the body was closed during retry.
	assert.Nil(t, resp)
	assert.Error(t, err)
	assert.Equal(t, int32(3), calls.Load())
}

func TestRetryTransport_RetryAfterDeltaSeconds(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := &http.Client{
		Transport: httpx.NewRetryTransport(nil, shortRetryOpts(3)...),
	}

	resp, err := client.Get(srv.URL)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, int32(2), calls.Load())
}

func TestRetryTransport_RetryAfterHTTPDate(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			// Set Retry-After to an HTTP-date ~1ms in the future (effectively immediate).
			w.Header().Set("Retry-After", time.Now().Add(1*time.Millisecond).UTC().Format(http.TimeFormat))
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := &http.Client{
		Transport: httpx.NewRetryTransport(nil, shortRetryOpts(3)...),
	}

	resp, err := client.Get(srv.URL)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, int32(2), calls.Load())
}

func TestRetryTransport_ContextCancellation(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()

	client := &http.Client{
		Transport: httpx.NewRetryTransport(nil,
			httpx.WithMaxRetries(100),
			httpx.WithInitialInterval(20*time.Millisecond),
			httpx.WithMaxInterval(20*time.Millisecond),
		),
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	require.NoError(t, err)

	resp, reqErr := client.Do(req)
	assert.Nil(t, resp)
	assert.Error(t, reqErr)
	// Should have stopped early, not exhausted all 100 retries.
	assert.Less(t, calls.Load(), int32(100))
}

func TestRetryTransport_POSTBodyReplay(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	var lastBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		lastBody = string(body)
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := &http.Client{
		Transport: httpx.NewRetryTransport(nil, shortRetryOpts(3)...),
	}

	payload := []byte(`{"key":"value"}`)
	resp, err := client.Post(srv.URL, "application/json", bytes.NewReader(payload))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, int32(2), calls.Load())
	assert.Equal(t, `{"key":"value"}`, lastBody)
}

// roundTripFunc adapts a function to http.RoundTripper so tests can run the
// transport inside a synctest bubble without real network I/O.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

// retryAfterThenOK answers the first request with 429 and the given
// Retry-After value, and every later request with 200.
func retryAfterThenOK(retryAfter func() string) (http.RoundTripper, *atomic.Int32) {
	var calls atomic.Int32
	return roundTripFunc(func(req *http.Request) (*http.Response, error) {
		resp := &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{},
			Body:       http.NoBody,
			Request:    req,
		}
		if calls.Add(1) == 1 {
			resp.StatusCode = http.StatusTooManyRequests
			resp.Header.Set("Retry-After", retryAfter())
		}
		return resp, nil
	}), &calls
}

func TestRetryTransport_HonoursRetryAfterDelay(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		retryAfter func() string
		wantWait   time.Duration
	}{
		{
			name:       "delta-seconds",
			retryAfter: func() string { return "30" },
			wantWait:   30 * time.Second,
		},
		{
			name: "HTTP-date",
			retryAfter: func() string {
				// synctest's clock starts on a whole second, so the
				// second-resolution HTTP-date yields an exact delay.
				return time.Now().Add(45 * time.Second).UTC().Format(http.TimeFormat)
			},
			wantWait: 45 * time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				base, calls := retryAfterThenOK(tt.retryAfter)
				// The exponential backoff would wait about 1 s; the server
				// delay must replace it.
				rt := httpx.NewRetryTransport(base, httpx.WithMaxRetries(3))
				req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://example.test", nil)
				require.NoError(t, err)

				start := time.Now()
				resp, err := rt.RoundTrip(req)
				require.NoError(t, err)
				defer func() { _ = resp.Body.Close() }()

				assert.Equal(t, http.StatusOK, resp.StatusCode)
				assert.Equal(t, int32(2), calls.Load())
				assert.Equal(t, tt.wantWait, time.Since(start))
			})
		})
	}
}

func TestRetryTransport_ContextEndKeepsAppErrCode(t *testing.T) {
	t.Parallel()

	// The context ends while the transport waits out a 60 s Retry-After.
	// backoff v6+ wraps the context error in a *backoff.RetryError, and
	// api.FromHTTP must still map it to the context's own code.
	tests := []struct {
		name    string
		newCtx  func(ctx context.Context) (context.Context, context.CancelFunc)
		wantErr error
	}{
		{
			name: "cancellation maps to Canceled",
			newCtx: func(ctx context.Context) (context.Context, context.CancelFunc) {
				ctx, cancel := context.WithCancel(ctx)
				time.AfterFunc(10*time.Second, cancel)
				return ctx, cancel
			},
			wantErr: apperr.ErrCanceled,
		},
		{
			name: "deadline maps to DeadlineExceeded",
			newCtx: func(ctx context.Context) (context.Context, context.CancelFunc) {
				return context.WithTimeout(ctx, 10*time.Second)
			},
			wantErr: apperr.ErrDeadlineExceeded,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				base, calls := retryAfterThenOK(func() string { return "60" })
				rt := httpx.NewRetryTransport(base, httpx.WithMaxRetries(3))
				ctx, cancel := tt.newCtx(t.Context())
				defer cancel()
				req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://example.test", nil)
				require.NoError(t, err)

				start := time.Now()
				resp, err := rt.RoundTrip(req)

				assert.Nil(t, resp)
				assert.Equal(t, int32(1), calls.Load(), "no attempt after the context ended")
				assert.Equal(t, 10*time.Second, time.Since(start), "stopped when the context ended")
				assert.ErrorIs(t, api.FromHTTP(err, nil, "op"), tt.wantErr)
			})
		})
	}
}

func TestRetryAfterFromResponse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		retryAfter string
		wantDelay  time.Duration // 0 means no *backoff.RetryAfterError
	}{
		{name: "delta-seconds", retryAfter: "7", wantDelay: 7 * time.Second},
		{name: "no header", retryAfter: ""},
		{name: "unparsable header", retryAfter: "soon"},
		{name: "HTTP-date in the past", retryAfter: "Mon, 02 Jan 2006 15:04:05 GMT"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			resp := &http.Response{StatusCode: http.StatusTooManyRequests, Header: http.Header{}}
			if tt.retryAfter != "" {
				resp.Header.Set("Retry-After", tt.retryAfter)
			}

			err := httpx.RetryAfterFromResponse(resp)

			require.Error(t, err)
			assert.Contains(t, err.Error(), http.StatusText(http.StatusTooManyRequests),
				"the status survives as the error text")
			retryAfter, ok := errors.AsType[*backoff.RetryAfterError](err)
			if tt.wantDelay == 0 {
				assert.False(t, ok)
				return
			}
			require.True(t, ok)
			assert.Equal(t, tt.wantDelay, retryAfter.Duration)
		})
	}
}
