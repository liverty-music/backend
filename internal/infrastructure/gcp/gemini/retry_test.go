package gemini_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/gcp/gemini"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSearch_RetryOnTransientError(t *testing.T) {
	t.Skip("pending rewrite for Go-side draft + Step 2 coercion split (#303)")
	t.Parallel()

	logger, _ := logging.New()
	ctx := context.Background()
	from := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	artist := &entity.Artist{Name: "Test Artist"}
	officialSite := &entity.OfficialSite{URL: "https://example.com"}

	successBody := `{
		"tours": [],
		"standalones": [{
			"event_title": "Retry Success Tour",
			"venue": "Test Hall",
			"local_date": "2026-03-01",
			"start_time": "2026-03-01T18:00:00Z",
			"source_url": "https://example.com/retry"
		}]
	}`

	var callCount atomic.Int32

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := callCount.Add(1)

		// First call returns 503 (retryable), second succeeds
		if n == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			if _, err := w.Write([]byte(`{"error":{"code":503,"message":"Service unavailable","status":"UNAVAILABLE"}}`)); err != nil {
				t.Fatal(err)
			}
			return
		}

		fullResponse := fmt.Sprintf(`{
			"candidates": [{
				"content": {"parts": [{"text": %s}]},
				"finishReason": "STOP",
				"groundingMetadata": {"webSearchQueries": ["test"]}
			}],
			"usageMetadata": {"promptTokenCount": 10, "candidatesTokenCount": 10, "totalTokenCount": 20}
		}`, strconv.Quote(successBody))

		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte(fullResponse)); err != nil {
			t.Fatal(err)
		}
	}))
	defer ts.Close()

	httpClient := &http.Client{Transport: &rewriteTransport{URL: ts.URL}}
	s, err := gemini.NewConcertSearcher(ctx, gemini.Config{
		ProjectID: "test", Location: "us-central1", ModelName: "gemini-pro",
	}, httpClient, false, logger)
	require.NoError(t, err)

	got, err := s.Search(ctx, artist, officialSite, from)

	assert.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "Retry Success Tour", got[0].Title)
	// Step 1 fans out into 3 parallel slices. The first request (whichever
	// slice wins the race) returns 503 and retries once; the other two
	// slices succeed on their first attempt. Step 2 parses the merged
	// envelope. Total: 3 slice calls + 1 retry + 1 parse = 5.
	assert.Equal(t, int32(5), callCount.Load(), "3 slices + 1 retry + Step 2 = 5 calls")
}

func TestSearch_AllRetriesExhausted(t *testing.T) {
	t.Parallel()

	logger, _ := logging.New()
	ctx := context.Background()
	from := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	artist := &entity.Artist{Name: "Test Artist"}
	officialSite := &entity.OfficialSite{URL: "https://example.com"}

	var callCount atomic.Int32

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
		if _, err := w.Write([]byte(`{"error":{"code":503,"message":"Service unavailable","status":"UNAVAILABLE"}}`)); err != nil {
			t.Fatal(err)
		}
	}))
	defer ts.Close()

	httpClient := &http.Client{Transport: &rewriteTransport{URL: ts.URL}}
	s, err := gemini.NewConcertSearcher(ctx, gemini.Config{
		ProjectID: "test", Location: "us-central1", ModelName: "gemini-pro",
	}, httpClient, false, logger)
	require.NoError(t, err)

	got, err := s.Search(ctx, artist, officialSite, from)

	assert.Nil(t, got)
	assert.Error(t, err)
	assert.ErrorIs(t, err, apperr.ErrUnavailable)
	// 3 parallel slices × 3 retries each = 9 total slice attempts. Step 2
	// is skipped because every slice exhausts retries.
	assert.Equal(t, int32(9), callCount.Load(), "3 slices × 3 retries = 9 calls")
}

func TestSearch_NonRetryableErrorStopsImmediately(t *testing.T) {
	t.Parallel()

	logger, _ := logging.New()
	ctx := context.Background()
	from := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	artist := &entity.Artist{Name: "Test Artist"}
	officialSite := &entity.OfficialSite{URL: "https://example.com"}

	var callCount atomic.Int32

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		w.WriteHeader(http.StatusBadRequest)
		if _, err := w.Write([]byte(`{"error":{"code":400,"message":"Bad Request","status":"INVALID_ARGUMENT"}}`)); err != nil {
			t.Fatal(err)
		}
	}))
	defer ts.Close()

	httpClient := &http.Client{Transport: &rewriteTransport{URL: ts.URL}}
	s, err := gemini.NewConcertSearcher(ctx, gemini.Config{
		ProjectID: "test", Location: "us-central1", ModelName: "gemini-pro",
	}, httpClient, false, logger)
	require.NoError(t, err)

	got, err := s.Search(ctx, artist, officialSite, from)

	assert.Nil(t, got)
	assert.Error(t, err)
	assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	// All 3 parallel Step 1 slices hit the 400. Permanent errors abort
	// each slice immediately (no retries). Step 2 is skipped because
	// runStep1Grounded surfaces the first error. Total: 3 calls.
	assert.Equal(t, int32(3), callCount.Load(), "3 slices × 1 (non-retryable) = 3 calls")
}

func TestSearch_ContextCancellationStopsRetry(t *testing.T) {
	t.Parallel()

	logger, _ := logging.New()
	from := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	artist := &entity.Artist{Name: "Test Artist"}
	officialSite := &entity.OfficialSite{URL: "https://example.com"}

	var callCount atomic.Int32

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
		if _, err := w.Write([]byte(`{"error":{"code":503,"message":"Service unavailable","status":"UNAVAILABLE"}}`)); err != nil {
			t.Fatal(err)
		}
	}))
	defer ts.Close()

	// Create a context that will be cancelled before the retry backoff completes
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	httpClient := &http.Client{Transport: &rewriteTransport{URL: ts.URL}}
	s, err := gemini.NewConcertSearcher(ctx, gemini.Config{
		ProjectID: "test", Location: "us-central1", ModelName: "gemini-pro",
	}, httpClient, false, logger)
	require.NoError(t, err)

	got, err := s.Search(ctx, artist, officialSite, from)

	assert.Nil(t, got)
	assert.Error(t, err)
	// 3 slices × 3 retries would be 9 calls if backoff ran to completion.
	// Context cancellation during backoff stops some retries.
	assert.Less(t, callCount.Load(), int32(9), "should not exhaust all retries when context is cancelled")
}
