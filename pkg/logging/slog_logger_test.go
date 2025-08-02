package logging_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/pannpers/go-backend-scaffold/pkg/logging"
	"go.opentelemetry.io/otel/trace"
)

// contextWithTrace creates a new context with a span context derived from the given trace and span ID hex strings.
func contextWithTrace(traceID, spanID string) context.Context {
	tid, err := trace.TraceIDFromHex(traceID)
	if err != nil {
		panic(fmt.Sprintf("invalid traceIDStr for test: %s, error: %v", traceID, err))
	}

	sid, err := trace.SpanIDFromHex(spanID)
	if err != nil {
		panic(fmt.Sprintf("invalid spanIDStr for test: %s, error: %v", spanID, err))
	}

	spanCtx := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    tid,
		SpanID:     sid,
		TraceFlags: trace.FlagsSampled, // Mark as sampled
	})

	return trace.ContextWithSpanContext(context.Background(), spanCtx)
}

// normalizeOutput trims leading and trailing whitespace from the log output string.
// Since tests configure the logger to omit the 'time' attribute via ReplaceAttr,
// this function only needs to handle whitespace (e.g., newlines appended by slog handlers).
func normalizeOutput(output string) string {
	if output == "" {
		return ""
	}
	// With ReplaceAttr removing the time field for tests, we only need to trim whitespace.
	return strings.TrimSpace(output)
}

