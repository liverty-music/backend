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
	t.Parallel()

	logger, _ := logging.New()
	ctx := context.Background()
	from := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	artist := &entity.Artist{Name: "Test Artist"}
	officialSite := &entity.OfficialSite{URL: "https://example.com"}

	successBody := `{
		"events": [{
			"artist_name": "Test Artist",
			"event_name": "Retry Success Tour",
			"venue": "Test Hall",
			"local_date": "2026-03-01",
			"start_time": "2026-03-01T18:00:00Z",
			"source_url": "https://example.com/retry"
		}]
	}`

	var callCount atomic.Int32

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := callCount.Add(1)

		// First call returns 504, second succeeds
		if n == 1 {
			w.WriteHeader(http.StatusGatewayTimeout)
			if _, err := w.Write([]byte(`{"error":{"code":504,"message":"Deadline exceeded","status":"DEADLINE_EXCEEDED"}}`)); err != nil {
				t.Fatal(err)
			}
			return
		}

		fullResponse := fmt.Sprintf(`{
			"candidates": [{
				"content": {"parts": [{"text": %s}]},
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
	}, httpClient, logger)
	require.NoError(t, err)

	got, err := s.Search(ctx, artist, officialSite, from)

	assert.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "Retry Success Tour", got[0].Title)
	assert.Equal(t, int32(2), callCount.Load(), "should have called API twice (1 failure + 1 success)")
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
		w.WriteHeader(http.StatusGatewayTimeout)
		if _, err := w.Write([]byte(`{"error":{"code":504,"message":"Deadline exceeded","status":"DEADLINE_EXCEEDED"}}`)); err != nil {
			t.Fatal(err)
		}
	}))
	defer ts.Close()

	httpClient := &http.Client{Transport: &rewriteTransport{URL: ts.URL}}
	s, err := gemini.NewConcertSearcher(ctx, gemini.Config{
		ProjectID: "test", Location: "us-central1", ModelName: "gemini-pro",
	}, httpClient, logger)
	require.NoError(t, err)

	got, err := s.Search(ctx, artist, officialSite, from)

	assert.Nil(t, got)
	assert.Error(t, err)
	assert.ErrorIs(t, err, apperr.ErrDeadlineExceeded)
	assert.Equal(t, int32(3), callCount.Load(), "should have called API 3 times (all retries exhausted)")
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
	}, httpClient, logger)
	require.NoError(t, err)

	got, err := s.Search(ctx, artist, officialSite, from)

	assert.Nil(t, got)
	assert.Error(t, err)
	assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	assert.Equal(t, int32(1), callCount.Load(), "should have called API only once (non-retryable)")
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
		w.WriteHeader(http.StatusGatewayTimeout)
		if _, err := w.Write([]byte(`{"error":{"code":504,"message":"Deadline exceeded","status":"DEADLINE_EXCEEDED"}}`)); err != nil {
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
	}, httpClient, logger)
	require.NoError(t, err)

	got, err := s.Search(ctx, artist, officialSite, from)

	assert.Nil(t, got)
	assert.Error(t, err)
	// Should not have exhausted all 3 retries due to context cancellation
	assert.Less(t, callCount.Load(), int32(3), "should not exhaust all retries when context is cancelled")
}
