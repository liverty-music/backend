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

// jst is the Japan Standard Time location used to build ground-truth times.
var jstLoc = func() *time.Location {
	loc, err := time.LoadLocation("Asia/Tokyo")
	if err != nil {
		return time.FixedZone("JST", 9*3600)
	}
	return loc
}()

func jst(y int, m time.Month, d, hh, mm int) time.Time {
	return time.Date(y, m, d, hh, mm, 0, 0, jstLoc)
}

func eventDate(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func fmtT(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	return t.In(jstLoc).Format("2006-01-02 15:04 MST")
}

func sameDayJST(a, b time.Time) bool {
	if a.IsZero() || b.IsZero() {
		return false
	}
	la, lb := a.In(jstLoc), b.In(jstLoc)
	return la.Year() == lb.Year() && la.YearDay() == lb.YearDay()
}

// TestSalesPhaseSearcher_Vaundy_HORO_Integration fires LIVE Gemini calls against
// a real tour pulled from the prod DB — Vaundy ASIA ARENA TOUR 2026 "HORO"
// (Japan legs: Tokyo/Makuhari Messe 2026-09-05/06, Fukuoka/Kitakyushu Messe
// 2026-10-24/25). The expected sales phases below are the REAL Japan presales,
// verified from Vaundy's official announcement (member.vaundy.jp / vaundy.jp):
//
//	① VAWS Premium 先行  : 2026-02-15 20:00 JST start  (FAN_CLUB / LOTTERY, 結果 03-27 / 支払 03-30)
//	② VAWS Member  先行  : 2026-03-02 12:00 JST start  (FAN_CLUB / LOTTERY, ぴあ経由)
//	③ Official     先行  : 2026-03-17 12:00 JST start  (OFFICIAL / LOTTERY)
//
// It runs the searcher with model = flash-lite at thinking = {medium, high} so
// the two levels can be compared (per the known flash-lite grounding behaviour:
// AUTO grounding under-fires at lower thinking and improves at high).
//
// Run with:
//
//	GCP_GEMINI_SEARCH_API_KEY=<key> go test -tags integration \
//	  ./internal/infrastructure/gcp/gemini/ -run TestSalesPhaseSearcher_Vaundy_HORO_Integration -v
func TestSalesPhaseSearcher_Vaundy_HORO_Integration(t *testing.T) {
	apiKey := os.Getenv("GCP_GEMINI_SEARCH_API_KEY")
	if apiKey == "" {
		t.Skip("GCP_GEMINI_SEARCH_API_KEY not set; skipping integration test")
	}

	ctx := context.Background()
	logger, _ := logging.New()

	const (
		artistName  = "Vaundy"
		seriesTitle = `Vaundy ASIA ARENA TOUR 2026 "HORO"`
		seriesID    = "vaundy-horo-2026"
	)

	// Real event IDs + dates from the prod DB (Japan legs only — clearest
	// Japanese-language sales schedule for grounding).
	candidateEvents := []*entity.SalesPhaseCandidateEvent{
		{EventID: "019e782c-6f58-7bf6-8a70-daacd48ce6a9", LocalDate: eventDate(2026, 9, 5), ListedVenueName: "幕張メッセ 9・11ホール", AdminArea: "JP-12"},
		{EventID: "019e782c-704b-7d23-8831-d1f53ef6e040", LocalDate: eventDate(2026, 9, 6), ListedVenueName: "幕張メッセ 9・11ホール", AdminArea: "JP-12"},
		{EventID: "019e826b-5390-7aa6-a944-98ed616294ea", LocalDate: eventDate(2026, 10, 24), ListedVenueName: "北九州メッセ", AdminArea: "JP-40"},
		{EventID: "019e826b-53c4-7eb2-8238-b3fdeafda4af", LocalDate: eventDate(2026, 10, 25), ListedVenueName: "北九州メッセ", AdminArea: "JP-40"},
	}

	// Single source of truth for the assertion below: every covered event a
	// candidate reports must resolve back to one of the events we provided.
	validEventIDs := make([]string, len(candidateEvents))
	for i, ce := range candidateEvents {
		validEventIDs[i] = ce.EventID
	}

	// Ground truth: the three real Japan presales (matched by apply_start day).
	groundTruth := []struct {
		name  string
		start time.Time
	}{
		{"VAWS Premium 先行", jst(2026, 2, 15, 20, 0)},
		{"VAWS Member 先行", jst(2026, 3, 2, 12, 0)},
		{"Official 先行", jst(2026, 3, 17, 12, 0)},
	}

	// Default to flash-lite for both steps; allow overriding the grounded
	// extract model to A/B grounding behaviour (e.g. gemini-3.5-flash).
	modelExtract := "gemini-3.1-flash-lite"
	if m := os.Getenv("SALES_PHASE_TEST_MODEL_EXTRACT"); m != "" {
		modelExtract = m
	}

	for _, thinking := range []string{"medium", "high"} {
		t.Run("extract="+modelExtract+"_thinking_"+thinking, func(t *testing.T) {
			cfg := gemini.SalesPhaseConfig{
				APIKey:          apiKey,
				ModelExtract:    modelExtract,
				ModelParse:      "gemini-3.1-flash-lite",
				Temperature:     1.0,
				ThinkingExtract: thinking,
				ThinkingParse:   thinking,
			}

			searcher, err := gemini.NewSalesPhaseSearcher(ctx, cfg, http.DefaultClient, logger)
			require.NoError(t, err)

			candidates, err := searcher.SearchSalesPhases(ctx, artistName, seriesTitle, seriesID, candidateEvents)
			require.NoError(t, err)

			t.Logf("[thinking=%s] SearchSalesPhases returned %d candidates", thinking, len(candidates))
			for i, c := range candidates {
				t.Logf("  [%d] method=%v channel=%v provider=%q start=%s end=%s result=%s pay=%s covered=%v url=%q",
					i, c.Method, c.Channel, c.ProviderName,
					fmtT(c.ApplyStartTime), fmtT(c.ApplyEndTime),
					fmtT(c.LotteryResultTime), fmtT(c.PaymentDeadlineTime),
					c.CoveredEventIDs, c.URL,
				)
			}

			// Structural guards (hard): every candidate must be persistable.
			for i, c := range candidates {
				assert.False(t, c.ApplyStartTime.IsZero(), "candidate[%d]: ApplyStartTime must not be zero", i)
				assert.NotEmpty(t, c.CoveredEventIDs, "candidate[%d]: CoveredEventIDs must not be empty", i)
				assert.NotEmpty(t, c.AnchorEventID, "candidate[%d]: AnchorEventID must not be empty", i)
				assert.Equal(t, seriesID, c.SeriesID, "candidate[%d]: SeriesID must match the input", i)
				for _, evID := range c.CoveredEventIDs {
					assert.Contains(t, validEventIDs, evID,
						"candidate[%d]: covered event must resolve to a provided candidate", i)
				}
			}

			// Accuracy vs ground truth — match by apply_start calendar day (JST).
			// Logged for the medium-vs-high comparison; soft floor of 1.
			matched := 0
			for _, g := range groundTruth {
				for _, c := range candidates {
					if sameDayJST(c.ApplyStartTime, g.start) {
						matched++
						t.Logf("  ✓ matched %q (apply_start %s)", g.name, fmtT(g.start))
						break
					}
				}
			}
			t.Logf("[thinking=%s] ground-truth presales matched: %d/%d (accuracy metric — logged, not gated)", thinking, matched, len(groundTruth))

			// Smoke gate: for a real, well-documented tour the searcher should
			// extract at least one phase. A 0 here (e.g. flash-lite AUTO grounding
			// under-firing at lower thinking) is a meaningful per-level failure.
			assert.GreaterOrEqual(t, len(candidates), 1,
				"[thinking=%s] expected at least one extracted sales phase", thinking)
		})
	}
}

// TestSalesPhaseSearcher_EmptyGrounding_Integration verifies that when the
// grounded search returns no phases (empty envelope), the searcher returns an
// empty slice with no error. Intentionally narrow: only the empty-result path,
// using a fictional artist/series that should produce no results.
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
