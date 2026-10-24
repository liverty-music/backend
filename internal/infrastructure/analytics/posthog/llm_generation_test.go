package posthog_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/sdk/trace"

	"github.com/liverty-music/backend/internal/infrastructure/analytics/posthog"
	"github.com/liverty-music/backend/internal/usecase"
)

func TestLLMGeneration_Properties(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		gen  posthog.LLMGeneration
		want usecase.AnalyticsProperties
	}{
		{
			name: "concert search that succeeded",
			gen: posthog.LLMGeneration{
				Model:        "gemini-3.8-flash",
				ModelVersion: "gemini-3.8-flash-001",
				Request: posthog.LLMRequest{
					ArtistID: "artist-1", OfficialSiteURL: "https://example.com",
					LinkedPageURLs: []string{"https://example.com/live"}, SearchFrom: "2026-10-23",
				},
				Usage:            posthog.LLMUsage{PromptTokens: 100, CandidatesTokens: 20, ThinkingTokens: 30, ToolUseTokens: 400, CachedTokens: 5},
				Latency:          1500 * time.Millisecond,
				WebSearchQueries: []string{"q1"},
				GroundingURLs:    []string{"https://example.com/live"},
				URLContext:       []posthog.URLContextResult{{URL: "https://example.com/live", Status: "URL_RETRIEVAL_STATUS_SUCCESS"}},
				FinishReason:     "STOP",
				Concerts:         &posthog.ConcertParseResult{DraftCount: 3, SeriesCount: 1, EventCount: 2},
			},
			want: usecase.AnalyticsProperties{
				"$ai_trace_id":                "trace-1",
				"$ai_span_id":                 "span-1",
				"$ai_model":                   "gemini-3.8-flash",
				"$ai_provider":                "google",
				"$ai_input_tokens":            500,
				"$ai_output_tokens":           50,
				"$ai_cache_read_input_tokens": 5,
				"$ai_latency":                 1.5,
				"$ai_is_error":                false,
				"$ai_stop_reason":             "STOP",
				"model_version":               "gemini-3.8-flash-001",
				"prompt_tokens":               100,
				"candidates_tokens":           20,
				"thinking_tokens":             30,
				"tool_use_tokens":             400,
				"web_search_queries":          []string{"q1"},
				"grounding_urls":              []string{"https://example.com/live"},
				"url_context":                 []posthog.URLContextResult{{URL: "https://example.com/live", Status: "URL_RETRIEVAL_STATUS_SUCCESS"}},
				"artist_id":                   "artist-1",
				"official_site_url":           "https://example.com",
				"linked_page_urls":            []string{"https://example.com/live"},
				"search_from":                 "2026-10-23",
				"draft_count":                 3,
				"series_count":                1,
				"event_count":                 2,
			},
		},
		{
			name: "sale search request that failed",
			gen: posthog.LLMGeneration{
				Model:     "gemini-3.8-flash",
				Request:   posthog.LLMRequest{ArtistID: "artist-1", SeriesIDs: []string{"s1"}},
				ErrorKind: "transient",
				Sales:     &posthog.SaleParseResult{Returned: 3, Kept: 2, Dropped: map[string]int{"already started": 1}},
			},
			want: usecase.AnalyticsProperties{
				"$ai_trace_id":                "trace-1",
				"$ai_span_id":                 "span-1",
				"$ai_model":                   "gemini-3.8-flash",
				"$ai_provider":                "google",
				"$ai_input_tokens":            0,
				"$ai_output_tokens":           0,
				"$ai_cache_read_input_tokens": 0,
				"$ai_latency":                 0.0,
				"$ai_is_error":                true,
				"$ai_error":                   "transient",
				"prompt_tokens":               0,
				"candidates_tokens":           0,
				"thinking_tokens":             0,
				"tool_use_tokens":             0,
				"web_search_queries":          []string{},
				"grounding_urls":              []string{},
				"url_context":                 []posthog.URLContextResult{},
				"artist_id":                   "artist-1",
				"series_ids":                  []string{"s1"},
				"returned_sales":              3,
				"kept_sales":                  2,
				"dropped_sales":               map[string]int{"already started": 1},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, tt.gen.Properties("trace-1", "span-1"))
		})
	}
}

func TestGenerationReporter_ReportGeneration(t *testing.T) {
	t.Parallel()

	t.Run("sends a personless $ai_generation under the span's trace", func(t *testing.T) {
		t.Parallel()
		ctx, span := trace.NewTracerProvider().Tracer("test").Start(context.Background(), "search")
		defer span.End()
		fake := &fakeEnqueuer{}
		client := posthog.NewWithEnqueuer(fake, "liverty-music-concert-discovery", "v1.2.3", testLogger(t))

		endTime := time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC)
		posthog.NewGenerationReporter(client, testLogger(t)).ReportGeneration(ctx, posthog.LLMGeneration{Model: "m", EndTime: endTime})

		captured := fake.Captured()
		require.Len(t, captured, 1)
		assert.NotEmpty(t, captured[0].Uuid, "a new event id")
		assert.Equal(t, endTime, captured[0].Timestamp, "the time the request ended")
		assert.Equal(t, "$ai_generation", captured[0].Event)
		assert.Equal(t, "liverty-music-concert-discovery", captured[0].DistinctId)
		assert.Equal(t, span.SpanContext().TraceID().String(), captured[0].Properties["$ai_trace_id"])
		assert.NotEmpty(t, captured[0].Properties["$ai_span_id"])
		assert.Equal(t, false, captured[0].Properties["$process_person_profile"])
		assert.Equal(t, "v1.2.3", captured[0].Properties["app_version"])
	})

	t.Run("without a client nothing is sent", func(t *testing.T) {
		t.Parallel()
		posthog.NewGenerationReporter(nil, testLogger(t)).ReportGeneration(context.Background(), posthog.LLMGeneration{})
		var nilReporter *posthog.GenerationReporter
		nilReporter.ReportGeneration(context.Background(), posthog.LLMGeneration{})
	})
}
