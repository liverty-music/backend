package logging

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/liverty-music/backend/pkg/logging/attr"
	"go.opentelemetry.io/otel/trace"
)

// Logger is a structured logger using slog.
type Logger struct {
	logger *slog.Logger
}

// New creates a new Logger with the given options.
func New(opts ...Option) *Logger {
	o := defaultOptions()

	for _, opt := range opts {
		opt(o)
	}

	handlerOpts := &slog.HandlerOptions{
		Level:       o.level,
		ReplaceAttr: o.replaceAttrFunc,
	}

	var handler slog.Handler

	switch o.format {
	case FormatText:
		handler = slog.NewTextHandler(o.writer, handlerOpts)
	case FormatJSON:
		handler = slog.NewJSONHandler(o.writer, handlerOpts)
	default:
		panic(fmt.Sprintf("unknown logger format: %d", o.format))
	}

	logger := slog.New(handler)

	return &Logger{
		logger: logger,
	}
}

// Debug logs a debug message.
func (l *Logger) Debug(ctx context.Context, msg string, args ...slog.Attr) {
	l.log(ctx, slog.LevelDebug, msg, args...)
}

// Info logs an info message.
func (l *Logger) Info(ctx context.Context, msg string, args ...slog.Attr) {
	l.log(ctx, slog.LevelInfo, msg, args...)
}

// Warn logs a warning message.
func (l *Logger) Warn(ctx context.Context, msg string, args ...slog.Attr) {
	l.log(ctx, slog.LevelWarn, msg, args...)
}

// Error logs an error message.
func (l *Logger) Error(ctx context.Context, msg string, err error, args ...slog.Attr) {
	errorAttr := slog.String(attr.Error, err.Error())

	allArgs := make([]slog.Attr, 0, len(args)+1)
	allArgs = append(allArgs, errorAttr)
	allArgs = append(allArgs, args...)

	l.log(ctx, slog.LevelError, msg, allArgs...)
}

// With returns a logger with the given attributes.
func (l *Logger) With(args ...slog.Attr) *Logger {
	slogArgs := make([]any, len(args))
	for i, v := range args {
		slogArgs[i] = v
	}

	return &Logger{
		logger: l.logger.With(slogArgs...),
	}
}

// log is the internal logging method that handles context.
func (l *Logger) log(ctx context.Context, level slog.Level, msg string, args ...slog.Attr) {
	// Extract trace and span IDs from context.
	contextAttrs := fromContext(ctx)

	allArgs := make([]slog.Attr, 0, len(contextAttrs)+len(args))
	allArgs = append(allArgs, contextAttrs...)
	allArgs = append(allArgs, args...)

	l.logger.LogAttrs(ctx, level, msg, allArgs...)
}

// fromContext extracts trace and span IDs from context using OpenTelemetry.
func fromContext(ctx context.Context) []slog.Attr {
	var attrs []slog.Attr

	spanContext := trace.SpanFromContext(ctx).SpanContext()

	if !spanContext.IsValid() {
		return attrs
	}

	attrs = append(attrs,
		slog.String(attr.TraceID, spanContext.TraceID().String()),
		slog.String(attr.SpanID, spanContext.SpanID().String()),
	)

	return attrs
}
