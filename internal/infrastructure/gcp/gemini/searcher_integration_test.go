//go:build integration

package gemini_test

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/gcp/gemini"
	"github.com/pannpers/go-logging/logging"
)

const (
	abEvalEnvVar        = "GEMINI_AB_EVAL"
	abEvalSmokeEnvVar   = "GEMINI_AB_EVAL_SMOKE"
	abEvalGCPVar        = "GCP_PROJECT_ID"
	abEvalAPIKeyVar     = "GCP_GEMINI_SEARCH_API_KEY" // optional: enables Gemini API direct backend
	abEvalModelsEnvVar  = "GEMINI_AB_EVAL_MODELS"     // CSV; empty = all built-in models
	abEvalArtistsEnvVar = "GEMINI_AB_EVAL_ARTISTS"    // CSV of artist names; empty = all fixture artists

	// resultsDir is relative to this package. Output filenames embed an
	// RFC3339Nano UTC timestamp to disambiguate concurrent runs.
	resultsDir = "testdata/ab_results"
)

// abMatrix returns the Cartesian product of the matrix axes plus repetitions.
//
// Current run is a tuning matrix on the most complex artist (Vaundy):
//   - Models: 3.1-flash-lite, 3.5-flash (3-flash-preview dropped — dominated)
//   - Temperature: 1.0 (fixed — prior 54-cell run showed monotonic gain to 1.0)
//   - Thinking: medium, high (re-comparing now that prompt + schema changed)
//   - Artists: filtered to Vaundy via GEMINI_AB_EVAL_ARTISTS (largest fixture,
//     most tour/standalone/festival edge cases)
//   - Repetitions: 3 (more samples per cell since artist count is small)
//
// Environment overrides:
//   - GEMINI_AB_EVAL_SMOKE=1 collapses to a single cell for an auth/API ping.
//   - GEMINI_AB_EVAL_MODELS=csv intersects the model list.
//   - GEMINI_AB_EVAL_ARTISTS=csv intersects the artist list by Name.
func abMatrix(artists []gemini.GroundTruthArtist) []abCell {
	if os.Getenv(abEvalSmokeEnvVar) == "1" && len(artists) > 0 {
		return []abCell{{
			Model:       "gemini-3.1-flash-lite",
			Temperature: 1.0,
			Thinking:    "medium",
			Artist:      artists[0],
			Repetition:  0,
		}}
	}
	models := []string{
		"gemini-3.1-flash-lite",
		"gemini-3.5-flash",
	}
	if filter := strings.TrimSpace(os.Getenv(abEvalModelsEnvVar)); filter != "" {
		want := map[string]bool{}
		for _, m := range strings.Split(filter, ",") {
			want[strings.TrimSpace(m)] = true
		}
		filtered := models[:0:0]
		for _, m := range models {
			if want[m] {
				filtered = append(filtered, m)
			}
		}
		models = filtered
	}

	// Apply optional artist-name filter.
	if filter := strings.TrimSpace(os.Getenv(abEvalArtistsEnvVar)); filter != "" {
		want := map[string]bool{}
		for _, a := range strings.Split(filter, ",") {
			want[strings.TrimSpace(a)] = true
		}
		filtered := artists[:0:0]
		for _, a := range artists {
			if want[a.Name] {
				filtered = append(filtered, a)
			}
		}
		artists = filtered
	}

	temps := []float32{1.0}
	thinks := []string{"medium", "high"}
	const reps = 3

	var cells []abCell
	for _, m := range models {
		for _, t := range temps {
			for _, th := range thinks {
				for _, a := range artists {
					for r := 0; r < reps; r++ {
						cells = append(cells, abCell{
							Model:       m,
							Temperature: t,
							Thinking:    th,
							Artist:      a,
							Repetition:  r,
						})
					}
				}
			}
		}
	}
	return cells
}

type abCell struct {
	Model       string
	Temperature float32
	Thinking    string
	Artist      gemini.GroundTruthArtist
	Repetition  int
}

