package di

import (
	"io"
	"log/slog"
	"os"

	"github.com/liverty-music/backend/pkg/config"
	"github.com/pannpers/go-logging/logging"
)

// NewBootstrapLogger returns the logger a binary uses from its first line until
// it exits, shared by run() and the fatal log in main(). It reads only the
// logging config from the environment and is built with provideLogger, like
// the DI logger, so the two never diverge. When the logging config cannot be
// loaded, it falls back to JSON so the failure that follows is still recorded
// at its severity.
func NewBootstrapLogger() *logging.Logger {
	return newBootstrapLogger(os.Stdout)
}

func newBootstrapLogger(w io.Writer) *logging.Logger {
	if cfg, err := config.Load[config.LoggingConfig](); err == nil {
		if logger, err := provideLogger(*cfg, logging.WithWriter(w)); err == nil {
			return logger
		}
	}
	// JSON with the default level cannot fail to build.
	logger, _ := provideLogger(config.LoggingConfig{Format: "json"}, logging.WithWriter(w))
	return logger
}

// provideLogger builds a logger from the logging config. In JSON format the
// level is written as `severity` with Cloud Logging's names; in text format,
// which is for a human at a terminal, it stays `level`. extra options are
// applied last.
func provideLogger(logCfg config.LoggingConfig, extra ...logging.Option) (*logging.Logger, error) {
	var opts []logging.Option
	switch logCfg.Level {
	case "debug":
		opts = append(opts, logging.WithLevel(slog.LevelDebug))
	case "info":
		opts = append(opts, logging.WithLevel(slog.LevelInfo))
	case "warn":
		opts = append(opts, logging.WithLevel(slog.LevelWarn))
	case "error":
		opts = append(opts, logging.WithLevel(slog.LevelError))
	}
	switch logCfg.Format {
	case "text":
		opts = append(opts, logging.WithFormat(logging.FormatText))
	case "json":
		opts = append(opts,
			logging.WithFormat(logging.FormatJSON),
			logging.WithReplaceAttr(severityAttr),
		)
	}
	return logging.New(append(opts, extra...)...)
}

// severityAttr writes the top-level level attribute as `severity`, the field
// the GKE logging agent documents for structured logs, with Cloud Logging's
// LogSeverity names (slog's WARN is WARNING). slog's `level` key is mapped
// only by undocumented behavior.
func severityAttr(groups []string, a slog.Attr) slog.Attr {
	if len(groups) > 0 || a.Key != slog.LevelKey {
		return a
	}
	level, ok := a.Value.Any().(slog.Level)
	if !ok {
		return a
	}
	var severity string
	switch {
	case level < slog.LevelInfo:
		severity = "DEBUG"
	case level < slog.LevelWarn:
		severity = "INFO"
	case level < slog.LevelError:
		severity = "WARNING"
	default:
		severity = "ERROR"
	}
	return slog.String("severity", severity)
}
