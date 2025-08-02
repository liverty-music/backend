package logging

import (
	"io"
	"log/slog"
	"os"
)

// Format represents the log output format.
type Format int

const (
	// FormatJSON specifies the JSON output format.
	FormatJSON Format = iota
	// FormatText specifies the human-readable text output format.
	FormatText
)

// DefaultLevel is the default logging level.
const DefaultLevel = slog.LevelInfo

// Option defines a function that configures a logger.
type Option func(*options)

// options holds all the logger configuration.
type options struct {
	writer          io.Writer
	level           slog.Level
	format          Format
	replaceAttrFunc func(groups []string, a slog.Attr) slog.Attr
}

// defaultOptions returns the default logger options.
func defaultOptions() *options {
	return &options{
		writer: os.Stdout,
		level:  DefaultLevel,
		format: FormatText, // Default to human-readable text format.
		// replaceAttrFunc is nil by default, meaning no attributes are replaced.
	}
}

// WithWriter sets the writer for the logger.
func WithWriter(w io.Writer) Option {
	return func(o *options) {
		if w != nil {
			o.writer = w
		}
	}
}

// WithLevel sets the logging level.
func WithLevel(level slog.Level) Option {
	return func(o *options) {
		o.level = level
	}
}

// WithFormat sets the output format for the logger.
func WithFormat(f Format) Option {
	return func(o *options) {
		o.format = f
	}
}

// WithReplaceAttr sets the ReplaceAttr function for the slog handler.
func WithReplaceAttr(f func(groups []string, a slog.Attr) slog.Attr) Option {
	return func(o *options) {
		o.replaceAttrFunc = f
	}
}