type cellResult struct {
	Model         string  `json:"model"`
	Temperature   float32 `json:"temperature"`
	ThinkingLevel string  `json:"thinking_level"`
	ArtistID      string  `json:"artist_id"`
	ArtistName    string  `json:"artist_name"`
	Repetition    int     `json:"repetition"`
	// Precision and recall split.
	// Precision = matched_public / (returned - festival_leaks)
	//   (a festival leak is a returned event matching an excluded_per_spec
	//    fixture entry — counted separately, not as a generic FP)
	// Recall = matched_public / public_fixture_count
	//   (excluded_per_spec fixture entries are removed from the denominator)
	Precision         float64                `json:"precision"`
	RecallPublic      float64                `json:"recall_public"`
	RecallAll         float64                `json:"recall_all"`
	F1Public          float64                `json:"f1_public"`
	F1All             float64                `json:"f1_all"`
	FalsePositives    int                    `json:"false_positives"`     // returned, not matching any fixture entry
	FestivalLeaks     int                    `json:"festival_leaks"`      // returned, matching an excluded_per_spec entry
	FieldAccuracy     aggregateFieldAccuracy `json:"field_accuracy"`
	PromptTokens      int32                  `json:"prompt_tokens"`
	CandidatesTokens  int32                  `json:"candidates_tokens"`
	ThinkingTokens    int32                  `json:"thinking_tokens"`
	ToolUseTokens     int32                  `json:"tool_use_tokens"`
	TotalTokens       int32                  `json:"total_tokens"`
	LatencyMillis     int64                  `json:"latency_ms"`
	ReturnedCount     int                    `json:"returned_count"`
	MatchedCount      int                    `json:"matched_count"`
	// Tours / standalones from the raw model JSON, pre-flatten.
	ToursCount       int `json:"tours_count"`
	StandalonesCount int `json:"standalones_count"`
	// Parts breakdown of the candidate. Useful for spotting cases where
	// the JSON arrived split across multiple parts or where most output
	// went into thought-summary parts that don't carry the final JSON.
	PartsTotal   int `json:"parts_total"`
	ThoughtParts int `json:"thought_parts"`
	TextParts    int `json:"text_parts"`
	// URLContext panel: per-status counts from URLContextMetadata.
	URLContextTotal   int `json:"url_context_total"`
	URLContextSuccess int `json:"url_context_success"`
	URLContextError   int `json:"url_context_error"`
	URLContextOther   int `json:"url_context_other"`
	// GroundingSearchQueries is the number of GoogleSearch queries the model
	// issued — billed separately at $35/1K. Together with URLContextTotal
	// this is the full grounding spend picture for the cell.
	GroundingSearchQueries int `json:"grounding_search_queries"`
	// Truncation diagnostics: even when FinishReason is "STOP" the API can
	// silently truncate the JSON candidate mid-emission. FinishMessage and
	// AvgLogprobs let us cross-check.
	FinishReason  string  `json:"finish_reason"`
	FinishMessage string  `json:"finish_message"`
	AvgLogprobs   float64 `json:"avg_logprobs"`
	CostUSD           float64 `json:"cost_usd"`
	Error             string  `json:"error,omitempty"`
}

type aggregateFieldAccuracy struct {
	Venue     float64 `json:"venue"`
	AdminArea float64 `json:"admin_area"`
	LocalDate float64 `json:"local_date"`
	StartTime float64 `json:"start_time"`
	OpenTime  float64 `json:"open_time"`
	SourceURL float64 `json:"source_url"`
}

type runFile struct {
	RunMetadata runMeta      `json:"run_metadata"`
	Cells       []cellResult `json:"cells"`
}

type runMeta struct {
	SDKVersion     string  `json:"sdk_version"`
	EvaluationFrom string  `json:"evaluation_from"`
	StartedAt      string  `json:"started_at"`
	FinishedAt     string  `json:"finished_at"`
	CellsExecuted  int     `json:"cells_executed"`
	TotalCostUSD   float64 `json:"total_cost_usd"`
}

