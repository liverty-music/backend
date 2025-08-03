package apperr

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	"github.com/liverty-music/backend/pkg/apperr/codes"
	"github.com/liverty-music/backend/pkg/logging"
)

// NewInterceptor creates a Connect interceptor that handles AppErr conversion and logging.
// It converts AppErr instances to appropriate Connect errors and logs server errors.
// Client errors (4xx status codes) are not logged, while server errors (5xx) are logged.
func NewInterceptor(logger *logging.Logger) connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			resp, err := next(ctx, req)
			if err != nil {
				return resp, handleError(ctx, err, logger)
			}
			return resp, nil
		}
	}
}

// handleError converts AppErr to Connect error and logs server errors.
func handleError(ctx context.Context, err error, logger *logging.Logger) error {
	if err == nil {
		return nil
	}

	var appErr *AppErr
	if !errors.As(err, &appErr) {
		// For non-AppErr errors, treat as unknown error
		logger.Error(ctx, "Unhandled error occurred", err)
		return connect.NewError(connect.CodeUnknown, err)
	}

	// Check if this is a client error (4xx) or server error (5xx)
	if IsServerError(appErr.Code) {
		// Log server errors with full context
		logger.Error(ctx, "Server error occurred", appErr)
	}

	// Convert AppErr to Connect error
	connectErr := connect.NewError(appErr.Code, appErr)

	// Add structured attributes as error details if available
	// Convert slog.Attr to Connect error details
	// Note: Connect error details are limited, so we'll include key attributes in the error message
	for _, attr := range appErr.Attrs {
		if attr.Key != "stacktrace" { // Skip stack trace in client-facing errors
			connectErr.Meta().Set(attr.Key, attr.Value.String())
		}
	}

	return connectErr
}

// IsServerError determines if a status code represents a server error (5xx).
// Client errors (4xx) are not logged, while server errors (5xx) are logged.
func IsServerError(code codes.Code) bool {
	switch code {
	case codes.Internal,
		codes.Unknown,
		codes.DataLoss,
		codes.Unavailable,
		codes.Unimplemented:
		return true
	case codes.InvalidArgument,
		codes.NotFound,
		codes.AlreadyExists,
		codes.PermissionDenied,
		codes.FailedPrecondition,
		codes.OutOfRange,
		codes.Unauthenticated,
		codes.Canceled,
		codes.DeadlineExceeded,
		codes.Aborted,
		codes.ResourceExhausted:
		return false
	default:
		// Default to server error for unknown codes
		return true
	}
}
