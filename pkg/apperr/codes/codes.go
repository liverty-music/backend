// Package codes provides status codes compatible with gRPC and Connect-RPC protocols.
// These codes are used throughout the application for consistent error handling and
// API compatibility across different transport protocols.
//
// # Overview
//
// The codes are based on the gRPC status code specification and are compatible with
// both gRPC and Connect-RPC protocols. They provide semantic meaning to errors and
// enable consistent error handling across different layers of the application.
//
// # Usage
//
// Use these codes when creating AppErr instances:
//
//	err := apperr.New(codes.InvalidArgument, "user ID cannot be empty")
//
//	err = apperr.Wrap(dbErr, codes.Internal, "failed to get user")
//
// # Code Categories
//
// Client errors (4xx equivalent):
//   - InvalidArgument: Invalid request parameters
//   - NotFound: Requested resource not found
//   - AlreadyExists: Resource already exists
//   - PermissionDenied: Insufficient permissions
//   - Unauthenticated: Invalid or missing authentication
//   - FailedPrecondition: Operation cannot be performed in current state
//   - OutOfRange: Operation attempted past valid range
//
// Server errors (5xx equivalent):
//   - Internal: Internal server error
//   - Unavailable: Service temporarily unavailable
//   - DataLoss: Unrecoverable data loss
//   - ResourceExhausted: Resources exhausted
//   - Unimplemented: Operation not implemented
//
// Operational errors:
//   - Canceled: Operation was canceled
//   - DeadlineExceeded: Operation timed out
//   - Aborted: Operation was aborted
//   - Unknown: Unknown error
//
// # Protocol Compatibility
//
// These codes map directly to:
//   - gRPC status codes (google.golang.org/grpc/codes)
//   - Connect-RPC codes (connectrpc.com/connect)
//   - HTTP status codes (approximate mapping)
//
// This ensures consistent error handling regardless of the transport protocol used.
package codes

import (
	"connectrpc.com/connect"
)

// Code is a status code defined according to the [gRPC documentation].
// It provides semantic meaning to errors and enables consistent error handling
// across different transport protocols (gRPC, Connect-RPC, HTTP).
//
// [gRPC documentation]: https://github.com/grpc/grpc/blob/master/doc/statuscodes.md
type Code = connect.Code

const (
	// Canceled indicates the operation was canceled (typically by the caller).
	//
	// The gRPC framework will generate this error code when cancellation
	// is requested.
	Canceled = connect.CodeCanceled

	// Unknown error. An example of where this error may be returned is
	// if a Status value received from another address space belongs to
	// an error-space that is not known in this address space. Also
	// errors raised by APIs that do not return enough error information
	// may be converted to this error.
	//
	// The gRPC framework will generate this error code in the above two
	// mentioned cases.
	Unknown = connect.CodeUnknown

	// InvalidArgument indicates client specified an invalid argument.
	// Note that this differs from FailedPrecondition. It indicates arguments
	// that are problematic regardless of the state of the system
	// (e.g., a malformed file name).
	InvalidArgument = connect.CodeInvalidArgument

	// DeadlineExceeded means operation expired before completion.
	//
	// For operations and APIs that change the state of the system,
	// this error may be returned even if the operation has completed
	// successfully. For example, a successful response from a server
	// could have been delayed long enough for the deadline to expire.
	DeadlineExceeded = connect.CodeDeadlineExceeded

	// NotFound means some requested entity (e.g., a file or directory) was
	// not found.
	NotFound = connect.CodeNotFound

	// AlreadyExists means an attempt to create an entity failed because one
	// already exists.
	AlreadyExists = connect.CodeAlreadyExists

	// PermissionDenied indicates the caller does not have permission to
	// execute the specified operation. It must not be used for rejections
	// caused by exhausting some resource (use ResourceExhausted instead for that
	// purpose).
	PermissionDenied = connect.CodePermissionDenied

	// ResourceExhausted indicates the operation is out of resource.
	// This should only be returned if there is no other way to interpret
	// the error.
	ResourceExhausted = connect.CodeResourceExhausted

	// FailedPrecondition indicates the operation was rejected because the
	// system is not in a state required for the operation's execution.
	FailedPrecondition = connect.CodeFailedPrecondition

	// Aborted indicates the operation was aborted, typically due to a
	// concurrency issue like sequencer check failures, transaction aborts, etc.
	Aborted = connect.CodeAborted

	// OutOfRange indicates operation was attempted past the valid range.
	// E.g. seeking or reading past end of file.
	OutOfRange = connect.CodeOutOfRange

	// Unimplemented indicates operation is not implemented or not
	// supported/enabled in this service.
	Unimplemented = connect.CodeUnimplemented

	// Internal errors. This means that this error should be considered
	// as an internal error that should not happen.
	Internal = connect.CodeInternal

	// Unavailable indicates the service is currently unavailable.
	// This is a most likely a transient condition and may be corrected
	// by retrying with a backoff.
	Unavailable = connect.CodeUnavailable

	// DataLoss indicates unrecoverable data loss or corruption.
	// This should only be returned if there is no other way to interpret
	// the error.
	DataLoss = connect.CodeDataLoss

	// Unauthenticated indicates the request does not have valid
	// authentication credentials for the operation.
	Unauthenticated = connect.CodeUnauthenticated
)