// TestConcertSearcher_ABEval is the matrix-based A/B harness for the Gemini
// concert searcher. It runs only when GEMINI_AB_EVAL=1 and requires ADC for
// the configured GCP project. Per design.md, this is a one-shot ad-hoc
// evaluation, not a CI test.
func TestConcertSearcher_ABEval(t *testing.T) {
	if os.Getenv(abEvalEnvVar) != "1" {
		t.Skipf("matrix A/B harness is opt-in: set %s=1 to run", abEvalEnvVar)
	}
	projectID := os.Getenv(abEvalGCPVar)
	if projectID == "" {
		t.Fatalf("%s must be set to a GCP project with Vertex AI + grounding enabled", abEvalGCPVar)
	}

	ctx := context.Background()
	logger, _ := logging.New(
		logging.WithLevel(slog.LevelInfo),
		logging.WithFormat(logging.FormatJSON),
	)

	gt, err := gemini.LoadGroundTruth()
	if err != nil {
		t.Fatalf("load ground truth: %v", err)
	}

	from, err := time.Parse("2006-01-02", gt.EvaluationFrom)
	if err != nil {
		t.Fatalf("parse evaluation_from: %v", err)
	}

	cells := abMatrix(gt.Artists)
	t.Logf("A/B matrix: %d cells across %d artists", len(cells), len(gt.Artists))

	startedAt := time.Now().UTC()
	results := make([]cellResult, 0, len(cells))
	totalCost := 0.0
	// Snapshot is namespaced per run (timestamped) so parallel invocations
	// (e.g. one for 3-flash-preview, another for 3.1-flash-lite + 3.5-flash)
	// don't clobber each other's progress files.
	if err := os.MkdirAll(resultsDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", resultsDir, err)
	}
	runStamp := startedAt.Format("20060102T150405Z")
	inProgressPath := filepath.Join(resultsDir, "in-progress_"+runStamp+".json")

	snapshot := func() {
		meta := runMeta{
			SDKVersion:     "google.golang.org/genai@v1.57.0",
			EvaluationFrom: gt.EvaluationFrom,
			StartedAt:      startedAt.Format(time.RFC3339),
			FinishedAt:     time.Now().UTC().Format(time.RFC3339),
			CellsExecuted:  len(results),
			TotalCostUSD:   totalCost,
		}
		jb, err := json.MarshalIndent(runFile{RunMetadata: meta, Cells: results}, "", "  ")
		if err != nil {
			t.Logf("snapshot marshal failed: %v", err)
			return
		}
		if err := os.WriteFile(inProgressPath, jb, 0o644); err != nil {
			t.Logf("snapshot write failed: %v", err)
		}
	}

	rawDir := filepath.Join(resultsDir, runStamp+"_raw")
	if err := os.MkdirAll(rawDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", rawDir, err)
	}
	t.Logf("raw responses → %s", rawDir)

	for i, cell := range cells {
		t.Logf("[%d/%d] model=%s temp=%.1f thinking=%s artist=%s rep=%d",
			i+1, len(cells), cell.Model, cell.Temperature, cell.Thinking, cell.Artist.Name, cell.Repetition)

		res := runCell(ctx, t, logger, projectID, cell, from, rawDir, i+1)
		results = append(results, res)
		totalCost += res.CostUSD
		snapshot()
	}

	finishedAt := time.Now().UTC()
	meta := runMeta{
		SDKVersion:     "google.golang.org/genai@v1.57.0",
		EvaluationFrom: gt.EvaluationFrom,
		StartedAt:      startedAt.Format(time.RFC3339),
		FinishedAt:     finishedAt.Format(time.RFC3339),
		CellsExecuted:  len(results),
		TotalCostUSD:   totalCost,
	}

	if err := writeOutputs(t, runFile{RunMetadata: meta, Cells: results}); err != nil {
		t.Fatalf("write outputs: %v", err)
	}
	// Final timestamped files exist now — drop the snapshot.
	_ = os.Remove(inProgressPath)
	t.Logf("A/B run complete: %d cells, $%.4f total cost", len(results), totalCost)
}

