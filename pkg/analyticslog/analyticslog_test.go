package analyticslog_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/liverty-music/backend/pkg/analyticslog"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const labelsKey = "logging.googleapis.com/labels"

// writeLine writes one INFO line with attrs as JSON and returns it parsed.
func writeLine(t *testing.T, attrs ...slog.Attr) map[string]any {
	t.Helper()
	var buf bytes.Buffer
	logger, err := logging.New(logging.WithWriter(&buf), logging.WithFormat(logging.FormatJSON))
	require.NoError(t, err)
	logger.Info(context.Background(), "line", attrs...)
	var entry map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &entry))
	return entry
}

// @spec components/infrastructure/backend/process/structured-logging "New topic"
func TestAttrs_EveryTopicCarriesTheSameMark(t *testing.T) {
	t.Parallel()

	topics := []analyticslog.Topic{
		analyticslog.TopicJobResult,
		analyticslog.TopicNotificationDelivery,
		analyticslog.TopicCatalogReview,
		analyticslog.TopicCatalogSync,
		analyticslog.TopicPayment,
		analyticslog.Topic("added_later"),
	}
	for _, topic := range topics {
		t.Run(string(topic), func(t *testing.T) {
			t.Parallel()

			entry := writeLine(t, analyticslog.Attrs(topic, slog.Int("count", 3))...)

			assert.Equal(t, map[string]any{"sink": "analytics", "topic": string(topic)}, entry[labelsKey])
			assert.InDelta(t, 3, entry["count"], 0, "the line's own attributes are kept")
		})
	}
}

// @spec components/infrastructure/backend/process/structured-logging "Line with personal data"
func TestAttrs_PersonalDataIsNotMarked(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		attrs []slog.Attr
	}{
		{name: "email key", attrs: []slog.Attr{slog.String("email", "fan@example.com")}},
		{name: "email key suffix", attrs: []slog.Attr{slog.String("owner_email", "x")}},
		{name: "email-shaped value", attrs: []slog.Attr{slog.String("failure_detail", "rejected for fan@example.com")}},
		{name: "email in a group", attrs: []slog.Attr{slog.Group("user", slog.String("contact", "fan@example.com"))}},
		{name: "fan name", attrs: []slog.Attr{slog.String("display_name", "Hanako")}},
		{name: "phone", attrs: []slog.Attr{slog.String("phone_number", "090-0000-0000")}},
		{name: "address", attrs: []slog.Attr{slog.String("shipping_address", "Tokyo")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			entry := writeLine(t, analyticslog.Attrs(analyticslog.TopicPayment, tt.attrs...)...)

			assert.NotContains(t, entry, labelsKey)
		})
	}
}

func TestAttrs_CatalogNamesStayMarkable(t *testing.T) {
	t.Parallel()

	entry := writeLine(t, analyticslog.Attrs(analyticslog.TopicCatalogSync,
		slog.String("artist_name", "Artist"),
		slog.String("venue_name", "Venue"),
	)...)

	assert.Contains(t, entry, labelsKey)
}

func TestAttrs_DoesNotWriteIntoCallerSlice(t *testing.T) {
	t.Parallel()

	attrs := make([]slog.Attr, 1, 4)
	attrs[0] = slog.Int("count", 1)
	spare := attrs[:2]

	_ = analyticslog.Attrs(analyticslog.TopicJobResult, attrs...)

	assert.Equal(t, slog.Attr{}, spare[1])
}
