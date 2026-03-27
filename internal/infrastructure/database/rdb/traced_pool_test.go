package rdb_test

import (
	"context"
	"testing"

	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

func TestInjectTraceparent(t *testing.T) {
	t.Parallel()

	t.Run("with valid span context", func(t *testing.T) {
		t.Parallel()

		exporter := tracetest.NewInMemoryExporter()
		tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
		tracer := tp.Tracer("test")

		ctx, span := tracer.Start(context.Background(), "test-span")
		defer span.End()

		sc := span.SpanContext()
		sql := "SELECT * FROM users"
		got := rdb.InjectTraceparent(ctx, sql)

		want := "/*traceparent='00-" + sc.TraceID().String() + "-" + sc.SpanID().String() + "-" + sc.TraceFlags().String() + "'*/ " + sql
		if got != want {
			t.Errorf("InjectTraceparent() =\n  %q\nwant:\n  %q", got, want)
		}
	})

	t.Run("without span in context", func(t *testing.T) {
		t.Parallel()

		sql := "SELECT * FROM users"
		got := rdb.InjectTraceparent(context.Background(), sql)
		if got != sql {
			t.Errorf("InjectTraceparent() = %q, want unmodified %q", got, sql)
		}
	})

	t.Run("with invalid span context", func(t *testing.T) {
		t.Parallel()

		ctx := trace.ContextWithSpanContext(context.Background(), trace.SpanContext{})
		sql := "INSERT INTO users (id) VALUES ($1)"
		got := rdb.InjectTraceparent(ctx, sql)
		if got != sql {
			t.Errorf("InjectTraceparent() = %q, want unmodified %q", got, sql)
		}
	})

	t.Run("format matches sqlcommenter spec", func(t *testing.T) {
		t.Parallel()

		exporter := tracetest.NewInMemoryExporter()
		tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
		tracer := tp.Tracer("test")

		ctx, span := tracer.Start(context.Background(), "test-span")
		defer span.End()

		sql := "DELETE FROM sessions"
		got := rdb.InjectTraceparent(ctx, sql)

		// Verify format: /*traceparent='00-{32hex}-{16hex}-{2hex}'*/ SQL
		if len(got) < len("/*traceparent='00-")+32+1+16+1+2+len("'*/ ")+len(sql) {
			t.Errorf("InjectTraceparent() result too short: %q", got)
		}
		if got[:len("/*traceparent='00-")] != "/*traceparent='00-" {
			t.Errorf("InjectTraceparent() missing traceparent prefix: %q", got)
		}
		if got[len(got)-len(sql):] != sql {
			t.Errorf("InjectTraceparent() missing original SQL suffix: %q", got)
		}
	})
}

func TestExtractOperation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		sql  string
		want string
	}{
		{name: "SELECT", sql: "SELECT * FROM users", want: "SELECT"},
		{name: "INSERT", sql: "INSERT INTO users (id) VALUES ($1)", want: "INSERT"},
		{name: "UPDATE", sql: "UPDATE users SET name = $1", want: "UPDATE"},
		{name: "DELETE", sql: "DELETE FROM users WHERE id = $1", want: "DELETE"},
		{name: "WITH (CTE)", sql: "WITH cte AS (SELECT 1) SELECT * FROM cte", want: "WITH"},
		{name: "TRUNCATE", sql: "TRUNCATE TABLE users CASCADE", want: "TRUNCATE"},
		{name: "leading whitespace", sql: "  SELECT 1", want: "SELECT"},
		{name: "tab whitespace", sql: "\tINSERT INTO foo (x) VALUES (1)", want: "INSERT"},
		{name: "newline whitespace", sql: "\n SELECT 1", want: "SELECT"},
		{name: "unknown operation", sql: "EXPLAIN SELECT 1", want: "DB"},
		{name: "empty string", sql: "", want: "DB"},
		{name: "single word unknown", sql: "VACUUM", want: "DB"},
		{name: "lowercase select", sql: "select * from users", want: "SELECT"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := rdb.ExtractOperation(tt.sql)
			if got != tt.want {
				t.Errorf("ExtractOperation(%q) = %q, want %q", tt.sql, got, tt.want)
			}
		})
	}
}

func TestExtractQueryMeta(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		sql       string
		wantOp    string
		wantTable string
	}{
		{name: "SELECT FROM", sql: "SELECT * FROM users WHERE id = $1", wantOp: "SELECT", wantTable: "users"},
		{name: "INSERT INTO", sql: "INSERT INTO artists (id, name) VALUES ($1, $2)", wantOp: "INSERT", wantTable: "artists"},
		{name: "UPDATE", sql: "UPDATE concerts SET name = $1 WHERE id = $2", wantOp: "UPDATE", wantTable: "concerts"},
		{name: "DELETE FROM", sql: "DELETE FROM sessions WHERE expired_at < $1", wantOp: "DELETE", wantTable: "sessions"},
		{name: "schema prefix", sql: "SELECT * FROM app.venues WHERE id = $1", wantOp: "SELECT", wantTable: "venues"},
		{name: "quoted table", sql: `SELECT * FROM "users" WHERE id = $1`, wantOp: "SELECT", wantTable: "users"},
		{name: "WITH CTE no table", sql: "WITH cte AS (SELECT 1) SELECT * FROM cte", wantOp: "WITH", wantTable: ""},
		{name: "TRUNCATE no table", sql: "TRUNCATE TABLE users CASCADE", wantOp: "TRUNCATE", wantTable: ""},
		{name: "lowercase", sql: "select * from tickets where event_id = $1", wantOp: "SELECT", wantTable: "tickets"},
		{name: "empty string", sql: "", wantOp: "DB", wantTable: ""},
		{name: "SELECT without FROM", sql: "SELECT 1", wantOp: "SELECT", wantTable: ""},
		{name: "INSERT with newline", sql: "INSERT INTO\n  follow_artists (user_id, artist_id) VALUES ($1, $2)", wantOp: "INSERT", wantTable: "follow_artists"},
		{name: "column name contains FROM", sql: "SELECT date_from, name FROM events WHERE id = $1", wantOp: "SELECT", wantTable: "events"},
		{name: "column name contains INTO", sql: "INSERT INTO tickets (inserted_into) VALUES ($1)", wantOp: "INSERT", wantTable: "tickets"},
		{name: "alias starts with keyword", sql: "SELECT FROMDATE, name FROM users WHERE id = $1", wantOp: "SELECT", wantTable: "users"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			meta := rdb.ExtractQueryMeta(tt.sql)
			if meta.Operation != tt.wantOp {
				t.Errorf("ExtractQueryMeta(%q).Operation = %q, want %q", tt.sql, meta.Operation, tt.wantOp)
			}
			if meta.Table != tt.wantTable {
				t.Errorf("ExtractQueryMeta(%q).Table = %q, want %q", tt.sql, meta.Table, tt.wantTable)
			}
		})
	}
}
