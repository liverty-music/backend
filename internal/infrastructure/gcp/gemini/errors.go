package gemini

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"google.golang.org/genai"
)

// toAppErr maps various infrastructure-level errors to domain-specific apperr.Error.
// It handles Context cancellations, Gemini API specifics, and JSON unmarshaling failures.
func toAppErr(err error, msg string, attrs ...slog.Attr) error {
	if err == nil {
		return nil
	}

	// Handle Context errors
	if errors.Is(err, context.DeadlineExceeded) {
		return apperr.Wrap(err, codes.DeadlineExceeded, msg, attrs...)
	}
	if errors.Is(err, context.Canceled) {
		return apperr.Wrap(err, codes.Canceled, msg, attrs...)
	}

	// Handle Gemini API errors
	if apiErr, ok := errors.AsType[genai.APIError](err); ok {
		var code codes.Code
		switch apiErr.Code {
		case http.StatusBadRequest:
			code = codes.InvalidArgument
		case http.StatusUnauthorized, http.StatusForbidden:
			code = codes.Unauthenticated
		case http.StatusNotFound:
			code = codes.NotFound
		case http.StatusTooManyRequests:
			code = codes.ResourceExhausted
		case http.StatusServiceUnavailable:
			code = codes.Unavailable
		case http.StatusGatewayTimeout:
			code = codes.DeadlineExceeded
		case 499: // Client Closed Request (Nginx-origin; Gemini uses it for server-side cancellation)
			code = codes.Canceled
		default:
			code = codes.Unknown
		}
		return apperr.Wrap(err, code, msg, attrs...)
	}

	// Handle JSON errors (usually indicate model output mismatch or malformed response)
	if _, ok := errors.AsType[*json.SyntaxError](err); ok {
		return apperr.Wrap(err, codes.Internal, msg, attrs...)
	}
	if _, ok := errors.AsType[*json.UnmarshalTypeError](err); ok {
		return apperr.Wrap(err, codes.Internal, msg, attrs...)
	}

	return apperr.Wrap(err, codes.Unknown, msg, attrs...)
}

// isRetryable reports whether err is a transient Gemini API error that
// may succeed on a subsequent attempt.
//
// Retryable status codes are aligned with Google's Vertex AI retry strategy:
// https://cloud.google.com/vertex-ai/generative-ai/docs/retry-strategy
//
//   - 401 (Unauthorized): Transient GKE Workload Identity token refresh.
//   - 408 (Request Timeout): Server did not receive complete request in time.
//   - 429 (Too Many Requests): Rate limit exceeded.
//   - 500 (Internal Server Error): Transient server-side failure.
//   - 502 (Bad Gateway): Upstream proxy failure.
//   - 503 (Service Unavailable): Server temporarily overloaded.
//   - 504 (Gateway Timeout): Deadline exceeded. Retryable because each attempt
//     uses an independent context with a fresh 120s timeout.
//
// NOT retryable:
//   - 499 (Client Cancelled): Gemini cancelled the operation server-side.
//   - Context errors (DeadlineExceeded, Canceled): caller's own deadline expired.
func isRetryable(err error) bool {
	apiErr, ok := errors.AsType[genai.APIError](err)
	if !ok {
		return false
	}
	switch apiErr.Code {
	case http.StatusUnauthorized, // Transient: GKE Workload Identity token refresh
		http.StatusRequestTimeout,
		http.StatusTooManyRequests,
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}
