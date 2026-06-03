//go:build integration

package gemini_test

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/gcp/gemini"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSalesPhaseSearcher_Integration fires a live Gemini API call against a
// real artist + series. Run with:
//
//	GCP_GEMINI_SEARCH_API_KEY=<key> go test -tags integration ./internal/infrastructure/gcp/gemini/ -run TestSalesPhaseSearcher_Integration -v
//
// The test requires GCP_GEMINI_SEARCH_API_KEY to be set; it skips otherwise.
func TestSalesPhaseSearcher_Integration(t *testing.T) {
	apiKey := os.Getenv("GCP_GEMINI_SEARCH_API_KEY")
	if apiKey == "" {
		t.Skip("GCP_GEMINI_SEARCH_API_KEY not set; skipping integration test")
	}

	ctx := context.Background()
	logger, _ := logging.New()

	cfg := gemini.SalesPhaseConfig{
		APIKey:          apiKey,
		ModelExtract:    "gemini-3.5-flash",
		ModelParse:      "gemini-3.5-flash",
		Temperature:     1.0,
		ThinkingExtract: "medium",
		ThinkingParse:   "low",
	}

	searcher, err := gemini.NewSalesPhaseSearcher(ctx, cfg, http.DefaultClient, logger)
	require.NoError(t, err)

	// Use a well-known Japanese artist with publicly discoverable ticket-sales
	// schedules for integration verification.
	artistName := "UVERworld"
	seriesTitle := "UVERworld TYCOON LIVE"

	// Seed candidate events matching approximate tour dates. The searcher will
	// match extracted covered_dates against these.
	candidateEvents := []*entity.SalesPhaseCandidateEvent{
		{
			EventID:         "test-event-001",
			LocalDate:       time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
			ListedVenueName: "幕張メッセ 国際展示場",
			AdminArea:       "千葉県",
		},
		{
			EventID:         "test-event-002",
			LocalDate:       time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC),
			ListedVenueName: "幕張メッセ 国際展示場",
			AdminArea:       "千葉県",
		},
	}

	candidates, err := searcher.SearchSalesPhases(
		ctx,
		artistName,
		seriesTitle,
		"test-series-001",
		candidateEvents,
	)
	require.NoError(t, err)

	t.Logf("SearchSalesPhases returned %d candidates", len(candidates))
	for i, c := range candidates {
		t.Logf("  [%d] method=%v channel=%v provider=%q apply_start=%v covered=%v url=%q",
			i, c.Method, c.Channel, c.ProviderName,
			c.ApplyStartTime.Format(time.RFC3339),
			c.CoveredEventIDs,
			c.URL,
		)
	}

	// Structural assertions: every returned candidate must pass the persistence guard.
	for i, c := range candidates {
		assert.False(t, c.ApplyStartTime.IsZero(),
			"candidate[%d]: ApplyStartTime must not be zero", i)
		assert.NotEmpty(t, c.CoveredEventIDs,
			"candidate[%d]: CoveredEventIDs must not be empty", i)
		assert.NotEmpty(t, c.AnchorEventID,
			"candidate[%d]: AnchorEventID must not be empty", i)
		assert.Equal(t, "test-series-001", c.SeriesID,
			"candidate[%d]: SeriesID must match the input", i)
	}
}

// TestSalesPhaseSearcher_EmptyGrounding verifies that when the grounded
// search returns no phases (empty envelope), the searcher returns an empty
// slice with no error.
//
// This test is intentionally narrow: it only covers the empty-result path
// using a fictional artist/series that should produce no results. It exists
// to catch regressions in the "empty grounding → no phase" contract without
// requiring a ground-truth fixture.
func TestSalesPhaseSearcher_EmptyGrounding_Integration(t *testing.T) {
	apiKey := os.Getenv("GCP_GEMINI_SEARCH_API_KEY")
	if apiKey == "" {
		t.Skip("GCP_GEMINI_SEARCH_API_KEY not set; skipping integration test")
	}

	ctx := context.Background()
	logger, _ := logging.New()

	cfg := gemini.SalesPhaseConfig{
		APIKey:       apiKey,
		ModelExtract: "gemini-3.1-flash-lite",
		ModelParse:   "gemini-3.1-flash-lite",
		Temperature:  1.0,
	}

	searcher, err := gemini.NewSalesPhaseSearcher(ctx, cfg, http.DefaultClient, logger)
	require.NoError(t, err)

	// Fictional artist + series — should produce no grounded results.
	candidates, err := searcher.SearchSalesPhases(
		ctx,
		"zzzNonExistentArtistXXX",
		"Nonexistent Tour 9999",
		"test-series-empty",
		nil,
	)
	require.NoError(t, err)
	// We can only assert the contract (no error), not the count, because the
	// model might still hallucinate results for a totally unknown query.
	t.Logf("empty-grounding test: %d candidates returned (expected ~0)", len(candidates))
}
