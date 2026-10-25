package gemini

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genai"
)

func TestToAppErr_GeminiHTTP499(t *testing.T) {
	t.Parallel()

	err := toAppErr(genai.APIError{Code: 499, Message: "The operation was cancelled."}, "failed to call Gemini API")

	appErr, ok := errors.AsType[*apperr.AppErr](err)
	require.True(t, ok, "expected AppErr, got %T", err)
	assert.Equal(t, codes.Canceled, appErr.Code)
}

func TestToAppErr_ContextCanceled(t *testing.T) {
	t.Parallel()

	err := toAppErr(context.Canceled, "failed to call Gemini API")

	appErr, ok := errors.AsType[*apperr.AppErr](err)
	require.True(t, ok, "expected AppErr, got %T", err)
	assert.Equal(t, codes.Canceled, appErr.Code)
}

func TestToAppErr_GeminiHTTP401And403(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  genai.APIError
		want codes.Code
	}{
		{
			name: "401 is missing or expired credentials",
			err:  genai.APIError{Code: http.StatusUnauthorized, Message: "unauthorized"},
			want: codes.Unauthenticated,
		},
		{
			name: "403 is a denial, such as a spent spend cap",
			err: genai.APIError{
				Code:    http.StatusForbidden,
				Message: "Spend cap breached for project",
				Status:  "PERMISSION_DENIED",
			},
			want: codes.PermissionDenied,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := toAppErr(tt.err, "failed to call Gemini API")

			appErr, ok := errors.AsType[*apperr.AppErr](err)
			require.True(t, ok, "expected AppErr, got %T", err)
			assert.Equal(t, tt.want, appErr.Code)
		})
	}
}
