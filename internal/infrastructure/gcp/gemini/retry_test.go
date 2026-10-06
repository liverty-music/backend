package gemini_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/gcp/gemini"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	errBody429 = `{"error":{"code":429,"message":"Resource exhausted","status":"RESOURCE_EXHAUSTED"}}`
	errBody503 = `{"error":{"code":503,"message":"Service unavailable","status":"UNAVAILABLE"}}`
	errBody504 = `{"error":{"code":504,"message":"Deadline exceeded","status":"DEADLINE_EXCEEDED"}}`
	errBody400 = `{"error":{"code":400,"message":"Bad Request","status":"INVALID_ARGUMENT"}}`
)

// @spec components/entity/concert/search "Recovered on retry"
func TestSearch_RetryOnTransientError(t *testing.T) {
	t.Parallel()

	from := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	artist := &entity.Artist{Name: "Test Artist"}
	officialSite := &entity.OfficialSite{URL: "https://example.com"}
	successBody := `{"series": [{"title": "Retry Success", "source_url": "https://example.com/retry", "events": [
		{"venue": "Test Hall", "country": "JP", "admin_area": "", "local_date": "2026-03-01", "open_time": "", "start_time": "2026-03-01T18:00:00Z"}]}]}`

	s, calls := newTestSearcher(t, gemini.Config{}, nil, func(n int32, _ map[string]any) (int, string) {
		// The first attempt times out; the second succeeds.
		if n == 1 {
			return http.StatusGatewayTimeout, errBody504
		}
		return http.StatusOK, geminiResponse(successBody, "STOP")
	})

	got, err := s.Search(context.Background(), artist, officialSite, from)

	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "Retry Success", got[0].Title)
	assert.Equal(t, int32(2), calls.Load(), "1 timeout + 1 retry success")
}

// @spec components/entity/concert/search "All attempts transient"
func TestSearch_AllRetriesExhausted(t *testing.T) {
	t.Parallel()

	from := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	artist := &entity.Artist{Name: "Test Artist"}
	officialSite := &entity.OfficialSite{URL: "https://example.com"}

	s, calls := newTestSearcher(t, gemini.Config{}, nil, func(int32, map[string]any) (int, string) {
		return http.StatusTooManyRequests, errBody429
	})

	got, md, err := s.SearchExt(context.Background(), artist, officialSite, from)

	// Transient exhaustion degrades to no results, flagged in the metadata.
	assert.NoError(t, err)
	assert.Empty(t, got)
	require.NotNil(t, md.Grounded)
	assert.True(t, md.Grounded.ExhaustedTransient)
	assert.Equal(t, int32(3), calls.Load(), "3 attempts in total")
}

func TestSearch_NonRetryableErrorStopsImmediately(t *testing.T) {
	t.Parallel()

	from := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	artist := &entity.Artist{Name: "Test Artist"}
	officialSite := &entity.OfficialSite{URL: "https://example.com"}

	s, calls := newTestSearcher(t, gemini.Config{}, nil, func(int32, map[string]any) (int, string) {
		return http.StatusBadRequest, errBody400
	})

	got, err := s.Search(context.Background(), artist, officialSite, from)

	assert.Nil(t, got)
	assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	assert.Equal(t, int32(1), calls.Load(), "permanent errors are not retried")
}

// @spec components/entity/concert/search "Caller deadline expires"
func TestSearch_ContextCancellationStopsRetry(t *testing.T) {
	t.Parallel()

	from := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	artist := &entity.Artist{Name: "Test Artist"}
	officialSite := &entity.OfficialSite{URL: "https://example.com"}

	s, calls := newTestSearcher(t, gemini.Config{}, nil, func(int32, map[string]any) (int, string) {
		return http.StatusServiceUnavailable, errBody503
	})

	// The deadline expires during the first retry backoff.
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	got, err := s.Search(ctx, artist, officialSite, from)

	assert.Nil(t, got)
	assert.Error(t, err)
	assert.Less(t, calls.Load(), int32(3), "should not exhaust all retries when context is cancelled")
}

// TestSearch_IncompleteResponseWithoutTextIsRetried locks in that an
// incomplete response carrying no text (e.g. TOO_MANY_TOOL_CALLS after a long
// search chain) is retried as a transient failure instead of being read as
// "no concerts" on the first attempt.
func TestSearch_IncompleteResponseWithoutTextIsRetried(t *testing.T) {
	t.Parallel()

	from := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	artist := &entity.Artist{Name: "Test Artist"}
	officialSite := &entity.OfficialSite{URL: "https://example.com"}
	noText := `{"candidates": [{"content": {"parts": [
		{"toolCall": {"toolType": "GOOGLE_SEARCH_WEB", "args": {"queries": ["Test Artist live"]}}}
	]}, "finishReason": "TOO_MANY_TOOL_CALLS"}]}`
	successBody := `{"series": [{"title": "Show", "source_url": "https://example.com/show", "events": [
		{"venue": "Test Hall", "country": "JP", "admin_area": "", "local_date": "2026-03-01", "open_time": "", "start_time": ""}]}]}`

	tests := []struct {
		name      string
		respond   func(n int32) string
		wantTitle []string
		wantCalls int32
		wantFlag  bool
	}{
		{
			name: "recovered on the next attempt",
			respond: func(n int32) string {
				if n == 1 {
					return noText
				}
				return geminiResponse(successBody, "STOP")
			},
			wantTitle: []string{"Show"},
			wantCalls: 2,
		},
		{
			name:      "every attempt incomplete degrades to no results",
			respond:   func(int32) string { return noText },
			wantCalls: 3,
			wantFlag:  true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s, calls := newTestSearcher(t, gemini.Config{}, nil, func(n int32, _ map[string]any) (int, string) {
				return http.StatusOK, tt.respond(n)
			})

			got, md, err := s.SearchExt(context.Background(), artist, officialSite, from)

			require.NoError(t, err)
			titles := make([]string, 0, len(got))
			for _, ds := range got {
				titles = append(titles, ds.Title)
			}
			if tt.wantTitle == nil {
				assert.Empty(t, titles)
			} else {
				assert.Equal(t, tt.wantTitle, titles)
			}
			assert.Equal(t, tt.wantCalls, calls.Load())
			require.NotNil(t, md.Grounded)
			assert.Equal(t, tt.wantFlag, md.Grounded.ExhaustedTransient)
		})
	}
}
