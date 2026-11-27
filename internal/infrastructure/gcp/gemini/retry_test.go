package gemini_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/gcp/gemini"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	errBody429 = `{"error":{"code":429,"message":"Resource exhausted","status":"RESOURCE_EXHAUSTED"}}`
	errBody503 = `{"error":{"code":503,"message":"Service unavailable","status":"UNAVAILABLE"}}`
	errBody504 = `{"error":{"code":504,"message":"Deadline exceeded","status":"DEADLINE_EXCEEDED"}}`
	errBody400 = `{"error":{"code":400,"message":"Bad Request","status":"INVALID_ARGUMENT"}}`
)

// A transient failure is not retried within a Search: it fails with
// Unavailable after its one request, for the caller to try again later.
func TestSearch_TransientFailureEndsAfterOneRequest(t *testing.T) {
	t.Parallel()

	from := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	artist := &entity.Artist{Name: "Test Artist"}
	officialSite := &entity.OfficialSite{URL: "https://example.com"}

	tests := []struct {
		name   string
		status int
		body   string
	}{
		// @spec components/entity/concert/search "Rate limited"
		{name: "rate limited", status: http.StatusTooManyRequests, body: errBody429},
		{name: "busy", status: http.StatusServiceUnavailable, body: errBody503},
		// @spec components/entity/concert/search "Server error"
		{name: "server error", status: http.StatusInternalServerError, body: `{"error":{"code":500,"message":"internal","status":"INTERNAL"}}`},
		{name: "gateway timeout", status: http.StatusGatewayTimeout, body: errBody504},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			reporter := &recordingReporter{}
			s, calls := newTestSearcher(t, gemini.Config{Reporter: reporter}, nil, func(int32, map[string]any) (int, string) {
				return tt.status, tt.body
			})

			got, md, err := s.SearchExt(context.Background(), artist, officialSite, from)

			assert.ErrorIs(t, err, apperr.ErrUnavailable)
			assert.Empty(t, got)
			assert.Equal(t, int32(1), calls.Load())
			require.NotNil(t, md.Grounded)
			assert.True(t, md.Grounded.Transient)
			reported := reporter.reported()
			require.Len(t, reported, 1)
			assert.Equal(t, "transient", reported[0].gen.ErrorKind)
		})
	}
}

// A transient failure is expected while the service is busy, so it is
// information; a permanent one is a warning.
func TestSearch_LogLevelOfAFailedCall(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name   string
		status int
		body   string
		level  string
	}{
		{name: "busy", status: http.StatusServiceUnavailable, body: errBody503, level: "INFO"},
		{name: "rejected", status: http.StatusForbidden, body: `{"error":{"code":403,"message":"denied","status":"PERMISSION_DENIED"}}`, level: "WARN"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var logs syncBuffer
			logger, err := logging.New(logging.WithWriter(&logs), logging.WithFormat(logging.FormatJSON))
			require.NoError(t, err)
			s, _ := newTestSearcher(t, gemini.Config{}, logger, func(int32, map[string]any) (int, string) {
				return tt.status, tt.body
			})

			_, _ = s.Search(context.Background(), &entity.Artist{Name: "A"}, nil, time.Now())

			var level string
			for _, line := range strings.Split(string(logs.Bytes()), "\n") {
				var entry struct{ Level, Msg string }
				if json.Unmarshal([]byte(line), &entry) == nil && entry.Msg == "gemini model call failed" {
					level = entry.Level
				}
			}
			assert.Equal(t, tt.level, level)
		})
	}
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
func TestSearch_CallerDeadlineDecidesOverTheFailure(t *testing.T) {
	t.Parallel()

	from := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	artist := &entity.Artist{Name: "Test Artist"}
	officialSite := &entity.OfficialSite{URL: "https://example.com"}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	s, calls := newTestSearcher(t, gemini.Config{}, nil, func(int32, map[string]any) (int, string) {
		// The call is detached from the caller's context, so it outlives the
		// deadline and then is refused.
		<-ctx.Done()
		return http.StatusServiceUnavailable, errBody503
	})

	got, err := s.Search(ctx, artist, officialSite, from)

	assert.Nil(t, got)
	assert.ErrorIs(t, err, apperr.ErrDeadlineExceeded)
	assert.Equal(t, int32(1), calls.Load())
}

