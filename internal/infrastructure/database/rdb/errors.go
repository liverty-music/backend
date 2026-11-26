package rdb

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
)

// toAppErr converts a database error into a structured application error.
// It maps specific PostgreSQL error codes to appropriate apperr codes
// (pgErrorCodes) and a failure to reach the database to Unavailable.
func toAppErr(err error, msg string, attrs ...slog.Attr) error {
	if err == nil {
		return nil
	}

	// Handle standard pgx errors
	if errors.Is(err, pgx.ErrNoRows) {
		return apperr.Wrap(err, codes.NotFound, msg, attrs...)
	}

	// Handle PostgreSQL specific errors
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok {
		if code, known := pgErrorCodes[pgErr.Code]; known {
			return apperr.Wrap(err, code, msg, attrs...)
		}
	}

	if isConnectionFailure(err) {
		return apperr.Wrap(err, codes.Unavailable, msg, attrs...)
	}

	// Unknown errors: preserve original error type for upstream inspection
	// (e.g., context.Canceled). Error classification is the interceptor's responsibility.
	// Note: attrs are intentionally not included here — structured logging attributes
	// are only meaningful when creating an AppErr for known error types above.
	return fmt.Errorf("%s: %w", msg, err)
}

// isConnectionFailure reports whether err comes from failing to reach the
// database: the connection could not be made, or it broke while in use. A
// call its caller cancelled is not one. A query the context ended is not one
// either, although its error looks like a network timeout; a connection
// attempt that timed out is.
func isConnectionFailure(err error) bool {
	if errors.Is(err, context.Canceled) {
		return false
	}
	if _, ok := errors.AsType[*pgconn.ConnectError](err); ok {
		return true
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	// pgconn.ErrConnClosed is a pooled connection that already broke; the two
	// EOFs are one that breaks during a call.
	if errors.Is(err, pgconn.ErrConnClosed) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	_, ok := errors.AsType[net.Error](err)
	return ok
}

// pgErrorCodes maps the PostgreSQL error codes toAppErr knows to apperr codes.
var pgErrorCodes = map[string]codes.Code{
	// Constraint violations (Class 23)
	"23505": codes.AlreadyExists,      // unique_violation
	"23503": codes.FailedPrecondition, // foreign_key_violation
	"23001": codes.FailedPrecondition, // restrict_violation (ON DELETE RESTRICT on a referenced row)
	"23502": codes.InvalidArgument,    // not_null_violation
	"23514": codes.InvalidArgument,    // check_violation
	"23P01": codes.FailedPrecondition, // exclusion_violation

	// Data exceptions (Class 22)
	"22P02": codes.InvalidArgument, // invalid_text_representation
	"22001": codes.InvalidArgument, // string_data_right_truncation
	"22003": codes.InvalidArgument, // numeric_value_out_of_range
	"22007": codes.InvalidArgument, // invalid_datetime_format
	"22012": codes.InvalidArgument, // division_by_zero

	// Transaction/concurrency errors (Class 40)
	"40001": codes.Aborted, // serialization_failure
	"40P01": codes.Aborted, // deadlock_detected

	// Lock wait bounded by lock_timeout (Class 55)
	"55P03": codes.Unavailable, // lock_not_available

	// Connection errors (Class 08)
	"08000": codes.Unavailable, // connection_exception
	"08003": codes.Unavailable, // connection_does_not_exist
	"08006": codes.Unavailable, // connection_failure
	"08001": codes.Unavailable, // sqlclient_unable_to_establish_sqlconnection
	"08004": codes.Unavailable, // sqlserver_rejected_establishment_of_sqlconnection
	"08007": codes.Unavailable, // transaction_resolution_unknown
	"08P01": codes.Unavailable, // protocol_violation

	// Insufficient resources (Class 53)
	"53000": codes.Unavailable, // insufficient_resources
	"53100": codes.Unavailable, // disk_full
	"53200": codes.Unavailable, // out_of_memory
	"53300": codes.Unavailable, // too_many_connections
	"53400": codes.Unavailable, // configuration_limit_exceeded

	// Operator intervention (Class 57)
	"57000": codes.Unavailable, // operator_intervention
	"57014": codes.Unavailable, // query_canceled
	"57P01": codes.Unavailable, // admin_shutdown
	"57P02": codes.Unavailable, // crash_shutdown
	"57P03": codes.Unavailable, // cannot_connect_now
}

// IsForeignKeyViolation returns true if the error is a PostgreSQL foreign key violation.
func IsForeignKeyViolation(err error) bool {
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok {
		return pgErr.Code == "23503"
	}
	return false
}

// IsUniqueViolation returns true if the error is a PostgreSQL unique violation.
func IsUniqueViolation(err error) bool {
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok {
		return pgErr.Code == "23505"
	}
	return false
}