func TestLogger_LevelMethods(t *testing.T) {
	t.Parallel()

	type args struct {
		ctx   context.Context
		msg   string
		err   error // Add error field for Error method
		attrs []slog.Attr
	}

	tests := []struct {
		name         string
		opts         []logging.Option                                                                        // Options for Logger.New
		methodToCall func(l *logging.Logger, ctx context.Context, msg string, err error, attrs ...slog.Attr) // Updated signature for Error method
		args         args
		wantOutput   string // Expected output *without* time, after trimming space.
	}{
		{
			name: "Info_JSON_NoTrace_NoAttrs",
			opts: []logging.Option{
				logging.WithLevel(slog.LevelInfo),
				logging.WithFormat(logging.FormatJSON),
			},
			methodToCall: func(l *logging.Logger, ctx context.Context, msg string, _ error, attrs ...slog.Attr) {
				l.Info(ctx, msg, attrs...)
			},
			args: args{
				ctx: context.Background(),
				msg: "hello info",
			},
			wantOutput: `{"level":"INFO","msg":"hello info"}`,
		},
		{
			name: "Info_JSON_NoTrace_WithAttrs",
			opts: []logging.Option{
				logging.WithLevel(slog.LevelInfo),
				logging.WithFormat(logging.FormatJSON),
			},
			methodToCall: func(l *logging.Logger, ctx context.Context, msg string, _ error, attrs ...slog.Attr) {
				l.Info(ctx, msg, attrs...)
			},
			args: args{
				ctx:   context.Background(),
				msg:   "info with attrs",
				attrs: []slog.Attr{slog.String("key1", "val1"), slog.Int("key2", 123)},
			},
			wantOutput: `{"level":"INFO","msg":"info with attrs","key1":"val1","key2":123}`,
		},
		{
			name: "Debug_BelowInfoLevel_JSON_ShouldBeEmpty",
			opts: []logging.Option{
				logging.WithLevel(slog.LevelInfo), // Logger configured at INFO
				logging.WithFormat(logging.FormatJSON),
			},
			methodToCall: func(l *logging.Logger, ctx context.Context, msg string, _ error, attrs ...slog.Attr) {
				l.Debug(ctx, msg, attrs...)
			},
			args: args{
				ctx: context.Background(),
				msg: "hello debug, should not see me",
			},
			wantOutput: ``, // Expected no output
		},
		{
			name: "Debug_AtDebugLevel_JSON_NoTrace",
			opts: []logging.Option{
				logging.WithLevel(slog.LevelDebug),
				logging.WithFormat(logging.FormatJSON),
			},
			methodToCall: func(l *logging.Logger, ctx context.Context, msg string, _ error, attrs ...slog.Attr) {
				l.Debug(ctx, msg, attrs...)
			},
			args: args{
				ctx:   context.Background(),
				msg:   "hello debug",
				attrs: []slog.Attr{slog.Bool("processed", true)},
			},
			wantOutput: `{"level":"DEBUG","msg":"hello debug","processed":true}`,
		},
		{
			name: "Warn_Text_WithTrace_WithAttrs",
			opts: []logging.Option{
				logging.WithLevel(slog.LevelWarn),
				logging.WithFormat(logging.FormatText),
			},
			methodToCall: func(l *logging.Logger, ctx context.Context, msg string, _ error, attrs ...slog.Attr) {
				l.Warn(ctx, msg, attrs...)
			},
			args: args{
				ctx:   contextWithTrace("0102030405060708090a0b0c0d0e0f10", "a1a2a3a4a5a6a7a8"),
				msg:   "warning with trace and attrs",
				attrs: []slog.Attr{slog.String("module", "auth")},
			},
			// Order: level, msg, trace_id, span_id, user_attrs
			wantOutput: `level=WARN msg="warning with trace and attrs" ` +
				`trace_id=0102030405060708090a0b0c0d0e0f10 span_id=a1a2a3a4a5a6a7a8 module=auth`,
		},
		{
			name: "Error_JSON_WithTrace_NoAttrs",
			opts: []logging.Option{
				logging.WithLevel(slog.LevelError),
				logging.WithFormat(logging.FormatJSON),
			},
			methodToCall: (*logging.Logger).Error,
			args: args{
				ctx: contextWithTrace("112233445566778899aabbccddeeff00", "b1b2b3b4b5b6b7b8"),
				msg: "critical error occurred",
				err: errors.New("database connection failed"),
			},
			wantOutput: `{"level":"ERROR","msg":"critical error occurred",` +
				`"trace_id":"112233445566778899aabbccddeeff00","span_id":"b1b2b3b4b5b6b7b8","error":"database connection failed"}`,
		},
		{
			name: "Info_DefaultFormatText_NoTrace_WithAttrs", // Default format is Text from options.go
			opts: []logging.Option{
				logging.WithLevel(slog.LevelInfo),
				// No WithFormat, should use default from options.go (FormatText)
			},
			methodToCall: func(l *logging.Logger, ctx context.Context, msg string, _ error, attrs ...slog.Attr) {
				l.Info(ctx, msg, attrs...)
			},
			args: args{
				ctx:   context.Background(),
				msg:   "info with default text format",
				attrs: []slog.Attr{slog.String("user", "default_user")},
			},
			wantOutput: `level=INFO msg="info with default text format" user=default_user`,
		},
		{
			name: "Error_Text_NoTrace_NoAttrs",
			opts: []logging.Option{
				logging.WithLevel(slog.LevelError),
				logging.WithFormat(logging.FormatText),
			},
			methodToCall: (*logging.Logger).Error,
			args: args{
				ctx: context.Background(),
				msg: "plain error text",
				err: errors.New("something went wrong"),
			},
			wantOutput: `level=ERROR msg="plain error text" error="something went wrong"`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer

			opts := tc.opts
			// Ensure buffer is used for output by adding WithWriter to the logger options.
			opts = append(opts, logging.WithWriter(&buf), logging.WithReplaceAttr(func(_ []string, a slog.Attr) slog.Attr {
				if a.Key == slog.TimeKey {
					return slog.Attr{} // Discard time attribute for test stability
				}

				return a
			}))

			logger := logging.New(opts...)

			tc.methodToCall(logger, tc.args.ctx, tc.args.msg, tc.args.err, tc.args.attrs...)

			gotOutput := normalizeOutput(buf.String())

			if gotOutput != tc.wantOutput {
				t.Errorf("Unexpected log output for '%s':\nwant: %q\ngot:  %q", tc.name, tc.wantOutput, gotOutput)
			}
		})
	}
}
