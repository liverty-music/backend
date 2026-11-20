//go:build integration

package gemini_test

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
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
	abEvalEnvVar         = "GEMINI_AB_EVAL"
	abEvalSmokeEnvVar    = "GEMINI_AB_EVAL_SMOKE"
	abEvalAPIKeyVar      = "GCP_GEMINI_SEARCH_API_KEY" // REQUIRED: Gemini API direct is the only supported backend
	abEvalModelsEnvVar   = "GEMINI_AB_EVAL_MODELS"     // CSV of model IDs; empty = gemini-4-hydrogen
	abEvalArtistsEnvVar  = "GEMINI_AB_EVAL_ARTISTS"    // CSV of artist names; empty = all fixture artists
	abEvalThinkingEnvVar = "GEMINI_AB_EVAL_THINKING"   // thinking level; "" = default low
	abEvalVariantsEnvVar = "GEMINI_AB_EVAL_VARIANTS"   // CSV of variants (see abVariant); empty = base
	abEvalRepsEnvVar     = "GEMINI_AB_EVAL_REPS"       // repetitions per cell; empty = 3

	// abSearchCapLine is the search-cap prompt line of the "cap" variant,
	// appended to the production system instruction.
	abSearchCapLine = "\nRun at most 10 Google Search queries; read the listed pages with url_context first.\n"

	// resultsDir is relative to this package. Output filenames embed an
	// RFC3339Nano UTC timestamp to disambiguate concurrent runs.
	resultsDir = "testdata/ab_results"
)

// abVariant is one searcher setup of the A/B matrix, named by its options
// joined with "+": "base" (production: the official top page's links listed
// in the prompt), "cap" (abSearchCapLine added to the system instruction) and
// "nosearch" (no Google Search tool; URL context only). For example
// "cap+nosearch".
//
// pass-tour-detail-pages-to-concert-search also evaluated listing the links
// of the first 3 concert pages ("detail"); it raised search queries and cost
// and was removed (testdata/README.md, 2026-11-20).
type abVariant struct {
	Name     string
	Cap      bool
	NoSearch bool
}

// parseABVariant parses a variant name such as "detail+cap".
func parseABVariant(name string) (abVariant, error) {
	v := abVariant{Name: name}
	for _, opt := range strings.Split(name, "+") {
		switch strings.TrimSpace(opt) {
		case "base":
		case "cap":
			v.Cap = true
		case "nosearch":
			v.NoSearch = true
		default:
			return abVariant{}, fmt.Errorf("unknown variant option %q in %q (want base, cap, nosearch)", opt, name)
		}
	}
	return v, nil
}

// apply sets the searcher setup of v on cell.
func (v abVariant) apply(cell *abCell) {
	cell.Variant = v.Name
	cell.PageLinks = true
	if v.Cap {
		p := gemini.DefaultPrompt
		p.SystemInstruction += abSearchCapLine
		cell.Prompt = &p
	}
	cell.WithoutGoogleSearch = v.NoSearch
}

