// Package rdb provides database infrastructure including traced pool wrappers.
package rdb

import (
	"context"
	"fmt"
	"runtime"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

const tracerName = "github.com/liverty-music/backend/internal/infrastructure/database/rdb"

// TracedPool wraps *pgxpool.Pool to transparently create OTel spans and inject
// sqlcommenter traceparent comments into every SQL query.
type TracedPool struct {
	inner  *pgxpool.Pool
	tracer trace.Tracer
}

// NewTracedPool creates a TracedPool wrapping the given pool.
func NewTracedPool(pool *pgxpool.Pool) *TracedPool {
	return &TracedPool{
		inner:  pool,
		tracer: otel.Tracer(tracerName),
	}
}

// Query executes a query that returns rows, with tracing and traceparent injection.
// The span is ended when the returned Rows is closed, covering the full row iteration.
func (tp *TracedPool) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	ctx, span := tp.startSpan(ctx, sql)

	rows, err := tp.inner.Query(ctx, InjectTraceparent(ctx, sql), args...)
	if err != nil {
		recordError(span, err)
		span.End()
		return nil, err
	}
	return &tracedRows{Rows: rows, span: span}, nil
}

// QueryRow executes a query that returns at most one row, with tracing and traceparent injection.
// A runtime finalizer ensures the span is eventually ended even if Scan is never called.
func (tp *TracedPool) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	ctx, span := tp.startSpan(ctx, sql)

	row := tp.inner.QueryRow(ctx, InjectTraceparent(ctx, sql), args...)
	r := &tracedRow{Row: row, span: span}
	runtime.SetFinalizer(r, func(r *tracedRow) { r.span.End() })
	return r
}

// Exec executes a query that doesn't return rows, with tracing and traceparent injection.
func (tp *TracedPool) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	ctx, span := tp.startSpan(ctx, sql)
	defer span.End()

	tag, err := tp.inner.Exec(ctx, InjectTraceparent(ctx, sql), args...)
	if err != nil {
		recordError(span, err)
	}
	return tag, err
}

// Begin starts a transaction and returns a TracedTx that applies tracing to queries within it.
func (tp *TracedPool) Begin(ctx context.Context) (pgx.Tx, error) {
	ctx, span := tp.tracer.Start(ctx, "BEGIN",
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(semconv.DBSystemPostgreSQL),
	)
	defer span.End()

	tx, err := tp.inner.Begin(ctx)
	if err != nil {
		recordError(span, err)
		return nil, err
	}
	return &TracedTx{inner: tx, tracer: tp.tracer}, nil
}

// Ping delegates to the inner pool.
func (tp *TracedPool) Ping(ctx context.Context) error {
	return tp.inner.Ping(ctx)
}

// Close delegates to the inner pool.
func (tp *TracedPool) Close() {
	tp.inner.Close()
}

func (tp *TracedPool) startSpan(ctx context.Context, sql string) (context.Context, trace.Span) {
	op := ExtractOperation(sql)
	return tp.tracer.Start(ctx, op,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			semconv.DBSystemPostgreSQL,
			attribute.String("db.query.text", sql),
			attribute.String("db.operation.name", op),
		),
	)
}

// tracedRow wraps pgx.Row to end the span after Scan completes.
type tracedRow struct {
	pgx.Row
	span trace.Span
}

// Scan delegates to the inner Row and ends the span.
func (r *tracedRow) Scan(dest ...any) error {
	err := r.Row.Scan(dest...)
	if err != nil {
		recordError(r.span, err)
	}
	r.span.End()
	runtime.KeepAlive(r)
	return err
}

// tracedRows wraps pgx.Rows to end the span when the rows are closed,
// ensuring the span covers the full row iteration lifecycle.
type tracedRows struct {
	pgx.Rows
	span trace.Span
}

// Close closes the underlying Rows and ends the span.
func (r *tracedRows) Close() {
	r.Rows.Close()
	r.span.End()
}

func recordError(span trace.Span, err error) {
	span.RecordError(err)
	span.SetStatus(codes.Error, err.Error())
}

// InjectTraceparent prepends a sqlcommenter traceparent comment to the SQL if
// there is a valid span in the context.
func InjectTraceparent(ctx context.Context, sql string) string {
	sc := trace.SpanFromContext(ctx).SpanContext()
	if !sc.IsValid() {
		return sql
	}
	return fmt.Sprintf("/*traceparent='00-%s-%s-%s'*/ %s",
		sc.TraceID().String(),
		sc.SpanID().String(),
		sc.TraceFlags().String(),
		sql,
	)
}

// ExtractOperation returns the SQL operation name from the first keyword of the query.
func ExtractOperation(sql string) string {
	s := strings.TrimSpace(sql)
	if i := strings.IndexAny(s, " \t\n\r"); i > 0 {
		op := strings.ToUpper(s[:i])
		switch op {
		case "SELECT", "INSERT", "UPDATE", "DELETE", "WITH", "TRUNCATE":
			return op
		}
	}
	return "DB"
}
