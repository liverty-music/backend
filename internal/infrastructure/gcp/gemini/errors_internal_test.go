package gemini

import (
	"context"
	"errors"
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
