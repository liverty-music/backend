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

	// An error this package created with its code (an empty or broken
	// response, a refused fetch) keeps that code.
	if ae, ok := errors.AsType[*apperr.AppErr](err); ok {
		return apperr.Wrap(err, ae.Code, msg, attrs...)
	}

	// Handle Gemini API errors
	if apiErr, ok := errors.AsType[genai.APIError](err); ok {
		return apperr.Wrap(err, apiErrorCode(apiErr.Code), msg, attrs...)
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

// apiErrorCodes maps the HTTP status of a Gemini API error to an apperr
// code; any other status is Unknown.
var apiErrorCodes = map[int]codes.Code{
	http.StatusBadRequest:   codes.InvalidArgument,
	http.StatusUnauthorized: codes.Unauthenticated,
	// Depleted prepaid credits.
	http.StatusPaymentRequired: codes.FailedPrecondition,
	// A denial, not missing credentials (AIP-193), such as a spent project
	// spend cap ("Spend cap breached", PERMISSION_DENIED).
	http.StatusForbidden:          codes.PermissionDenied,
	http.StatusNotFound:           codes.NotFound,
	http.StatusTooManyRequests:    codes.ResourceExhausted,
	http.StatusServiceUnavailable: codes.Unavailable,
	http.StatusGatewayTimeout:     codes.DeadlineExceeded,
	// Client Closed Request (Nginx-origin; Gemini uses it for server-side
	// cancellation).
	499: codes.Canceled,
}

// apiErrorCode returns the apperr code of a Gemini API error's HTTP status.
func apiErrorCode(status int) codes.Code {
	if code, ok := apiErrorCodes[status]; ok {
		return code
	}
	return codes.Unknown
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
//     uses an independent context with a fresh budget.
//
// NOT retryable:
//   - 499 (Client Cancelled): Gemini cancelled the operation server-side.
//   - Context errors (DeadlineExceeded, Canceled). The concert searcher
//     counts a request whose own budget passed as transient itself.
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
