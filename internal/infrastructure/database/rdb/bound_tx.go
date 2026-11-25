package rdb

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// boundTxKey is the context key of the transaction that [TracedPool] runs
// queries on instead of taking a connection of its own.
type boundTxKey struct{}

// withBoundTx returns a context whose [TracedPool] Query, QueryRow and Exec
// run on tx, so work that has to run while tx holds a lock needs no second
// connection. The queries run one at a time: a transaction's connection is not
// safe for concurrent use, so a goroutine started under this context must not
// query. Begin is not affected: a transaction begun under it still takes a
// connection of its own.
func withBoundTx(ctx context.Context, tx pgx.Tx) context.Context {
	return context.WithValue(ctx, boundTxKey{}, tx)
}

// boundTx returns the transaction ctx is bound to, if any.
func boundTx(ctx context.Context) (pgx.Tx, bool) {
	tx, ok := ctx.Value(boundTxKey{}).(pgx.Tx)
	return tx, ok
}
