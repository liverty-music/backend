package rdb

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.opentelemetry.io/otel/attribute"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

var _ pgx.Tx = (*TracedTx)(nil)

// TracedTx wraps pgx.Tx to create OTel spans and inject sqlcommenter traceparent
// comments for queries executed within a transaction.
type TracedTx struct {
	inner  pgx.Tx
	tracer trace.Tracer
}

// Query executes a query within the transaction, with tracing and traceparent injection.
func (t *TracedTx) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	ctx, span := t.startSpan(ctx, sql)
	defer span.End()

	rows, err := t.inner.Query(ctx, InjectTraceparent(ctx, sql), args...)
	if err != nil {
		recordError(span, err)
	}
	return rows, err
}

// QueryRow executes a query that returns at most one row within the transaction.
func (t *TracedTx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	ctx, span := t.startSpan(ctx, sql)

	row := t.inner.QueryRow(ctx, InjectTraceparent(ctx, sql), args...)
	return &tracedRow{Row: row, span: span}
}

// Exec executes a query that doesn't return rows within the transaction.
func (t *TracedTx) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	ctx, span := t.startSpan(ctx, sql)
	defer span.End()

	tag, err := t.inner.Exec(ctx, InjectTraceparent(ctx, sql), args...)
	if err != nil {
		recordError(span, err)
	}
	return tag, err
}

// Commit commits the transaction.
func (t *TracedTx) Commit(ctx context.Context) error {
	ctx, span := t.tracer.Start(ctx, "COMMIT",
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(semconv.DBSystemPostgreSQL),
	)
	defer span.End()

	err := t.inner.Commit(ctx)
	if err != nil {
		recordError(span, err)
	}
	return err
}

// Rollback rolls back the transaction.
func (t *TracedTx) Rollback(ctx context.Context) error {
	ctx, span := t.tracer.Start(ctx, "ROLLBACK",
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(semconv.DBSystemPostgreSQL),
	)
	defer span.End()

	err := t.inner.Rollback(ctx)
	if err != nil && !errors.Is(err, pgx.ErrTxClosed) {
		recordError(span, err)
	}
	return err
}

// Begin starts a pseudo nested transaction (savepoint).
func (t *TracedTx) Begin(ctx context.Context) (pgx.Tx, error) {
	tx, err := t.inner.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return &TracedTx{inner: tx, tracer: t.tracer}, nil
}

// Conn returns the underlying *pgx.Conn.
func (t *TracedTx) Conn() *pgx.Conn {
	return t.inner.Conn()
}

// CopyFrom delegates to the inner transaction.
func (t *TracedTx) CopyFrom(ctx context.Context, tableName pgx.Identifier, columnNames []string, rowSrc pgx.CopyFromSource) (int64, error) {
	return t.inner.CopyFrom(ctx, tableName, columnNames, rowSrc)
}

// SendBatch delegates to the inner transaction.
func (t *TracedTx) SendBatch(ctx context.Context, b *pgx.Batch) pgx.BatchResults {
	return t.inner.SendBatch(ctx, b)
}

// LargeObjects delegates to the inner transaction.
func (t *TracedTx) LargeObjects() pgx.LargeObjects {
	return t.inner.LargeObjects()
}

// Prepare delegates to the inner transaction.
func (t *TracedTx) Prepare(ctx context.Context, name, sql string) (*pgconn.StatementDescription, error) {
	return t.inner.Prepare(ctx, name, sql)
}

func (t *TracedTx) startSpan(ctx context.Context, sql string) (context.Context, trace.Span) {
	op := ExtractOperation(sql)
	return t.tracer.Start(ctx, op,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			semconv.DBSystemPostgreSQL,
			attribute.String("db.query.text", sql),
			attribute.String("db.operation.name", op),
		),
	)
}