func runCell(
	ctx context.Context,
	t *testing.T,
	logger *logging.Logger,
	projectID string,
	cell abCell,
	from time.Time,
	rawDir string,
	cellIdx int,
) cellResult {
	t.Helper()

	res := cellResult{
		Model:         cell.Model,
		Temperature:   cell.Temperature,
		ThinkingLevel: cell.Thinking,
		ArtistID:      cell.Artist.ID,
		ArtistName:    cell.Artist.Name,
		Repetition:    cell.Repetition,
	}

	s, err := gemini.NewConcertSearcher(ctx, gemini.Config{
		ProjectID:     projectID,
		Location:      "global",
		ModelName:     cell.Model,
		Temperature:   cell.Temperature,
		ThinkingLevel: cell.Thinking,
		APIKey:        os.Getenv(abEvalAPIKeyVar), // empty → Vertex AI; set → Gemini API direct
	}, nil, true, logger)
	if err != nil {
		res.Error = "construct searcher: " + err.Error()
		writeRawResponse(t, rawDir, cellIdx, cell, nil, nil, res.Error)
		return res
	}

	artist := &entity.Artist{ID: cell.Artist.ID, Name: cell.Artist.Name}
	site := &entity.OfficialSite{URL: cell.Artist.OfficialSiteURL}

	start := time.Now()
	got, md, err := s.SearchExt(ctx, artist, site, from)
	res.LatencyMillis = time.Since(start).Milliseconds()
	if md != nil {
		res.PromptTokens = md.PromptTokens
		res.CandidatesTokens = md.CandidatesTokens
		res.ThinkingTokens = md.ThinkingTokens
		res.ToolUseTokens = md.ToolUseTokens
		res.TotalTokens = md.TotalTokens
		res.ToursCount = md.ToursCount
		res.StandalonesCount = md.StandalonesCount
		res.PartsTotal = md.PartsTotal
		res.ThoughtParts = md.ThoughtParts
		res.TextParts = md.TextParts
		res.GroundingSearchQueries = md.WebSearchQueries
		res.FinishReason = md.FinishReason
		res.FinishMessage = md.FinishMessage
		res.AvgLogprobs = md.AvgLogprobs
		res.URLContextTotal = len(md.URLContextRetrieved)
		for _, u := range md.URLContextRetrieved {
			switch {
			case strings.Contains(u.Status, "SUCCESS"):
				res.URLContextSuccess++
			case strings.Contains(u.Status, "ERROR") || strings.Contains(u.Status, "FAILED"):
				res.URLContextError++
			default:
				res.URLContextOther++
			}
		}
	}

	errMsg := ""
	if err != nil {
		errMsg = err.Error()
	}
	writeRawResponse(t, rawDir, cellIdx, cell, md, got, errMsg)

	// Always charge for tokens / search queries consumed, even when the call
	// errored out (e.g. invalid-JSON truncation still bills the input,
	// thinking, candidates, and tool-use tokens the API metered). Skipping
	// CostUSD on error under-reports actual spend by ~30-50% in our matrix.
	res.CostUSD = gemini.DefaultPricing.CostUSD(
		cell.Model,
		res.PromptTokens,
		res.CandidatesTokens,
		res.ThinkingTokens,
		res.ToolUseTokens,
		int32(res.GroundingSearchQueries),
	)

	if err != nil {
		res.Error = errMsg
		return res
	}

	res.ReturnedCount = len(got)
	scoreCell(&res, got, cell.Artist.Events)
	return res
}

// writeRawResponse persists one cell's raw model output and grounding info
// for offline analysis. The file name encodes the cell coordinates so it
// can be located without consulting the index JSON.
func writeRawResponse(
	t *testing.T,
	dir string,
	cellIdx int,
	cell abCell,
	md *gemini.SearchMetadata,
	parsed []*entity.ScrapedConcert,
	errMsg string,
) {
	t.Helper()
	safeArtist := strings.ReplaceAll(cell.Artist.Name, " ", "_")
	fname := fmt.Sprintf("cell_%03d_%s_T%.1f_th-%s_%s_rep%d.json",
		cellIdx, cell.Model, cell.Temperature, cell.Thinking, safeArtist, cell.Repetition)
	path := filepath.Join(dir, fname)

	payload := map[string]any{
		"cell_index":       cellIdx,
		"model":            cell.Model,
		"temperature":      cell.Temperature,
		"thinking_level":   cell.Thinking,
		"artist_id":        cell.Artist.ID,
		"artist_name":      cell.Artist.Name,
		"official_site":    cell.Artist.OfficialSiteURL,
		"repetition":       cell.Repetition,
		"parsed_concerts":  parsed,
		"error":            errMsg,
	}
	if md != nil {
		payload["raw_response_text"] = md.RawResponseText
		payload["finish_reason"] = md.FinishReason
		payload["finish_message"] = md.FinishMessage
		payload["avg_logprobs"] = md.AvgLogprobs
		payload["retry_count"] = md.RetryCount
		payload["invalid_json"] = md.InvalidJSON
		payload["prompt_tokens"] = md.PromptTokens
		payload["candidates_tokens"] = md.CandidatesTokens
		payload["thinking_tokens"] = md.ThinkingTokens
		payload["total_tokens"] = md.TotalTokens
		payload["tool_use_tokens"] = md.ToolUseTokens
		payload["tours_count"] = md.ToursCount
		payload["standalones_count"] = md.StandalonesCount
		payload["parts_total"] = md.PartsTotal
		payload["thought_parts"] = md.ThoughtParts
		payload["text_parts"] = md.TextParts
		payload["web_search_queries"] = md.WebSearchQueriesList
		payload["grounding_chunk_urls"] = md.GroundingChunkURLs
		payload["rendered_parts_count"] = md.RenderedParts
		payload["url_context_retrieved"] = md.URLContextRetrieved
	}
	jb, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		t.Logf("raw response marshal failed for cell %d: %v", cellIdx, err)
		return
	}
	if err := os.WriteFile(path, jb, 0o644); err != nil {
		t.Logf("raw response write failed for cell %d: %v", cellIdx, err)
	}
}

