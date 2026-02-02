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
	var apiErr genai.APIError
	if errors.As(err, &apiErr) {
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
		default:
			code = codes.Unknown
		}
		return apperr.Wrap(err, code, msg, attrs...)
	}

	// Handle JSON errors (usually indicate model output mismatch or malformed response)
	var jsonSyntaxErr *json.SyntaxError
	if errors.As(err, &jsonSyntaxErr) {
		return apperr.Wrap(err, codes.Internal, msg, attrs...)
	}
	var jsonTypeErr *json.UnmarshalTypeError
	if errors.As(err, &jsonTypeErr) {
		return apperr.Wrap(err, codes.Internal, msg, attrs...)
	}

	return apperr.Wrap(err, codes.Unknown, msg, attrs...)
}
