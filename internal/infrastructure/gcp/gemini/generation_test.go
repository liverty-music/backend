package gemini_test

import (
	"context"
	"net/http"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/analytics/posthog"
	"github.com/liverty-music/backend/internal/infrastructure/gcp/gemini"
)

// reportedGeneration is one generation a searcher reported, with the trace of
// the context it was reported under.
type reportedGeneration struct {
	traceID string
	gen     posthog.LLMGeneration
}

// recordingReporter is a gemini.GenerationReporter that records each report.
type recordingReporter struct {
	mu   sync.Mutex
	gens []reportedGeneration
}

func (r *recordingReporter) ReportGeneration(ctx context.Context, g posthog.LLMGeneration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.gens = append(r.gens, reportedGeneration{traceID: trace.SpanContextFromContext(ctx).TraceID().String(), gen: g})
}

func (r *recordingReporter) reported() []reportedGeneration {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]reportedGeneration(nil), r.gens...)
}

// spanContext returns a context carrying a recording span, standing in for
// the search's trace.
func spanContext(t *testing.T) (context.Context, string) {
	t.Helper()
	ctx, span := sdktrace.NewTracerProvider().Tracer("test").Start(context.Background(), "search")
	t.Cleanup(func() { span.End() })
	return ctx, span.SpanContext().TraceID().String()
}

// @spec components/entity/concert/search "Search with a retry"
func TestSearch_ReportsEveryRequestOfARetry(t *testing.T) {
	t.Parallel()

	from := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	artist := &entity.Artist{ID: "artist-1", Name: "Test Artist"}
	officialSite := &entity.OfficialSite{URL: "https://example.com"}
	successBody := `{"series": [{"title": "Tour", "source_url": "https://example.com/tour", "events": [
		{"venue": "Hall A", "country": "JP", "admin_area": "", "local_date": "2026-03-01", "open_time": "", "start_time": ""},
		{"venue": "Hall B", "country": "JP", "admin_area": "", "local_date": "2026-03-08", "open_time": "", "start_time": ""}]}]}`

	reporter := &recordingReporter{}
	s, calls := newTestSearcher(t, gemini.Config{Reporter: reporter}, nil, func(n int32, _ map[string]any) (int, string) {
		if n == 1 {
			return http.StatusServiceUnavailable, errBody503
		}
		return http.StatusOK, geminiResponse(successBody, "STOP")
	})
	ctx, traceID := spanContext(t)

	got, err := s.Search(ctx, artist, officialSite, from)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, int32(2), calls.Load())

	reported := reporter.reported()
	require.Len(t, reported, 2, "one generation per request")
	for _, r := range reported {
		assert.Equal(t, traceID, r.traceID, "both under the search's trace")
		assert.Equal(t, "gemini-test", r.gen.Model)
		assert.Equal(t, posthog.LLMRequest{ArtistID: "artist-1", OfficialSiteURL: "https://example.com", SearchFrom: "2026-02-01"}, r.gen.Request)
	}
	first, second := reported[0].gen, reported[1].gen
	assert.Equal(t, "transient", first.ErrorKind)
	assert.Nil(t, first.Concerts, "the failed request parsed nothing")
	assert.Empty(t, second.ErrorKind)
	assert.Equal(t, "STOP", second.FinishReason)
	assert.Equal(t, posthog.LLMUsage{PromptTokens: 10, CandidatesTokens: 10}, second.Usage)
	assert.Equal(t, &posthog.ConcertParseResult{DraftCount: 2, SeriesCount: 1, EventCount: 2}, second.Concerts)
}

// @spec components/entity/concert/search "Grounded search"
func TestSearch_ReportsQueriesAndPagesRead(t *testing.T) {
	t.Parallel()

	from := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	artist := &entity.Artist{ID: "artist-1", Name: "Test Artist"}
	body := `{"series": []}`
	response := `{
		"modelVersion": "gemini-test-001",
		"candidates": [{
			"content": {"parts": [{"text": ` + strconv.Quote(body) + `}]},
			"finishReason": "STOP",
			"groundingMetadata": {
				"webSearchQueries": ["artist tour 2026", "artist live", "artist schedule"],
				"groundingChunks": [{"web": {"uri": "https://example.com/live"}}]
			},
			"urlContextMetadata": {"urlMetadata": [
				{"retrievedUrl": "https://example.com/live", "urlRetrievalStatus": "URL_RETRIEVAL_STATUS_SUCCESS"},
				{"retrievedUrl": "https://example.com/tour", "urlRetrievalStatus": "URL_RETRIEVAL_STATUS_ERROR"}
			]}
		}],
		"usageMetadata": {"promptTokenCount": 100, "candidatesTokenCount": 20, "thoughtsTokenCount": 30, "toolUsePromptTokenCount": 400, "cachedContentTokenCount": 5}
	}`

	reporter := &recordingReporter{}
	s, _ := newTestSearcher(t, gemini.Config{Reporter: reporter}, nil, func(int32, map[string]any) (int, string) {
		return http.StatusOK, response
	})

	_, err := s.Search(context.Background(), artist, &entity.OfficialSite{URL: "https://example.com"}, from)
	require.NoError(t, err)

	reported := reporter.reported()
	require.Len(t, reported, 1)
	g := reported[0].gen
	assert.Equal(t, []string{"artist tour 2026", "artist live", "artist schedule"}, g.WebSearchQueries)
	assert.Equal(t, []posthog.URLContextResult{
		{URL: "https://example.com/live", Status: "URL_RETRIEVAL_STATUS_SUCCESS"},
		{URL: "https://example.com/tour", Status: "URL_RETRIEVAL_STATUS_ERROR"},
	}, g.URLContext)
	assert.Equal(t, []string{"https://example.com/live"}, g.GroundingURLs)
	assert.Equal(t, "gemini-test-001", g.ModelVersion)
	assert.Equal(t, posthog.LLMUsage{PromptTokens: 100, CandidatesTokens: 20, ThinkingTokens: 30, ToolUseTokens: 400, CachedTokens: 5}, g.Usage)
	assert.Positive(t, g.Latency)
	assert.False(t, g.EndTime.IsZero(), "the time the request ended")
	assert.Equal(t, &posthog.ConcertParseResult{}, g.Concerts)
}

// A request that fails permanently is reported as a failure, and a reporter
// that is not set changes nothing.
func TestSearch_ReportsPermanentFailure(t *testing.T) {
	t.Parallel()

	reporter := &recordingReporter{}
	s, _ := newTestSearcher(t, gemini.Config{Reporter: reporter}, nil, func(int32, map[string]any) (int, string) {
		return http.StatusForbidden, `{"error":{"code":403,"message":"denied","status":"PERMISSION_DENIED"}}`
	})

	_, err := s.Search(context.Background(), &entity.Artist{ID: "a", Name: "A"}, nil, time.Now())
	require.Error(t, err)

	reported := reporter.reported()
	require.Len(t, reported, 1)
	assert.Equal(t, "permanent", reported[0].gen.ErrorKind)
}
