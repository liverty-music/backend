package gemini_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/liverty-music/backend/internal/infrastructure/gcp/gemini"
	"github.com/stretchr/testify/assert"
	"google.golang.org/genai"
)

func TestIsRetryable(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "504 Gateway Timeout is retryable",
			err:  genai.APIError{Code: http.StatusGatewayTimeout, Message: "timeout"},
			want: true,
		},
		{
			name: "503 Service Unavailable is retryable",
			err:  genai.APIError{Code: http.StatusServiceUnavailable, Message: "unavailable"},
			want: true,
		},
		{
			name: "429 Too Many Requests is retryable",
			err:  genai.APIError{Code: http.StatusTooManyRequests, Message: "rate limited"},
			want: true,
		},
		{
			name: "499 Client Cancelled is retryable",
			err:  genai.APIError{Code: 499, Message: "cancelled"},
			want: true,
		},
		{
			name: "400 Bad Request is not retryable",
			err:  genai.APIError{Code: http.StatusBadRequest, Message: "bad request"},
			want: false,
		},
		{
			name: "401 Unauthorized is retryable (transient WI token refresh)",
			err:  genai.APIError{Code: http.StatusUnauthorized, Message: "unauthorized"},
			want: true,
		},
		{
			name: "500 Internal Server Error is not retryable",
			err:  genai.APIError{Code: http.StatusInternalServerError, Message: "internal"},
			want: false,
		},
		{
			name: "context.DeadlineExceeded is not retryable",
			err:  context.DeadlineExceeded,
			want: false,
		},
		{
			name: "context.Canceled is not retryable",
			err:  context.Canceled,
			want: false,
		},
		{
			name: "wrapped non-API error is not retryable",
			err:  fmt.Errorf("wrapped: %w", fmt.Errorf("some error")),
			want: false,
		},
		{
			name: "nil error is not retryable",
			err:  nil,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := gemini.IsRetryable(tt.err)
			assert.Equal(t, tt.want, got)
		})
	}
}
