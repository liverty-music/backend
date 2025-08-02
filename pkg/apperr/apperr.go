// Package apperr provides structured error handling with status code compatibility.
// It enables consistent error management across gRPC/Connect-RPC services with automatic
// stack trace capture, structured logging integration, and semantic error comparison.
//
// # Overview
//
// AppErr is the main error type that wraps errors with additional context including:
//   - Status codes from pkg/apperr/codes for API compatibility
//   - Structured logging attributes
//   - Automatic stack trace capture
//   - Error chain unwrapping support
//
// # Basic Usage
//
// Create new errors with status codes:
//
//	// Create a new error
//	err := apperr.New(codes.InvalidArgument, "user ID cannot be empty")
//
//	// Wrap an existing error
//	err = apperr.Wrap(dbErr, codes.Internal, "failed to get user",
//		slog.String("user_id", userID))
//
// # Error Comparison
//
// Use predefined error variables for semantic comparison:
//
//	if errors.Is(err, apperr.ErrNotFound) {
//		// Handle not found error
//	}
//
//	if errors.Is(err, apperr.ErrInvalidArgument) {
//		// Handle invalid argument error
//	}
//
// # Structured Logging
//
// AppErr implements slog.LogValuer for structured logging:
//
//	logger.Error("operation failed", slog.Any("error", err))
//	// Logs: {"msg": "operation failed", "error": {"msg": "...", "code": "...", "cause": "..."}}
//
// # Error Chain Unwrapping
//
// AppErr supports standard error unwrapping:
//
//	var appErr *apperr.AppErr
//	if errors.As(err, &appErr) {
//		fmt.Printf("Status code: %v\n", appErr.Code)
//	}
//
//	// Unwrap to get the original cause
//	originalErr := errors.Unwrap(err)
//
// # Predefined Error Variables
//
// The package provides predefined error variables for all status codes:
//   - ErrCanceled, ErrUnknown, ErrInvalidArgument
//   - ErrDeadlineExceeded, ErrNotFound, ErrAlreadyExists
//   - ErrPermissionDenied, ErrResourceExhausted, ErrFailedPrecondition
//   - ErrAborted, ErrOutOfRange, ErrUnimplemented
//   - ErrInternal, ErrUnavailable, ErrDataLoss, ErrUnauthenticated
//
// These variables can be used directly or as targets for errors.Is comparisons.
package apperr

import (
	"errors"
	"fmt"
	"log/slog"
	"runtime"
	"strings"

	"github.com/pannpers/go-backend-scaffold/pkg/apperr/codes"
)

// AppErr represents an application error with status code compatibility.
// It provides structured error handling with automatic stack trace capture,
// status code mapping, and structured logging support.
// AppErr implements the error interface and can be used with the standard
// errors package functions like errors.Is and errors.As.
type AppErr struct {
	Cause error       // Original error that caused this AppErr (if any)
	Code  codes.Code  // Status code representing the error type
	Msg   string      // Human-readable error message
	Attrs []slog.Attr // Structured attributes for logging context
}

// Global error variables provide predefined AppErr instances for common status codes.
// These can be used directly or as targets for errors.Is comparisons.
var (
	// ErrCanceled represents a canceled operation.
	ErrCanceled = &AppErr{Code: codes.Canceled}

	// ErrUnknown represents an unknown error.
	ErrUnknown = &AppErr{Code: codes.Unknown}

	// ErrInvalidArgument represents an invalid argument error.
	ErrInvalidArgument = &AppErr{Code: codes.InvalidArgument}

	// ErrDeadlineExceeded represents a deadline exceeded error.
	ErrDeadlineExceeded = &AppErr{Code: codes.DeadlineExceeded}

	// ErrNotFound represents a not found error.
	ErrNotFound = &AppErr{Code: codes.NotFound}

	// ErrAlreadyExists represents an already exists error.
	ErrAlreadyExists = &AppErr{Code: codes.AlreadyExists}

	// ErrPermissionDenied represents a permission denied error.
	ErrPermissionDenied = &AppErr{Code: codes.PermissionDenied}

	// ErrResourceExhausted represents a resource exhausted error.
	ErrResourceExhausted = &AppErr{Code: codes.ResourceExhausted}

	// ErrFailedPrecondition represents a failed precondition error.
	ErrFailedPrecondition = &AppErr{Code: codes.FailedPrecondition}

	// ErrAborted represents an aborted operation error.
	ErrAborted = &AppErr{Code: codes.Aborted}

	// ErrOutOfRange represents an out of range error.
	ErrOutOfRange = &AppErr{Code: codes.OutOfRange}

	// ErrUnimplemented represents an unimplemented operation error.
	ErrUnimplemented = &AppErr{Code: codes.Unimplemented}

	// ErrInternal represents an internal server error.
	ErrInternal = &AppErr{Code: codes.Internal}

	// ErrUnavailable represents a service unavailable error.
	ErrUnavailable = &AppErr{Code: codes.Unavailable}

	// ErrDataLoss represents a data loss error.
	ErrDataLoss = &AppErr{Code: codes.DataLoss}

	// ErrUnauthenticated represents an unauthenticated request error.
	ErrUnauthenticated = &AppErr{Code: codes.Unauthenticated}
)

