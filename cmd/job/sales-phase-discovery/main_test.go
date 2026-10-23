// Package main cannot be imported, so this test is in-package.
package main

import (
	"context"
	"io"
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/pkg/telemetry"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

// Not parallel: it installs a global tracer provider.
func TestDiscoverArtists_OneTracePerArtist(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder)))
	logger, err := logging.New(logging.WithWriter(io.Discard))
	require.NoError(t, err)

	artists := []*entity.Artist{{ID: "a1"}, {ID: "a2"}, {ID: "a3"}}
	seen := map[trace.TraceID]bool{}
	runCtx, run := telemetry.StartJobRun(context.Background(), "sales-phase-discovery")
	failed := discoverArtists(runCtx, logger, artists, func(ctx context.Context, _ *entity.Artist) error {
		seen[trace.SpanContextFromContext(ctx).TraceID()] = true
		return nil
	})
	run.End()

	assert.Zero(t, failed)
	var items int
	for _, s := range recorder.Ended() {
		if s.Name() != "ticket_sale_search.artist" {
			continue
		}
		items++
		assert.True(t, seen[s.SpanContext().TraceID()], "the search runs in its item trace")
		require.Len(t, s.Links(), 1)
		assert.Equal(t, run.SpanContext().SpanID(), s.Links()[0].SpanContext.SpanID(), "linked to the run")
	}
	assert.Equal(t, 3, items)
	assert.Len(t, seen, 3, "each artist has its own trace")
}