// scoreCell computes precision / recall / per-field accuracy by matching
// returned events to fixture events on (LocalDate, NormalizedVenue).
//
// Recall is split two ways:
//   - "all"    — denominator is every in-scope fixture event.
//   - "public" — drops members-only entries (Google Search can't reach them).
//
// In both cases, excluded_per_spec fixture entries (multi-artist festivals)
// are removed from the denominator: they exist in the fixture solely as
// negative samples. Returning one bumps `FestivalLeaks` rather than
// `MatchedCount`, and is excluded from the precision denominator so that
// "stayed silent on excluded events" doesn't punish precision.
//
// FalsePositives = returned events not matching any fixture entry at all
// (either in-scope or excluded). These are the worst kind — pure hallucinations
// or wrong-venue/date matches.
func scoreCell(res *cellResult, got []*entity.ScrapedConcert, fixture []gemini.GroundTruthEvent) {
	fixByKey := make(map[gemini.MatchKey]gemini.GroundTruthEvent, len(fixture))
	for _, f := range fixture {
		fixByKey[f.Key()] = f
	}

	var matchedInScope, publicMatched, festivalLeaks, falsePositives int
	var sums aggregateFieldAccuracy

	for _, sc := range got {
		key := gemini.KeyForScraped(sc)
		fx, ok := fixByKey[key]
		if !ok {
			falsePositives++
			continue
		}
		if fx.ExcludedPerSpec {
			festivalLeaks++
			continue
		}
		matchedInScope++
		if fx.Visibility != "members-only" {
			publicMatched++
		}
		acc := gemini.CompareEvent(sc, fx)
		if acc.Venue {
			sums.Venue++
		}
		if acc.AdminArea {
			sums.AdminArea++
		}
		if acc.LocalDate {
			sums.LocalDate++
		}
		if acc.StartTime {
			sums.StartTime++
		}
		if acc.OpenTime {
			sums.OpenTime++
		}
		if acc.SourceURL {
			sums.SourceURL++
		}
	}

	// Denominators that exclude excluded_per_spec entries.
	var allTotal, publicTotal int
	for _, f := range fixture {
		if f.ExcludedPerSpec {
			continue
		}
		allTotal++
		if f.Visibility != "members-only" {
			publicTotal++
		}
	}

	res.MatchedCount = matchedInScope
	res.FalsePositives = falsePositives
	res.FestivalLeaks = festivalLeaks

	// Precision denominator: returned minus festival leaks (so silence on
	// excluded events neither helps nor hurts).
	precisionDenom := matchedInScope + falsePositives
	if precisionDenom > 0 {
		res.Precision = float64(matchedInScope) / float64(precisionDenom)
	}
	if allTotal > 0 {
		res.RecallAll = float64(matchedInScope) / float64(allTotal)
	}
	if publicTotal > 0 {
		res.RecallPublic = float64(publicMatched) / float64(publicTotal)
	}
	res.F1Public = harmonic(res.Precision, res.RecallPublic)
	res.F1All = harmonic(res.Precision, res.RecallAll)
	if matchedInScope > 0 {
		div := float64(matchedInScope)
		res.FieldAccuracy = aggregateFieldAccuracy{
			Venue:     sums.Venue / div,
			AdminArea: sums.AdminArea / div,
			LocalDate: sums.LocalDate / div,
			StartTime: sums.StartTime / div,
			OpenTime:  sums.OpenTime / div,
			SourceURL: sums.SourceURL / div,
		}
	}
}

func harmonic(p, r float64) float64 {
	if p+r == 0 {
		return 0
	}
	return 2 * p * r / (p + r)
}