// Error implements the error interface.
// Returns the formatted error message including the status code.
func (e *AppErr) Error() string {
	return e.Msg
}

// Unwrap returns the underlying cause error, if any.
// This enables compatibility with the standard errors.Unwrap function.
func (e *AppErr) Unwrap() error {
	return e.Cause
}

// Is enables error checking with errors.Is.
// Returns true if the target is an AppErr with the same Code, or if the Cause field matches the target.
// This allows semantic error comparison based on error codes rather than exact instance matching.
func (e *AppErr) Is(target error) bool {
	if target == nil {
		return false
	}

	if t, ok := target.(*AppErr); ok {
		// Compare by Code for semantic equivalence
		return e.Code == t.Code
	}

	return errors.Is(e.Cause, target)
}

// LogValue implements slog.LogValuer, allowing AppErr to be logged as structured attributes.
// When used with slog, this will output all error context as structured fields including
// message, code, cause, and any additional attributes.
func (e *AppErr) LogValue() slog.Value {
	attrs := []slog.Attr{
		slog.String("msg", e.Msg),
		slog.String("code", e.Code.String()),
	}
	if e.Cause != nil {
		attrs = append(attrs, slog.String("cause", e.Cause.Error()))
	}

	anyAttrs := make([]any, len(e.Attrs))
	for i, attr := range e.Attrs {
		anyAttrs[i] = attr
	}

	attrs = append(attrs, slog.Group("attrs", anyAttrs...))

	return slog.GroupValue(attrs...)
}

// New creates a new AppErr instance without a cause error.
// The message is automatically formatted to include the status code.
// A stack trace is automatically captured and included in the attributes.
// Use this when there is no underlying error to wrap.
//
// Example:
//
//	err := apperr.New(codes.InvalidArgument, "user ID cannot be empty")
//	// Returns: "user ID cannot be empty (InvalidArgument)"
//
//	// With structured logging attributes
//	err = apperr.New(codes.NotFound, "user not found",
//		slog.String("user_id", "123"),
//		slog.String("operation", "GetUser"))
func New(code codes.Code, msg string, attrs ...slog.Attr) error {
	attrs = append(attrs, withStack())

	return &AppErr{
		Code:  code,
		Msg:   fmt.Sprintf("%s (%s)", msg, code),
		Attrs: attrs,
	}
}

// Wrap wraps an existing error with additional context and status code.
// If the error is already an AppErr, it will be flattened and the messages will be concatenated.
//
// Note: When wrapping an existing AppErr, its original Code field will be overridden by the given code.
// A stack trace is automatically captured and included in the attributes.
// Use this to wrap existing errors with additional context and status code.
//
// Example:
//
//	// Wrap a database error
//	err := apperr.Wrap(dbErr, codes.Internal, "failed to get user",
//		slog.String("user_id", userID))
//
//	// Wrap an existing AppErr (flattens the chain)
//	err = apperr.Wrap(appErr, codes.NotFound, "user lookup failed")
//	// Result: "user lookup failed (NotFound): original message"
func Wrap(err error, code codes.Code, msg string, attrs ...slog.Attr) error {
	attrs = append(attrs, withStack())

	// If err is already an AppErr, flatten the chain
	var appErr *AppErr
	if !errors.As(err, &appErr) {
		// Original behavior for non-AppErr errors
		return &AppErr{
			Cause: err,
			Code:  code,
			Msg:   fmt.Sprintf("%s: %s (%s)", msg, err.Error(), code),
			Attrs: attrs,
		}
	}

	// Concatenate messages: new message + old AppErr's message
	combinedMsg := fmt.Sprintf("%s (%s): %s", msg, code, appErr.Msg)

	// Merge attributes, keeping original stack trace and filtering duplicates from new attrs
	var mergedAttrs []slog.Attr
	mergedAttrs = append(mergedAttrs, appErr.Attrs...)

	// Add new attributes, but skip stack traces to avoid duplication
	for _, attr := range attrs {
		if attr.Key != "stacktrace" {
			mergedAttrs = append(mergedAttrs, attr)
		}
	}

	cause := appErr.Cause
	if cause == nil {
		cause = appErr
	}

	return &AppErr{
		Cause: cause,       // Keep the original cause
		Code:  code,        // Use new code
		Msg:   combinedMsg, // Concatenated message
		Attrs: mergedAttrs, // Merge attributes (keeping original stack trace)
	}
}

const callStackSkip = 3

// withStack captures the current stack trace and returns it as a slog attribute.
// This is used internally by New and Wrap to automatically include stack traces.
// The stack trace excludes the withStack function itself and the immediate caller (New/Wrap).
func withStack() slog.Attr {
	var pcs [32]uintptr

	n := runtime.Callers(callStackSkip, pcs[:]) // Skip withStack and New/Wrap
	if n == 0 {
		return slog.String("stacktrace", "unknown")
	}

	var sb strings.Builder

	frames := runtime.CallersFrames(pcs[:n])

	for {
		frame, more := frames.Next()
		sb.WriteString(fmt.Sprintf("%s\n\t%s:%d\n", frame.Function, frame.File, frame.Line))

		if !more {
			break
		}
	}

	return slog.String("stacktrace", sb.String())
}
