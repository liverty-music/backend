package api_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

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
