package di_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"testing"

	"github.com/liverty-music/backend/internal/di"
	"github.com/liverty-music/backend/pkg/config"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// parseJSONLines parses buf as one JSON object per line and fails the test on
// any line that is not a single JSON object.
func parseJSONLines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var entries []map[string]any
	sc := bufio.NewScanner(buf)
	for sc.Scan() {
		var e map[string]any
		require.NoErrorf(t, json.Unmarshal(sc.Bytes(), &e), "line is not JSON: %s", sc.Text())
		entries = append(entries, e)
	}
	require.NoError(t, sc.Err())
	return entries
}

func TestNewBootstrapLogger(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		log  func(l *logging.Logger)
		want []map[string]any
	}{
		{
			// @spec components/infrastructure/backend/process/structured-logging "Startup message"
			name: "startup message is recorded at INFO",
			log: func(l *logging.Logger) {
				l.Info(context.Background(), "starting event consumer")
			},
			want: []map[string]any{{"severity": "INFO", "msg": "starting event consumer"}},
		},
		{
			// @spec components/infrastructure/backend/process/structured-logging "Process gives up at startup"
			name: "startup failure is one ERROR entry naming the dependency",
			log: func(l *logging.Logger) {
				err := fmt.Errorf("connect NATS: %w", errors.New("nats: no servers available for connection"))
				l.Error(context.Background(), "consumer failed", err)
			},
			want: []map[string]any{{
				"severity": "ERROR",
				"msg":      "consumer failed",
				"error":    "connect NATS: nats: no servers available for connection",
			}},
		},
		{
			name: "warning uses the Cloud Logging name",
			log: func(l *logging.Logger) {
				l.Warn(context.Background(), "NATS connection failed, retrying")
			},
			want: []map[string]any{{"severity": "WARNING", "msg": "NATS connection failed, retrying"}},
		},
		{
			name: "falls back to JSON when the logging config cannot be loaded",
			env:  map[string]string{"LOGGING_STRUCTURED": "not-a-bool"},
			log: func(l *logging.Logger) {
				l.Error(context.Background(), "server failed", errors.New("boom"))
			},
			want: []map[string]any{{"severity": "ERROR", "msg": "server failed", "error": "boom"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			buf := &bytes.Buffer{}

			tt.log(di.NewBootstrapLoggerTo(buf))

			entries := parseJSONLines(t, buf)
			require.Len(t, entries, len(tt.want))
			for i, want := range tt.want {
				for k, v := range want {
					assert.Equal(t, v, entries[i][k], "field %q", k)
				}
				assert.NotContains(t, entries[i], slog.LevelKey)
			}
		})
	}
}

func TestProvideLogger_TextKeepsLevel(t *testing.T) {
	t.Parallel()
	buf := &bytes.Buffer{}
	logger, err := di.ProvideLogger(config.LoggingConfig{Format: "text"}, logging.WithWriter(buf))
	require.NoError(t, err)

	logger.Warn(context.Background(), "retrying")

	assert.Contains(t, buf.String(), "level=WARN")
	assert.NotContains(t, buf.String(), "severity")
}
