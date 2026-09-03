package api

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/liverty-music/backend/pkg/httpx"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
)

// FromHTTP converts a network error or an HTTP response into a structured application error.
// It maps standard HTTP status codes and context errors to appropriate apperr codes.
//
// If err is not nil, it checks for context cancellation or timeout.
// If resp is not nil and has a non-2xx status code, it maps the code to an apperr.
// If both are nil or resp is 2xx, it returns nil.
func FromHTTP(err error, resp *http.Response, msg string, attrs ...slog.Attr) error {
	if err != nil {
		// Narrow down context errors
		switch err {
		case context.Canceled:
			return apperr.Wrap(err, codes.Canceled, msg, attrs...)
		case context.DeadlineExceeded:
			return apperr.Wrap(err, codes.DeadlineExceeded, msg, attrs...)
		}
		return apperr.Wrap(err, codes.Unavailable, msg, attrs...)
	}

	if resp == nil {
		return nil
	}

	// Capture the response body for diagnostics on error statuses before
	// delegating the status→code decision to the shared policy.
	if resp.StatusCode >= 400 && resp.Body != nil {
		if body := httpx.CaptureResponseBody(resp.Body); body != "" {
			attrs = append(attrs, slog.String("responseBody", body))
		}
	}

	return FromStatus(resp.StatusCode, nil, msg, attrs...)
}

// FromStatus maps an HTTP-style status code to an apperr code, wrapping cause
// (or creating a fresh error when cause is nil). It returns nil for 2xx codes.
//
// This is the shared status→code entry point for callers that surface a status
// code WITHOUT a *http.Response — SDK-based clients such as Stripe
// (*stripe.Error.HTTPStatusCode) or Pocket Sign — so raw-HTTP and SDK callers
// classify identically instead of each hand-rolling a (divergent) mapping.
// FromHTTP delegates here after capturing the response body.
func FromStatus(statusCode int, cause error, msg string, attrs ...slog.Attr) error {
	if statusCode >= 200 && statusCode < 300 {
		return nil
	}

	code := statusToCode(statusCode)
	wrapped := fmt.Sprintf("%s: api returned status %d", msg, statusCode)
	if cause != nil {
		return apperr.Wrap(cause, code, wrapped, attrs...)
	}
	return apperr.New(code, wrapped, attrs...)
}

// statusToCode is the single source of truth for the HTTP-status → apperr-code
// policy. Both FromHTTP and FromStatus consult it, so the mapping cannot drift
// between raw-HTTP and SDK-based callers. Unmapped 5xx → Unavailable, unmapped
// 4xx → InvalidArgument, anything else (incl. a missing/zero status) → Internal.
func statusToCode(statusCode int) codes.Code {
	switch statusCode {
	case http.StatusBadRequest:
		return codes.InvalidArgument
	case http.StatusUnauthorized:
		return codes.Unauthenticated
	case http.StatusForbidden:
		return codes.PermissionDenied
	case http.StatusNotFound:
		return codes.NotFound
	case http.StatusConflict:
		return codes.AlreadyExists
	case http.StatusTooManyRequests:
		return codes.ResourceExhausted
	case http.StatusServiceUnavailable:
		return codes.ResourceExhausted // Mapped to ResourceExhausted for rate limiting context
	case http.StatusGatewayTimeout:
		return codes.DeadlineExceeded
	default:
		switch {
		case statusCode >= 500:
			return codes.Unavailable
		case statusCode >= 400:
			return codes.InvalidArgument
		default:
			return codes.Internal
		}
	}
}