func writeOutputs(t *testing.T, rf runFile) error {
	t.Helper()
	if err := os.MkdirAll(resultsDir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", resultsDir, err)
	}
	stamp := time.Now().UTC().Format("20060102T150405Z")
	jsonPath := filepath.Join(resultsDir, stamp+".json")
	csvPath := filepath.Join(resultsDir, stamp+".csv")

	jb, err := json.MarshalIndent(rf, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal results: %w", err)
	}
	if err := os.WriteFile(jsonPath, jb, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", jsonPath, err)
	}
	t.Logf("wrote %s", jsonPath)

	f, err := os.Create(csvPath)
	if err != nil {
		return fmt.Errorf("create %s: %w", csvPath, err)
	}
	defer func() { _ = f.Close() }()

	w := csv.NewWriter(f)
	defer w.Flush()

	if err := w.Write([]string{
		"model", "temperature", "thinking_level", "artist", "rep",
		"precision", "recall_public", "recall_all", "f1_public", "f1_all",
		"returned", "matched", "false_positives", "festival_leaks",
		"tours_count", "standalones_count",
		"parts_total", "thought_parts", "text_parts",
		"url_ctx_total", "url_ctx_success", "url_ctx_error", "url_ctx_other",
		"search_queries", "finish_reason", "finish_message", "avg_logprobs",
		"venue_acc", "admin_area_acc", "local_date_acc", "start_time_acc", "open_time_acc", "source_url_acc",
		"prompt_tokens", "candidates_tokens", "thinking_tokens", "tool_use_tokens", "total_tokens",
		"latency_ms", "cost_usd", "error",
	}); err != nil {
		return err
	}
	for _, c := range rf.Cells {
		row := []string{
			c.Model,
			strconv.FormatFloat(float64(c.Temperature), 'f', 2, 32),
			c.ThinkingLevel,
			c.ArtistName,
			strconv.Itoa(c.Repetition),
			strconv.FormatFloat(c.Precision, 'f', 4, 64),
			strconv.FormatFloat(c.RecallPublic, 'f', 4, 64),
			strconv.FormatFloat(c.RecallAll, 'f', 4, 64),
			strconv.FormatFloat(c.F1Public, 'f', 4, 64),
			strconv.FormatFloat(c.F1All, 'f', 4, 64),
			strconv.Itoa(c.ReturnedCount),
			strconv.Itoa(c.MatchedCount),
			strconv.Itoa(c.FalsePositives),
			strconv.Itoa(c.FestivalLeaks),
			strconv.Itoa(c.ToursCount),
			strconv.Itoa(c.StandalonesCount),
			strconv.Itoa(c.PartsTotal),
			strconv.Itoa(c.ThoughtParts),
			strconv.Itoa(c.TextParts),
			strconv.Itoa(c.URLContextTotal),
			strconv.Itoa(c.URLContextSuccess),
			strconv.Itoa(c.URLContextError),
			strconv.Itoa(c.URLContextOther),
			strconv.Itoa(c.GroundingSearchQueries),
			c.FinishReason,
			c.FinishMessage,
			strconv.FormatFloat(c.AvgLogprobs, 'f', 4, 64),
			strconv.FormatFloat(c.FieldAccuracy.Venue, 'f', 4, 64),
			strconv.FormatFloat(c.FieldAccuracy.AdminArea, 'f', 4, 64),
			strconv.FormatFloat(c.FieldAccuracy.LocalDate, 'f', 4, 64),
			strconv.FormatFloat(c.FieldAccuracy.StartTime, 'f', 4, 64),
			strconv.FormatFloat(c.FieldAccuracy.OpenTime, 'f', 4, 64),
			strconv.FormatFloat(c.FieldAccuracy.SourceURL, 'f', 4, 64),
			strconv.Itoa(int(c.PromptTokens)),
			strconv.Itoa(int(c.CandidatesTokens)),
			strconv.Itoa(int(c.ThinkingTokens)),
			strconv.Itoa(int(c.ToolUseTokens)),
			strconv.Itoa(int(c.TotalTokens)),
			strconv.FormatInt(c.LatencyMillis, 10),
			strconv.FormatFloat(c.CostUSD, 'f', 6, 64),
			c.Error,
		}
		if err := w.Write(row); err != nil {
			return err
		}
	}
	t.Logf("wrote %s", csvPath)
	return nil
}
