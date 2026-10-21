package api_test

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/cenkalti/backoff/v7"
	"github.com/liverty-music/backend/pkg/api"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// statusCases is the shared status→apperr-code policy table, exercised through
// both FromStatus and FromHTTP so the two entry points can never drift.
var statusCases = []struct {
	name     string
	status   int
	wantErr  error // apperr sentinel; nil means "no error expected"
	wantNoop bool
}{
	{name: "200 → nil", status: http.StatusOK, wantNoop: true},
	{name: "204 → nil", status: http.StatusNoContent, wantNoop: true},
	{name: "400 → InvalidArgument", status: http.StatusBadRequest, wantErr: apperr.ErrInvalidArgument},
	{name: "401 → Unauthenticated", status: http.StatusUnauthorized, wantErr: apperr.ErrUnauthenticated},
	{name: "403 → PermissionDenied", status: http.StatusForbidden, wantErr: apperr.ErrPermissionDenied},
	{name: "404 → NotFound", status: http.StatusNotFound, wantErr: apperr.ErrNotFound},
	{name: "409 → AlreadyExists", status: http.StatusConflict, wantErr: apperr.ErrAlreadyExists},
	{name: "412 → FailedPrecondition", status: http.StatusPreconditionFailed, wantErr: apperr.ErrFailedPrecondition},
	{name: "429 → ResourceExhausted", status: http.StatusTooManyRequests, wantErr: apperr.ErrResourceExhausted},
	{name: "418 (unmapped 4xx) → InvalidArgument", status: http.StatusTeapot, wantErr: apperr.ErrInvalidArgument},
	{name: "500 → Unavailable", status: http.StatusInternalServerError, wantErr: apperr.ErrUnavailable},
	{name: "502 (unmapped 5xx) → Unavailable", status: http.StatusBadGateway, wantErr: apperr.ErrUnavailable},
	{name: "503 → ResourceExhausted", status: http.StatusServiceUnavailable, wantErr: apperr.ErrResourceExhausted},
	{name: "504 → DeadlineExceeded", status: http.StatusGatewayTimeout, wantErr: apperr.ErrDeadlineExceeded},
	{name: "0 (no status) → Internal", status: 0, wantErr: apperr.ErrInternal},
	{name: "302 (3xx) → Internal", status: http.StatusFound, wantErr: apperr.ErrInternal},
}

func TestFromStatus(t *testing.T) {
	t.Parallel()

	for _, tc := range statusCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// cause == nil path (creates a fresh error).
			err := api.FromStatus(tc.status, nil, "op failed")
			if tc.wantNoop {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.ErrorIs(t, err, tc.wantErr)
		})
	}
}

func TestFromStatus_WrapsCause(t *testing.T) {
	t.Parallel()

	cause := errors.New("boom")
	err := api.FromStatus(http.StatusNotFound, cause, "lookup failed")

	require.Error(t, err)
	assert.ErrorIs(t, err, apperr.ErrNotFound, "code from status")
	assert.ErrorIs(t, err, cause, "underlying cause is preserved in the chain")
}

func TestFromHTTP_DelegatesToStatusPolicy(t *testing.T) {
	t.Parallel()

	for _, tc := range statusCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			resp := &http.Response{StatusCode: tc.status}
			err := api.FromHTTP(nil, resp, "http op")
			if tc.wantNoop {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.ErrorIs(t, err, tc.wantErr)
		})
	}
}

func TestFromHTTP_NetworkAndContextErrors(t *testing.T) {
	t.Parallel()

	t.Run("nil err and nil resp → nil", func(t *testing.T) {
		t.Parallel()
		assert.NoError(t, api.FromHTTP(nil, nil, "op"))
	})
	t.Run("context.Canceled → Canceled", func(t *testing.T) {
		t.Parallel()
		err := api.FromHTTP(context.Canceled, nil, "op")
		assert.ErrorIs(t, err, apperr.ErrCanceled)
	})
	t.Run("context.DeadlineExceeded → DeadlineExceeded", func(t *testing.T) {
		t.Parallel()
		err := api.FromHTTP(context.DeadlineExceeded, nil, "op")
		assert.ErrorIs(t, err, apperr.ErrDeadlineExceeded)
	})
	t.Run("other network error → Unavailable", func(t *testing.T) {
		t.Parallel()
		err := api.FromHTTP(errors.New("dial tcp: connection refused"), nil, "op")
		assert.ErrorIs(t, err, apperr.ErrUnavailable)
	})
}

