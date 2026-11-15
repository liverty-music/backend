package rdb

import (
	"context"
	"log/slog"

	"github.com/jackc/pgx/v5"
)

// boundEntryCreation sets, for the rest of tx, the lock and statement timeouts
// of an entry creation, so a creation that waits on a lock fails with
// Unavailable (see toAppErr) instead of holding the connection.
func boundEntryCreation(ctx context.Context, tx pgx.Tx, timeout, msg string, attrs []slog.Attr) error {
	for _, setting := range []string{"lock_timeout", "statement_timeout"} {
		if _, err := tx.Exec(ctx, "SET LOCAL "+setting+" = '"+timeout+"'"); err != nil {
			return toAppErr(err, msg, attrs...)
		}
	}
	return nil
}
