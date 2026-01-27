package rdb

import (
	"errors"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
)

// toAppErr converts a database error into a structured application error.
// It maps specific PostgreSQL error codes to appropriate apperr codes.
func toAppErr(err error, msg string, attrs ...slog.Attr) error {
	if err == nil {
		return nil
	}

	// Handle standard pgx errors
	if errors.Is(err, pgx.ErrNoRows) {
		return apperr.Wrap(err, codes.NotFound, msg, attrs...)
	}

	// Handle PostgreSQL specific errors
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		// Constraint violations (Class 23)
		case "23505": // unique_violation
			return apperr.Wrap(err, codes.AlreadyExists, msg, attrs...)
		case "23503": // foreign_key_violation
			return apperr.Wrap(err, codes.FailedPrecondition, msg, attrs...)
		case "23502": // not_null_violation
			return apperr.Wrap(err, codes.InvalidArgument, msg, attrs...)
		case "23514": // check_violation
			return apperr.Wrap(err, codes.InvalidArgument, msg, attrs...)
		case "23P01": // exclusion_violation
			return apperr.Wrap(err, codes.FailedPrecondition, msg, attrs...)

		// Data exceptions (Class 22)
		case "22P02": // invalid_text_representation
			return apperr.Wrap(err, codes.InvalidArgument, msg, attrs...)
		case "22001": // string_data_right_truncation
			return apperr.Wrap(err, codes.InvalidArgument, msg, attrs...)
		case "22003": // numeric_value_out_of_range
			return apperr.Wrap(err, codes.InvalidArgument, msg, attrs...)
		case "22007": // invalid_datetime_format
			return apperr.Wrap(err, codes.InvalidArgument, msg, attrs...)
		case "22012": // division_by_zero
			return apperr.Wrap(err, codes.InvalidArgument, msg, attrs...)

		// Transaction/concurrency errors (Class 40)
		case "40001", "40P01": // serialization_failure, deadlock_detected
			return apperr.Wrap(err, codes.Aborted, msg, attrs...)

		// Connection errors (Class 08)
		case "08000", "08003", "08006", "08001", "08004", "08007", "08P01":
			// connection_exception, connection_does_not_exist, connection_failure,
			// sqlclient_unable_to_establish_sqlconnection, sqlserver_rejected_establishment_of_sqlconnection,
			// transaction_resolution_unknown, protocol_violation
			return apperr.Wrap(err, codes.Unavailable, msg, attrs...)

		// Insufficient resources (Class 53)
		case "53000", "53100", "53200", "53300", "53400":
			// insufficient_resources, disk_full, out_of_memory, too_many_connections, configuration_limit_exceeded
			return apperr.Wrap(err, codes.Unavailable, msg, attrs...)

		// Operator intervention (Class 57)
		case "57000", "57014", "57P01", "57P02", "57P03":
			// operator_intervention, query_canceled, admin_shutdown, crash_shutdown, cannot_connect_now
			return apperr.Wrap(err, codes.Unavailable, msg, attrs...)
		}
	}

	// Default to Internal error
	return apperr.Wrap(err, codes.Internal, msg, attrs...)
}

// IsForeignKeyViolation returns true if the error is a PostgreSQL foreign key violation.
func IsForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23503"
	}
	return false
}

// IsUniqueViolation returns true if the error is a PostgreSQL unique violation.
func IsUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23505"
	}
	return false
}
