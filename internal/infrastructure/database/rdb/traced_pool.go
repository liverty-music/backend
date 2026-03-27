// Package rdb provides database infrastructure including traced pool wrappers.
package rdb

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

const tracerName = "github.com/liverty-music/backend/internal/infrastructure/database/rdb"

// TracedPool wraps *pgxpool.Pool to transparently create OTel spans and inject
// sqlcommenter traceparent comments into every SQL query.
type TracedPool struct {
	inner         *pgxpool.Pool
	tracer        trace.Tracer
	dbNamespace   string
	serverAddress string
}

// NewTracedPool creates a TracedPool wrapping the given pool.
// dbNamespace is the database name and serverAddress is the database host.
func NewTracedPool(pool *pgxpool.Pool, dbNamespace, serverAddress string) *TracedPool {
	tp := &TracedPool{
		inner:         pool,
		tracer:        otel.Tracer(tracerName),
		dbNamespace:   dbNamespace,
		serverAddress: serverAddress,
	}
	tp.registerPoolMetrics(pool)
	return tp
}

func (tp *TracedPool) registerPoolMetrics(pool *pgxpool.Pool) {
	meter := otel.Meter(tracerName)
	_, _ = meter.Int64ObservableGauge("db.pool.active_connections",
		metric.WithDescription("Number of active (acquired) database connections"),
		metric.WithInt64Callback(func(_ context.Context, o metric.Int64Observer) error {
			o.Observe(int64(pool.Stat().AcquiredConns()))
			return nil
		}),
	)
	_, _ = meter.Int64ObservableGauge("db.pool.idle_connections",
		metric.WithDescription("Number of idle database connections"),
		metric.WithInt64Callback(func(_ context.Context, o metric.Int64Observer) error {
			o.Observe(int64(pool.Stat().IdleConns()))
			return nil
		}),
	)
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
	return &TracedTx{inner: tx, tracer: tp.tracer, dbNamespace: tp.dbNamespace, serverAddress: tp.serverAddress}, nil
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
	meta := ExtractQueryMeta(sql)
	attrs := []attribute.KeyValue{
		semconv.DBSystemPostgreSQL,
		attribute.String("db.query.text", sql),
		attribute.String("db.operation.name", meta.Operation),
	}
	if meta.Table != "" {
		attrs = append(attrs, attribute.String("db.collection.name", meta.Table))
	}
	if tp.dbNamespace != "" {
		attrs = append(attrs, attribute.String("db.namespace", tp.dbNamespace))
	}
	if tp.serverAddress != "" {
		attrs = append(attrs, attribute.String("server.address", tp.serverAddress))
	}
	return tp.tracer.Start(ctx, meta.Operation,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(attrs...),
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
	runtime.SetFinalizer(r, nil)
	r.span.End()
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

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		span.SetAttributes(attribute.String("db.response.status_code", pgErr.Code))
	}
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

// QueryMeta holds the extracted operation and table name from a SQL query.
type QueryMeta struct {
	Operation string
	Table     string
}

// ExtractQueryMeta returns the SQL operation name and primary table name from a query.
// Table extraction uses simple pattern matching for common SQL forms; complex queries
// (CTEs, subqueries) may not match, in which case Table is empty.
func ExtractQueryMeta(sql string) QueryMeta {
	s := strings.TrimSpace(sql)
	i := strings.IndexAny(s, " \t\n\r")
	if i <= 0 {
		return QueryMeta{Operation: "DB"}
	}

	op := strings.ToUpper(s[:i])
	switch op {
	case "SELECT", "INSERT", "UPDATE", "DELETE", "WITH", "TRUNCATE":
	default:
		return QueryMeta{Operation: "DB"}
	}

	table := extractTable(op, s)
	return QueryMeta{Operation: op, Table: table}
}

// ExtractOperation returns the SQL operation name from the first keyword of the query.
// Deprecated: Use ExtractQueryMeta for both operation and table name.
func ExtractOperation(sql string) string {
	return ExtractQueryMeta(sql).Operation
}

// extractTable extracts the primary table name from SQL based on the operation.
func extractTable(op, sql string) string {
	upper := strings.ToUpper(sql)

	var keyword string
	switch op {
	case "SELECT", "DELETE":
		keyword = "FROM"
	case "INSERT":
		keyword = "INTO"
	case "UPDATE":
		keyword = "UPDATE"
	default:
		return ""
	}

	idx := findKeyword(upper, keyword)
	if idx < 0 {
		return ""
	}

	rest := strings.TrimSpace(sql[idx+len(keyword):])
	// Take the first word (table name), stripping any schema prefix or quotes.
	end := strings.IndexAny(rest, " \t\n\r(,;")
	if end < 0 {
		end = len(rest)
	}
	if end == 0 {
		return ""
	}

	table := rest[:end]
	// Strip schema prefix (e.g., "app.concerts" → "concerts").
	if dot := strings.LastIndex(table, "."); dot >= 0 {
		table = table[dot+1:]
	}
	return strings.Trim(table, "\"")
}

// findKeyword returns the index of keyword in upper-cased SQL, ensuring it appears
// at a word boundary (preceded by whitespace or start-of-string). This prevents
// matching column names like "date_from" when searching for "FROM".
func findKeyword(upper, keyword string) int {
	start := 0
	for {
		idx := strings.Index(upper[start:], keyword)
		if idx < 0 {
			return -1
		}
		abs := start + idx
		if abs == 0 || upper[abs-1] == ' ' || upper[abs-1] == '\t' || upper[abs-1] == '\n' || upper[abs-1] == '\r' {
			return abs
		}
		start = abs + len(keyword)
	}
}