// @spec components/entity/concert/search "Response not completed"
func TestSearch_IncompleteResponseIsNotTransient(t *testing.T) {
	t.Parallel()

	from := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	artist := &entity.Artist{Name: "Test Artist"}
	officialSite := &entity.OfficialSite{URL: "https://example.com"}
	// No text at all: it must not be read as "no concerts" either.
	noText := `{"candidates": [{"content": {"parts": [
		{"toolCall": {"toolType": "GOOGLE_SEARCH_WEB", "args": {"queries": ["Test Artist live"]}}}
	]}, "finishReason": "MAX_TOKENS"}]}`

	reporter := &recordingReporter{}
	s, calls := newTestSearcher(t, gemini.Config{Reporter: reporter}, nil, func(int32, map[string]any) (int, string) {
		return http.StatusOK, noText
	})

	got, md, err := s.SearchExt(context.Background(), artist, officialSite, from)

	assert.Nil(t, got)
	assert.ErrorIs(t, err, apperr.ErrInternal)
	assert.Equal(t, int32(1), calls.Load())
	require.NotNil(t, md.Grounded)
	assert.False(t, md.Grounded.Transient)
	reported := reporter.reported()
	require.Len(t, reported, 1)
	assert.Equal(t, "incomplete", reported[0].gen.ErrorKind)
}

// @spec components/entity/concert/search "Too many tool calls"
func TestSearch_TooManyToolCallsFailsWithoutRetry(t *testing.T) {
	t.Parallel()

	from := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	artist := &entity.Artist{Name: "Test Artist"}
	officialSite := &entity.OfficialSite{URL: "https://example.com"}
	stopped := `{"candidates": [{"content": {"parts": [
		{"toolCall": {"toolType": "GOOGLE_SEARCH_WEB", "args": {"queries": ["Test Artist live", "Test Artist tour"]}}}
	]}, "finishReason": "TOO_MANY_TOOL_CALLS"}]}`

	s, calls := newTestSearcher(t, gemini.Config{}, nil, func(int32, map[string]any) (int, string) {
		return http.StatusOK, stopped
	})

	got, md, err := s.SearchExt(context.Background(), artist, officialSite, from)

	assert.Nil(t, got)
	assert.ErrorIs(t, err, apperr.ErrInternal)
	assert.Equal(t, int32(1), calls.Load(), "not retried")
	require.NotNil(t, md.Grounded)
	assert.Equal(t, 2, md.Grounded.WebSearchQueries, "the stopped call's queries are still recorded")
}

