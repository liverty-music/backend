package api

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

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

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}

	code := codes.Internal
	switch resp.StatusCode {
	case http.StatusBadRequest:
		code = codes.InvalidArgument
	case http.StatusUnauthorized:
		code = codes.Unauthenticated
	case http.StatusForbidden:
		code = codes.PermissionDenied
	case http.StatusNotFound:
		code = codes.NotFound
	case http.StatusConflict:
		code = codes.AlreadyExists
	case http.StatusTooManyRequests:
		code = codes.ResourceExhausted
	case http.StatusServiceUnavailable:
		code = codes.ResourceExhausted // Mapped to ResourceExhausted for rate limiting context
	case http.StatusGatewayTimeout:
		code = codes.DeadlineExceeded
	default:
		if resp.StatusCode >= 500 {
			code = codes.Unavailable
		} else if resp.StatusCode >= 400 {
			code = codes.InvalidArgument
		}
	}

	return apperr.New(code, fmt.Sprintf("%s: api returned status %d", msg, resp.StatusCode), attrs...)
}
