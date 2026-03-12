// Package httpx provides HTTP transport utilities.
package httpx

import (
	"net/http"
	"strconv"
	"time"

	"github.com/cenkalti/backoff/v5"
)

// retryableStatusCodes contains HTTP status codes that are considered transient
// and eligible for retry.
var retryableStatusCodes = map[int]bool{
	http.StatusTooManyRequests:    true, // 429
	http.StatusServiceUnavailable: true, // 503
	http.StatusGatewayTimeout:     true, // 504
}

// RetryTransport wraps an http.RoundTripper with automatic retry logic
// using exponential backoff for transient HTTP errors (429, 503, 504).
// It respects Retry-After headers and replays request bodies via GetBody.
type RetryTransport struct {
	base            http.RoundTripper
	maxTries        uint
	initialInterval time.Duration
	maxInterval     time.Duration
}

// Option configures a RetryTransport.
type Option func(*RetryTransport)

// WithMaxRetries sets the maximum number of total attempts (including the first).
// Default is 4 (1 initial + 3 retries).
func WithMaxRetries(n uint) Option {
	return func(rt *RetryTransport) {
		rt.maxTries = n
	}
}

// WithInitialInterval sets the initial backoff interval before randomization.
// Default is 1 second.
func WithInitialInterval(d time.Duration) Option {
	return func(rt *RetryTransport) {
		rt.initialInterval = d
	}
}

// WithMaxInterval caps the exponential backoff interval.
// Default is 10 seconds.
func WithMaxInterval(d time.Duration) Option {
	return func(rt *RetryTransport) {
		rt.maxInterval = d
	}
}

// NewRetryTransport creates a RetryTransport that wraps base with exponential
// backoff retry on transient HTTP status codes (429, 503, 504).
//
// If base is nil, http.DefaultTransport is used.
func NewRetryTransport(base http.RoundTripper, opts ...Option) *RetryTransport {
	if base == nil {
		base = http.DefaultTransport
	}
	rt := &RetryTransport{
		base:            base,
		maxTries:        4,
		initialInterval: 1 * time.Second,
		maxInterval:     10 * time.Second,
	}
	for _, o := range opts {
		o(rt)
	}
	return rt
}

// RoundTrip executes the HTTP request with automatic retry on transient errors.
// For retries involving POST/PUT requests, the body is replayed via req.GetBody.
func (rt *RetryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	bo := &backoff.ExponentialBackOff{
		InitialInterval:     rt.initialInterval,
		RandomizationFactor: 0.5,
		Multiplier:          2.0,
		MaxInterval:         rt.maxInterval,
	}

	resp, err := backoff.Retry(req.Context(), func() (*http.Response, error) {
		// Replay the request body for retries (required for POST/PUT).
		if req.GetBody != nil {
			body, err := req.GetBody()
			if err != nil {
				return nil, backoff.Permanent(err)
			}
			req.Body = body
		}

		resp, err := rt.base.RoundTrip(req)
		if err != nil {
			return nil, backoff.Permanent(err)
		}

		if !IsRetryableStatus(resp.StatusCode) {
			return resp, nil
		}

		_ = resp.Body.Close()
		return nil, RetryAfterFromResponse(resp)
	},
		backoff.WithBackOff(bo),
		backoff.WithMaxTries(rt.maxTries),
		backoff.WithMaxElapsedTime(0), // No total time limit; controlled by maxTries and context.
	)

	return resp, err
}

// IsRetryableStatus reports whether the HTTP status code is transient and
// eligible for retry (429, 503, 504).
func IsRetryableStatus(statusCode int) bool {
	return retryableStatusCodes[statusCode]
}

// RetryAfterFromResponse parses the Retry-After header from resp and returns
// a backoff.RetryAfterError if present, or a plain retryableError otherwise.
// The caller should close the response body before calling this function.
func RetryAfterFromResponse(resp *http.Response) error {
	if ra := resp.Header.Get("Retry-After"); ra != "" {
		if seconds, err := strconv.Atoi(ra); err == nil {
			return backoff.RetryAfter(seconds)
		}
		if t, err := http.ParseTime(ra); err == nil {
			if wait := time.Until(t); wait > 0 {
				return &backoff.RetryAfterError{Duration: wait}
			}
		}
	}
	return retryableError{statusCode: resp.StatusCode}
}

// retryableError is a transient error returned during retry to trigger the next attempt.
type retryableError struct {
	statusCode int
}

func (e retryableError) Error() string {
	return http.StatusText(e.statusCode)
}