func TestSearch_InvalidRequestRetriedWithoutToolReport(t *testing.T) {
	t.Parallel()

	from := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	artist := &entity.Artist{Name: "Test Artist"}
	officialSite := &entity.OfficialSite{URL: "https://example.com"}
	successBody := `{"series": [{"title": "Recovered", "source_url": "https://example.com/tour", "events": [
		{"venue": "Test Hall", "country": "JP", "admin_area": "", "local_date": "2026-03-01", "open_time": "", "start_time": ""}]}]}`

	type want struct {
		titles      []string
		err         error
		calls       int32
		toolConfigs []bool
	}
	tests := []struct {
		name    string
		cfg     gemini.Config
		respond func(n int32) (int, string)
		want    want
	}{
		{
			// @spec components/entity/concert/search "Invalid request recovered without the tool report"
			name: "Invalid request recovered without the tool report",
			cfg:  gemini.Config{IncludeServerSideToolInvocations: true},
			respond: func(n int32) (int, string) {
				if n == 1 {
					return http.StatusBadRequest, errBody400
				}
				return http.StatusOK, geminiResponse(successBody, "STOP")
			},
			want: want{titles: []string{"Recovered"}, calls: 2, toolConfigs: []bool{true, false}},
		},
		{
			// @spec components/entity/concert/search "Invalid request rejected twice"
			name: "Invalid request rejected twice",
			cfg:  gemini.Config{IncludeServerSideToolInvocations: true},
			respond: func(int32) (int, string) {
				return http.StatusBadRequest, errBody400
			},
			want: want{err: apperr.ErrInvalidArgument, calls: 2, toolConfigs: []bool{true, false}},
		},
		{
			name: "no re-send when the tool report is off",
			cfg:  gemini.Config{},
			respond: func(int32) (int, string) {
				return http.StatusBadRequest, errBody400
			},
			want: want{err: apperr.ErrInvalidArgument, calls: 1, toolConfigs: []bool{false}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var (
				mu          sync.Mutex
				toolConfigs []bool
			)
			s, calls := newTestSearcher(t, tt.cfg, nil, func(n int32, body map[string]any) (int, string) {
				mu.Lock()
				_, has := body["toolConfig"]
				toolConfigs = append(toolConfigs, has)
				mu.Unlock()
				return tt.respond(n)
			})

			got, md, err := s.SearchExt(context.Background(), artist, officialSite, from)

			titles := make([]string, 0, len(got))
			for _, ds := range got {
				titles = append(titles, ds.Title)
			}
			if tt.want.err != nil {
				assert.ErrorIs(t, err, tt.want.err)
				assert.Empty(t, titles)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.want.titles, titles)
			}
			assert.Equal(t, tt.want.calls, calls.Load())
			mu.Lock()
			assert.Equal(t, tt.want.toolConfigs, toolConfigs, "whether each request carried toolConfig")
			mu.Unlock()
			require.NotNil(t, md.Grounded)
			assert.Equal(t, int(tt.want.calls), md.Grounded.RetryCount, "requests across both passes")
			assert.Equal(t, tt.want.calls == 2, md.Grounded.ToolReportDropped)
		})
	}
}

// @spec components/entity/concert/search "Deadline passes during a call that then succeeds"
func TestSearch_DeadlinePassedDuringSuccessfulCallFails(t *testing.T) {
	t.Parallel()

	from := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	artist := &entity.Artist{Name: "Test Artist"}
	officialSite := &entity.OfficialSite{URL: "https://example.com"}
	successBody := `{"series": [{"title": "Late Show", "source_url": "https://example.com/late", "events": [
		{"venue": "Test Hall", "country": "JP", "admin_area": "", "local_date": "2026-03-01", "open_time": "", "start_time": ""}]}]}`

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	s, calls := newTestSearcher(t, gemini.Config{}, nil, func(int32, map[string]any) (int, string) {
		// The call is detached from the caller's context, so it outlives the
		// deadline and then answers with concerts.
		<-ctx.Done()
		return http.StatusOK, geminiResponse(successBody, "STOP")
	})

	got, err := s.Search(ctx, artist, officialSite, from)

	assert.ErrorIs(t, err, apperr.ErrDeadlineExceeded)
	assert.Empty(t, got)
	assert.Equal(t, int32(1), calls.Load())
}

// @spec components/entity/concert/search "Response without text"
func TestSearch_StopWithoutTextFailsWithoutRetry(t *testing.T) {
	t.Parallel()

	from := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	artist := &entity.Artist{Name: "Test Artist"}
	officialSite := &entity.OfficialSite{URL: "https://example.com"}
	noText := `{"candidates": [{"content": {"parts": [
		{"toolCall": {"toolType": "GOOGLE_SEARCH_WEB", "args": {"queries": ["Test Artist live"]}}}
	]}, "finishReason": "STOP"}]}`

	reporter := &recordingReporter{}
	s, calls := newTestSearcher(t, gemini.Config{Reporter: reporter}, nil, func(int32, map[string]any) (int, string) {
		return http.StatusOK, noText
	})

	got, _, err := s.SearchExt(context.Background(), artist, officialSite, from)

	assert.ErrorIs(t, err, apperr.ErrInternal)
	assert.Empty(t, got)
	assert.Equal(t, int32(1), calls.Load(), "not retried")
	reported := reporter.reported()
	require.Len(t, reported, 1)
	assert.Equal(t, "no_text", reported[0].gen.ErrorKind)
}