// csvEnv returns the trimmed, non-empty entries of a CSV environment
// variable, or def when it is unset.
func csvEnv(name string, def ...string) []string {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return def
	}
	var out []string
	for _, v := range strings.Split(raw, ",") {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// abMatrix returns the Cartesian product of the matrix axes plus repetitions.
//
// The default matrix runs the "base" variant on every fixture artist:
//   - Model: gemini-4-hydrogen (production)
//   - Temperature: not sent (Gemini 3.8 Flash migration guide)
//   - Thinking: low
//   - Repetitions: 3
//
// Every cell lists the official site's pages in the prompt and records the
// search queries from the server-side tool calls.
//
// Environment overrides:
//   - GEMINI_AB_EVAL_SMOKE=1 collapses to a single cell for an auth/API ping:
//     the first model, variant and artist.
//   - GEMINI_AB_EVAL_MODELS=csv replaces the model list.
//   - GEMINI_AB_EVAL_VARIANTS=csv replaces the variant list (abVariant).
//   - GEMINI_AB_EVAL_REPS=n replaces the repetitions.
//   - GEMINI_AB_EVAL_ARTISTS=csv intersects the artist list by Name.
func abMatrix(t *testing.T, artists []gemini.GroundTruthArtist) []abCell {
	t.Helper()
	// Apply the artist filter BEFORE smoke collapse so smoke mode can
	// target a specific artist (e.g. GEMINI_AB_EVAL_ARTISTS=Vaundy).
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

	models := csvEnv(abEvalModelsEnvVar, "gemini-4-hydrogen")
	thinks := csvEnv(abEvalThinkingEnvVar, "low")
	var variants []abVariant
	for _, name := range csvEnv(abEvalVariantsEnvVar, "base") {
		v, err := parseABVariant(name)
		if err != nil {
			t.Fatalf("%s: %v", abEvalVariantsEnvVar, err)
		}
		variants = append(variants, v)
	}
	reps := 3
	if v := strings.TrimSpace(os.Getenv(abEvalRepsEnvVar)); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			t.Fatalf("%s must be a positive integer (got %q)", abEvalRepsEnvVar, v)
		}
		reps = n
	}
	if os.Getenv(abEvalSmokeEnvVar) == "1" && len(artists) > 0 {
		models, thinks, variants, artists, reps = models[:1], thinks[:1], variants[:1], artists[:1], 1
	}

	// Repetitions are the outer loop so that the variants of one artist and
	// repetition run minutes apart, not one variant after another: the
	// official pages and the search index then change for all variants alike.
	var cells []abCell
	for r := 0; r < reps; r++ {
		for _, a := range artists {
			for _, m := range models {
				for _, th := range thinks {
					for _, v := range variants {
						cell := abCell{
							Model:                  m,
							OmitTemperature:        true,
							Thinking:               th,
							Artist:                 a,
							Repetition:             r,
							IncludeToolInvocations: true,
						}
						v.apply(&cell)
						cells = append(cells, cell)
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
	// Variant labels a prompt variant; Prompt, when non-nil, replaces the
	// production prompt for this cell.
	Variant string
	Prompt  *gemini.Prompt
	// OmitTemperature leaves temperature unset in requests (Temperature is
	// then ignored), per the Gemini 3.8 Flash migration guide.
	OmitTemperature bool
	// IncludeToolInvocations returns the server-side tool calls (search
	// queries) in the response.
	IncludeToolInvocations bool
	// PageLinks lists the concert pages the official top page links to in
	// the prompt, as production does. LinkProfile, when non-nil, replaces
	// the production link profile.
	PageLinks   bool
	LinkProfile *gemini.LinkProfile
	// WithoutGoogleSearch leaves the Google Search tool out of the call.
	WithoutGoogleSearch bool
}

type cellResult struct {
	Model         string  `json:"model"`
	Temperature   float32 `json:"temperature"`
	ThinkingLevel string  `json:"thinking_level"`
	ArtistID      string  `json:"artist_id"`
	ArtistName    string  `json:"artist_name"`
	Repetition    int     `json:"repetition"`
	Variant       string  `json:"variant,omitempty"`
	OmitTemp      bool    `json:"omit_temperature,omitempty"`
	// Precision and recall split.
	// Precision = matched_public / (returned - festival_leaks)
	//   (a festival leak is a returned event matching an excluded_per_spec
	//    fixture entry — counted separately, not as a generic FP)
	// Recall = matched_public / public_fixture_count
	//   (excluded_per_spec fixture entries are removed from the denominator)
	Precision        float64                `json:"precision"`
	RecallPublic     float64                `json:"recall_public"`
	RecallAll        float64                `json:"recall_all"`
	F1Public         float64                `json:"f1_public"`
	F1All            float64                `json:"f1_all"`
	FalsePositives   int                    `json:"false_positives"` // returned, not matching any fixture entry
	FestivalLeaks    int                    `json:"festival_leaks"`  // returned, matching an excluded_per_spec entry
	FieldAccuracy    aggregateFieldAccuracy `json:"field_accuracy"`
	PromptTokens     int32                  `json:"prompt_tokens"`
	CandidatesTokens int32                  `json:"candidates_tokens"`
	ThinkingTokens   int32                  `json:"thinking_tokens"`
	ToolUseTokens    int32                  `json:"tool_use_tokens"`
	TotalTokens      int32                  `json:"total_tokens"`
	LatencyMillis    int64                  `json:"latency_ms"`
	ReturnedCount    int                    `json:"returned_count"`
	MatchedCount     int                    `json:"matched_count"`
	// Series classified TOUR / SINGLE (by venue count), pre-flatten.
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
	// URLContextCalls / URLContextSucceeded count from the server-side tool
	// parts, which also hold fetches that only failed.
	URLContextCalls     int `json:"url_context_calls"`
	URLContextSucceeded int `json:"url_context_succeeded"`
	// LinkedPages is the number of official concert pages listed in the
	// prompt.
	LinkedPages int `json:"linked_pages"`
	// GroundingSearchQueries is the number of GoogleSearch queries the model
	// issued — billed separately at $14/1K (5,000/month free, shared across
	// Gemini 3.x). Together with URLContextTotal this is the full grounding
	// spend picture for the cell.
	GroundingSearchQueries int `json:"grounding_search_queries"`
	// Truncation diagnostics: even when FinishReason is "STOP" the API can
	// silently truncate the JSON candidate mid-emission. FinishMessage and
	// AvgLogprobs let us cross-check. These mirror Pass 2 values.
	FinishReason  string  `json:"finish_reason"`
	FinishMessage string  `json:"finish_message"`
	AvgLogprobs   float64 `json:"avg_logprobs"`
	// URLs the model fetched with URL context.
	DiscoveredURLCount int      `json:"discovered_url_count"`
	DiscoveredURLs     []string `json:"discovered_urls,omitempty"`
	CostUSD            float64  `json:"cost_usd"`
	// CostUSD2027 is the cost at the price from 2027-01-01 (Pricing2027),
	// 0 for a model without one.
	CostUSD2027 float64 `json:"cost_usd_2027"`
	// RetryCount is the number of attempts of the grounded call.
	RetryCount int `json:"retry_count"`
	// EmptyResponse is true when the call answered with no candidate or
	// ended without text.
	EmptyResponse bool `json:"empty_response"`
	// GroundingChunks is the number of groundingChunks of the response
	// (backend #772: absent on some Gemini 3 responses).
	GroundingChunks int    `json:"grounding_chunks"`
	Error           string `json:"error,omitempty"`
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
	StartTime      string  `json:"started_at"`
	FinishTime     string  `json:"finished_at"`
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
	apiKey := os.Getenv(abEvalAPIKeyVar)
	if apiKey == "" {
		t.Fatalf("%s must be set; Gemini API direct is the only supported backend for the concert searcher", abEvalAPIKeyVar)
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

	for i := range gt.Artists {
		gt.Artists[i].Events = upcomingEvents(gt.Artists[i].Events)
	}
	cells := abMatrix(t, gt.Artists)
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
			SDKVersion:     "google.golang.org/genai@v1.69.0",
			EvaluationFrom: gt.EvaluationFrom,
			StartTime:      startedAt.Format(time.RFC3339),
			FinishTime:     time.Now().UTC().Format(time.RFC3339),
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
		t.Logf("[%d/%d] model=%s thinking=%s variant=%s artist=%s rep=%d",
			i+1, len(cells), cell.Model, cell.Thinking, cell.Variant, cell.Artist.Name, cell.Repetition)

		res := runCell(ctx, t, logger, cell, from, rawDir, i+1)
		t.Logf("  recall_public=%.2f queries=%d linked=%d cost_2027=$%.3f latency=%dms empty=%t chunks=%d err=%q",
			res.RecallPublic, res.GroundingSearchQueries, res.LinkedPages, res.CostUSD2027, res.LatencyMillis,
			res.EmptyResponse, res.GroundingChunks, res.Error)
		results = append(results, res)
		totalCost += res.CostUSD
		snapshot()
	}

	finishedAt := time.Now().UTC()
	meta := runMeta{
		SDKVersion:     "google.golang.org/genai@v1.69.0",
		EvaluationFrom: gt.EvaluationFrom,
		StartTime:      startedAt.Format(time.RFC3339),
		FinishTime:     finishedAt.Format(time.RFC3339),
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
		Variant:       cell.Variant,
		OmitTemp:      cell.OmitTemperature,
	}

	var pageClient *http.Client
	if cell.PageLinks {
		pageClient = gemini.NewOfficialPageClient()
	}
	s, err := gemini.NewConcertSearcher(ctx, gemini.Config{
		APIKey:          os.Getenv(abEvalAPIKeyVar),
		Model:           cell.Model,
		Temperature:     cell.Temperature,
		OmitTemperature: cell.OmitTemperature,
		ThinkingLevel:   cell.Thinking,

		IncludeServerSideToolInvocations: cell.IncludeToolInvocations,
	}, nil, pageClient, logger)
	if err != nil {
		res.Error = "construct searcher: " + err.Error()
		writeRawResponse(t, rawDir, cellIdx, cell, nil, nil, res.Error)
		return res
	}
	if cell.Prompt != nil {
		gemini.SetPrompt(s, *cell.Prompt)
	}
	if cell.LinkProfile != nil {
		gemini.SetLinkProfile(s, *cell.LinkProfile)
	}
	gemini.SetWithoutGoogleSearch(s, cell.WithoutGoogleSearch)

	artist := &entity.Artist{ID: cell.Artist.ID, Name: cell.Artist.Name}
	site := &entity.OfficialSite{URL: cell.Artist.OfficialSiteURL}

	start := time.Now()
	got, md, err := s.SearchExt(ctx, artist, site, from)
	res.LatencyMillis = time.Since(start).Milliseconds()
	if md != nil {
		res.ToursCount = md.ToursCount
		res.StandalonesCount = md.StandalonesCount
		res.DiscoveredURLs = md.DiscoveredURLs
		res.DiscoveredURLCount = md.DiscoveredURLCount
	}
	if md != nil && md.Grounded != nil {
		pm := md.Grounded
		res.PromptTokens = pm.PromptTokens
		res.CandidatesTokens = pm.CandidatesTokens
		res.ThinkingTokens = pm.ThinkingTokens
		res.ToolUseTokens = pm.ToolUseTokens
		res.TotalTokens = pm.TotalTokens
		res.PartsTotal = pm.PartsTotal
		res.ThoughtParts = pm.ThoughtParts
		res.TextParts = pm.TextParts
		res.GroundingSearchQueries = pm.WebSearchQueries
		res.FinishReason = pm.FinishReason
		res.FinishMessage = pm.FinishMessage
		res.AvgLogprobs = pm.AvgLogprobs
		res.URLContextTotal = len(pm.URLContextRetrieved)
		res.URLContextCalls = pm.URLContextCalls
		res.URLContextSucceeded = pm.URLContextSucceeded
		res.LinkedPages = len(pm.LinkedPageURLs)
		res.RetryCount = pm.RetryCount
		res.GroundingChunks = len(pm.GroundingChunkURLs)
		for _, u := range pm.URLContextRetrieved {
			switch {
			case strings.Contains(u.Status, "SUCCESS"):
				res.URLContextSuccess++
			case strings.Contains(u.Status, "ERROR") || strings.Contains(u.Status, "FAILED"):
				res.URLContextError++
			default:
				res.URLContextOther++
			}
		}
		// Charging on errored calls is correct: even invalid-JSON responses
		// bill every input/output/thinking token and search query.
		res.CostUSD = gemini.DefaultPricing.CostUSD(
			cell.Model,
			pm.PromptTokens,
			pm.CandidatesTokens,
			pm.ThinkingTokens,
			pm.ToolUseTokens,
			int32(pm.WebSearchQueries),
		)
		res.CostUSD2027 = gemini.Pricing2027.CostUSD(
			cell.Model,
			pm.PromptTokens,
			pm.CandidatesTokens,
			pm.ThinkingTokens,
			pm.ToolUseTokens,
			int32(pm.WebSearchQueries),
		)
	}

	errMsg := ""
	if err != nil {
		errMsg = err.Error()
		res.EmptyResponse = strings.Contains(errMsg, "no candidates") || strings.Contains(errMsg, "without text")
	}
	writeRawResponse(t, rawDir, cellIdx, cell, md, got, errMsg)

	if err != nil {
		res.Error = errMsg
		return res
	}

	// Flatten the series-grouped output into per-event scoring units, carrying
	// each series' SourceURL down onto its events.
	scored := gemini.FlattenForScoring(got)
	res.ReturnedCount = len(scored)
	scoreCell(&res, scored, cell.Artist.Events)
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
	parsed []*entity.DiscoveredSeries,
	errMsg string,
) {
	t.Helper()
	safeArtist := strings.ReplaceAll(cell.Artist.Name, " ", "_")
	variant := ""
	if cell.Variant != "" {
		variant = "_v-" + cell.Variant
	}
	temp := fmt.Sprintf("T%.1f", cell.Temperature)
	if cell.OmitTemperature {
		temp = "Tunset"
	}
	fname := fmt.Sprintf("cell_%03d_%s_%s_th-%s%s_%s_rep%d.json",
		cellIdx, cell.Model, temp, cell.Thinking, variant, safeArtist, cell.Repetition)
	path := filepath.Join(dir, fname)

	payload := map[string]any{
		"cell_index":      cellIdx,
		"model":           cell.Model,
		"temperature":     cell.Temperature,
		"thinking_level":  cell.Thinking,
		"artist_id":       cell.Artist.ID,
		"artist_name":     cell.Artist.Name,
		"official_site":   cell.Artist.OfficialSiteURL,
		"repetition":      cell.Repetition,
		"variant":         cell.Variant,
		"parsed_concerts": parsed,
		"error":           errMsg,
	}
	if md != nil {
		payload["invalid_json"] = md.InvalidJSON
		payload["draft_count"] = md.DraftCount
		payload["tours_count"] = md.ToursCount
		payload["standalones_count"] = md.StandalonesCount
		payload["discovered_urls"] = md.DiscoveredURLs
		payload["discovered_url_count"] = md.DiscoveredURLCount
		if md.Grounded != nil {
			payload["grounded"] = passMetadataPayload(md.Grounded)
		}
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

// passMetadataPayload renders a *gemini.PassMetadata as a serialisable map
// for inclusion in the per-cell raw response file. Keeps the JSON keys
// consistent with the cellResult / CSV column names.
func passMetadataPayload(pm *gemini.PassMetadata) map[string]any {
	return map[string]any{
		"prompt_tokens":         pm.PromptTokens,
		"candidates_tokens":     pm.CandidatesTokens,
		"thinking_tokens":       pm.ThinkingTokens,
		"tool_use_tokens":       pm.ToolUseTokens,
		"total_tokens":          pm.TotalTokens,
		"finish_reason":         pm.FinishReason,
		"finish_message":        pm.FinishMessage,
		"avg_logprobs":          pm.AvgLogprobs,
		"retry_count":           pm.RetryCount,
		"parts_total":           pm.PartsTotal,
		"thought_parts":         pm.ThoughtParts,
		"text_parts":            pm.TextParts,
		"raw_response_text":     pm.RawResponseText,
		"web_search_queries":    pm.WebSearchQueriesList,
		"grounding_chunk_urls":  pm.GroundingChunkURLs,
		"rendered_parts":        pm.RenderedParts,
		"url_context_retrieved": pm.URLContextRetrieved,
		"url_context_calls":     pm.URLContextCalls,
		"url_context_succeeded": pm.URLContextSucceeded,
		"linked_page_urls":      pm.LinkedPageURLs,
		"tool_report_dropped":   pm.ToolReportDropped,
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
func scoreCell(res *cellResult, got []gemini.ScoredEvent, fixture []gemini.GroundTruthEvent) {
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
		"model", "temperature", "thinking_level", "variant", "artist", "rep",
		"precision", "recall_public", "recall_all", "f1_public", "f1_all",
		"returned", "matched", "false_positives", "festival_leaks",
		"tours_count", "standalones_count",
		"parts_total", "thought_parts", "text_parts",
		"url_ctx_total", "url_ctx_success", "url_ctx_error", "url_ctx_other",
		"url_ctx_calls", "url_ctx_succeeded", "linked_pages",
		"search_queries", "finish_reason", "finish_message", "avg_logprobs",
		"discovered_url_count",
		"venue_acc", "admin_area_acc", "local_date_acc", "start_time_acc", "open_time_acc", "source_url_acc",
		"prompt_tokens", "candidates_tokens", "thinking_tokens", "tool_use_tokens", "total_tokens",
		"latency_ms", "cost_usd", "cost_usd_2027", "retry_count", "empty_response", "grounding_chunks", "error",
	}); err != nil {
		return err
	}
	for _, c := range rf.Cells {
		row := []string{
			c.Model,
			strconv.FormatFloat(float64(c.Temperature), 'f', 2, 32),
			c.ThinkingLevel,
			c.Variant,
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
			strconv.Itoa(c.URLContextCalls),
			strconv.Itoa(c.URLContextSucceeded),
			strconv.Itoa(c.LinkedPages),
			strconv.Itoa(c.GroundingSearchQueries),
			c.FinishReason,
			c.FinishMessage,
			strconv.FormatFloat(c.AvgLogprobs, 'f', 4, 64),
			strconv.Itoa(c.DiscoveredURLCount),

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
			strconv.FormatFloat(c.CostUSD2027, 'f', 6, 64),
			strconv.Itoa(c.RetryCount),
			strconv.FormatBool(c.EmptyResponse),
			strconv.Itoa(c.GroundingChunks),
			c.Error,
		}
		if err := w.Write(row); err != nil {
			return err
		}
	}
	t.Logf("wrote %s", csvPath)
	return nil
}
