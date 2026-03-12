package httpx_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/liverty-music/backend/pkg/httpx"
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
