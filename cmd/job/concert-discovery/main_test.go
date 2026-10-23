// Package main cannot be imported, so this test is in-package.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/pkg/telemetry"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

// @spec components/infrastructure/backend/process/trace-propagation "One trace per artist"
//
// Not parallel: it installs a global tracer provider.
func TestSearchArtists_OneTracePerArtist(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder)))
	var logs bytes.Buffer
	logger, err := logging.New(logging.WithWriter(&logs), logging.WithFormat(logging.FormatJSON))
	require.NoError(t, err)

	artists := []*entity.Artist{{ID: "a1", Name: "A1"}, {ID: "a2", Name: "A2"}, {ID: "a3", Name: "A3"}}
	seen := map[string]trace.TraceID{}
	runCtx, run := telemetry.StartJobRun(context.Background(), "concert-discovery")
	failed := searchArtists(runCtx, logger, artists, func(ctx context.Context, artist *entity.Artist) error {
		seen[artist.ID] = trace.SpanContextFromContext(ctx).TraceID()
		if artist.ID == "a2" {
			return errors.New("search failed")
		}
		return nil
	})
	run.End()

	assert.Equal(t, 1, failed)
	var items []sdktrace.ReadOnlySpan
	for _, s := range recorder.Ended() {
		if s.Name() == "concert_search.artist" {
			items = append(items, s)
		}
	}
	require.Len(t, items, 3)
	traceIDs := map[trace.TraceID]bool{}
	for _, item := range items {
		traceIDs[item.SpanContext().TraceID()] = true
		assert.NotEqual(t, run.SpanContext().TraceID(), item.SpanContext().TraceID())
		require.Len(t, item.Links(), 1)
		assert.Equal(t, run.SpanContext().SpanID(), item.Links()[0].SpanContext.SpanID(), "linked to the run")
		var artistID string
		for _, a := range item.Attributes() {
			if a.Key == attribute.Key("artist.id") {
				artistID = a.Value.AsString()
			}
		}
		assert.Equal(t, seen[artistID], item.SpanContext().TraceID(), "the search for %s runs in its item trace", artistID)
	}
	assert.Len(t, traceIDs, 3, "each artist has its own trace")

	var line struct {
		TraceID  string `json:"trace_id"`
		ArtistID string `json:"artist_id"`
	}
	require.NoError(t, json.Unmarshal(logs.Bytes(), &line), "logs: %s", logs.String())
	assert.Equal(t, "a2", line.ArtistID)
	assert.Equal(t, seen["a2"].String(), line.TraceID, "the failure log carries the artist's trace id")
}

// @spec components/infrastructure/backend/process/structured-logging "Job summary"
func TestLogSummary_MarkedAsJobResult(t *testing.T) {
	t.Parallel()

	var logs bytes.Buffer
	logger, err := logging.New(logging.WithWriter(&logs), logging.WithFormat(logging.FormatJSON))
	require.NoError(t, err)

	logSummary(context.Background(), logger, 5, 2)

	var line map[string]any
	require.NoError(t, json.Unmarshal(logs.Bytes(), &line), "logs: %s", logs.String())
	assert.Equal(t, "concert discovery job complete", line["msg"])
	assert.Equal(t, map[string]any{"sink": "analytics", "topic": "job_result"}, line["logging.googleapis.com/labels"])
	assert.InDelta(t, 3, line["artists_succeeded"], 0)
}