// retryErr runs backoff.Retry with an operation that always fails with opErr
// (assert.AnError stands for a retryable failure such as a 503)
// and returns the error Retry produces.
func retryErr(t *testing.T, ctx context.Context, opErr error, opts ...backoff.RetryOption) error {
	t.Helper()
	_, err := backoff.Retry(ctx, func() (struct{}, error) { return struct{}{}, opErr }, opts...)
	require.Error(t, err)
	return err
}

// TestFromHTTP_RetryErrors pins the codes FromHTTP gives errors that come out
// of backoff.Retry. Since backoff v6, Retry wraps every failure in a
// *backoff.RetryError, so a context error is no longer returned bare; the
// codes below are the ones the bare errors mapped to under backoff v5.
func TestFromHTTP_RetryErrors(t *testing.T) {
	t.Parallel()

	cancelled := func(t *testing.T) context.Context {
		t.Helper()
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		return ctx
	}
	expired := func(t *testing.T) context.Context {
		t.Helper()
		ctx, cancel := context.WithDeadline(t.Context(), time.Unix(0, 0))
		t.Cleanup(cancel)
		return ctx
	}

	tests := []struct {
		name    string
		err     func(t *testing.T) error
		wantErr error
	}{
		{
			name: "caller cancellation between attempts → Canceled",
			err: func(t *testing.T) error {
				t.Helper()
				return retryErr(t, cancelled(t), assert.AnError)
			},
			wantErr: apperr.ErrCanceled,
		},
		{
			name: "caller deadline between attempts → DeadlineExceeded",
			err: func(t *testing.T) error {
				t.Helper()
				return retryErr(t, expired(t), assert.AnError)
			},
			wantErr: apperr.ErrDeadlineExceeded,
		},
		{
			name: "permanent context.Canceled (throttler wait) → Canceled",
			err: func(t *testing.T) error {
				t.Helper()
				return retryErr(t, t.Context(), backoff.Permanent(context.Canceled))
			},
			wantErr: apperr.ErrCanceled,
		},
		{
			name: "max elapsed time reached → Unavailable, not DeadlineExceeded",
			err: func(t *testing.T) error {
				t.Helper()
				return retryErr(t, t.Context(), assert.AnError,
					backoff.WithBackOff(backoff.NewConstantBackOff(time.Hour)),
					backoff.WithMaxElapsedTime(time.Minute))
			},
			wantErr: apperr.ErrUnavailable,
		},
		{
			name: "max tries exhausted → Unavailable",
			err: func(t *testing.T) error {
				t.Helper()
				return retryErr(t, t.Context(), assert.AnError, backoff.WithMaxTries(1))
			},
			wantErr: apperr.ErrUnavailable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := api.FromHTTP(tt.err(t), nil, "op")

			assert.ErrorIs(t, err, tt.wantErr)
		})
	}
}

func TestFromHTTP_ContextErrorWrappedByHTTPClient(t *testing.T) {
	t.Parallel()

	// http.Client returns a *url.Error around the context error; it maps to
	// the context's code like a bare context error does.
	tests := []struct {
		name    string
		err     error
		wantErr error
	}{
		{
			name:    "url.Error around Canceled → Canceled",
			err:     &url.Error{Op: "Get", URL: "http://example.test", Err: context.Canceled},
			wantErr: apperr.ErrCanceled,
		},
		{
			name:    "url.Error around DeadlineExceeded → DeadlineExceeded",
			err:     &url.Error{Op: "Get", URL: "http://example.test", Err: context.DeadlineExceeded},
			wantErr: apperr.ErrDeadlineExceeded,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.ErrorIs(t, api.FromHTTP(tt.err, nil, "op"), tt.wantErr)
		})
	}
}
