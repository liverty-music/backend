//go:build integration

package gemini_test

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/infrastructure/gcp/gemini"
	"github.com/pannpers/go-logging/logging"
)

const (
	groundingEvalEnvVar        = "GEMINI_GROUNDING_EVAL"            // "1" enables the run
	groundingEvalVariantEnvVar = "GEMINI_GROUNDING_EVAL_VARIANT"    // FINAL (production, default), FINAL_TP, E2UJ, or E2UJA (one variant per run)
	groundingEvalRepsEnvVar    = "GEMINI_GROUNDING_EVAL_REPS"       // optional repetition override (e.g. 1 for a smoke run)
	groundingEvalThinkEnvVar   = "GEMINI_GROUNDING_EVAL_THINKING"   // optional thinking level override (default low)
	groundingEvalArtistEnvVar  = "GEMINI_GROUNDING_EVAL_ARTIST"     // optional fixture artist name (default Vaundy)
	groundingEvalTempEnvVar    = "GEMINI_GROUNDING_EVAL_TEMP"       // optional temperature; temperature is not sent when unset
	groundingEvalToolCallsEnv  = "GEMINI_GROUNDING_EVAL_TOOL_CALLS" // "1" returns server-side tool calls (search queries)

	groundingEvalArtist   = "Vaundy"
	groundingEvalModel    = "gemini-3.8-flash"
	groundingEvalThinking = "low"
	groundingEvalReps     = 3
)

// promptMinimal is the user prompt of the E2UJ / E2UJA variants (the
// production prompt template).
const promptMinimal = `Extract tours and standalone shows by %[2]s taking place on or after %[1]s.

Official site: %[3]s
`

// systemInstructionJSON is the E2UJ system instruction: the production
// scope and sources plus explicit tool-usage and extraction rules.
const systemInstructionJSON = `You are a data-extraction agent for a live-music information system. Extract official concert information for the given artist.

Scope:
- Concerts and tours organized by the artist that take place on or after the given start date.
- Tours and shows: emit one entry in "series" per tour or show, with one event per date. Include solo one-off shows, fan-club-only shows, and 2-4 act named co-headliner bills (対バン).
- Exclude music festivals and other multi-artist events where the artist is one of many performers.

Sources: use only the official site given in the prompt, or official tour-specific pages. Do not use third-party sites.

Tool usage: read page content with the url_context tool, starting from the official site URL. Use google_search only to discover the URL of a page you cannot reach otherwise; never use google_search to look up details (dates, venues, open/start times) that are on a page you can read.

Extraction rules:
- title, source_url and venue MUST be copied verbatim (character for character) in their ORIGINAL LANGUAGE as printed on the source page. Do NOT translate, romanize, or localize them. Even when a page offers a multilingual or English view, always use the Japanese-language form.
- source_url: the official page dedicated to THIS specific tour/show (a tour feature page or the news article announcing it).
- local_date, open_time, start_time and admin_area follow the formats in the response schema. Use "" when the page does not provide the information.
- Treat concerts that share the same venue, local_date, and start_time as duplicates and drop them.

Respond with JSON that follows the response schema.
`

// systemInstructionJSONReadFirst is systemInstructionJSON with a mandatory
// first action placed at the very top (prompting guide: put essential
// behavioral constraints at the beginning of the system instruction), to
// make the model read the official site with url_context before searching.
var systemInstructionJSONReadFirst = strings.Replace(systemInstructionJSON,
	"Extract official concert information for the given artist.\n",
	"Extract official concert information for the given artist.\n\n"+
		"MANDATORY FIRST STEP: before any google_search call, read the official site URL given in the prompt with the url_context tool. Then read, with url_context, the official pages it links to that list concerts (live, schedule, tour, or news pages). Call google_search only if a page you still need cannot be reached this way.\n", 1)

// groundingVariant returns the prompt for a variant; nil means the
// production prompt.
func groundingVariant(t *testing.T, name string) *gemini.Prompt {
	t.Helper()
	switch name {
	case "FINAL":
		// Production configuration.
		return nil
	case "E2UJ":
		// Explicit url_context-first tool-usage and extraction rules.
		return &gemini.Prompt{SystemInstruction: systemInstructionJSON, Template: promptMinimal}
	case "E2UJA":
		// E2UJ with the url_context-first instruction at the top.
		return &gemini.Prompt{SystemInstruction: systemInstructionJSONReadFirst, Template: promptMinimal}
	default:
		t.Fatalf("%s must be FINAL, E2UJ, or E2UJA (got %q)", groundingEvalVariantEnvVar, name)
		return nil
	}
}

// TestConcertSearcher_GroundingVariants measures how prompt variants affect
// grounding (search-query fan-out) and recall on one fixture artist. Set
// GEMINI_GROUNDING_EVAL_TOOL_CALLS=1 to read the per-call search queries from
// the server-side tool calls; otherwise run one variant per invocation, each
// in its own clock hour, so the billing export's hourly "search query" SKU
// count isolates the variant (groundingMetadata is absent on Gemini 3).
//
// The scope scenarios are checked per run: festival entries are
// excluded_per_spec negatives in the fixture (a returned one counts as a
// leak), co-headliner bills organized by the artist are in-scope standalones,
// and a cancelled show the model returns appears as a false positive (the
// fixture holds no cancelled shows), to be confirmed against the raw output.
//
// @spec components/entity/concert/search "Festival left out"
// @spec components/entity/concert/search "Cancelled show left out"
// @spec components/entity/concert/search "Co-headliner bill organized by the artist"
//
// Venue text is scored by normalized match against the fixture, so a venue
// copied from a page's English version or carrying a show subtitle counts as
// a miss.
//
// @spec components/entity/concert/search "Default language of a multilingual tour page"
// @spec components/entity/concert/search "Show subtitle kept out of the venue"
//
// The fixture also covers source text as written: Vaundy's tour pages offer
// English and Korean views (scored against the Japanese venue names), and its
// JAPAN ARENA TOUR 2027-2028 lists dates without a year after 2027 dates
// (scored by exact local date).
//
// @spec components/entity/concert/search "Japanese venue on a multilingual page"
// @spec components/entity/concert/search "Year inferred from a two-year tour title"
func TestConcertSearcher_GroundingVariants(t *testing.T) {
	if os.Getenv(groundingEvalEnvVar) != "1" {
		t.Skipf("set %s=1 to run the grounding variant evaluation", groundingEvalEnvVar)
	}
	if os.Getenv(abEvalAPIKeyVar) == "" {
		t.Fatalf("%s is required", abEvalAPIKeyVar)
	}
	variant := strings.ToUpper(strings.TrimSpace(os.Getenv(groundingEvalVariantEnvVar)))
	if variant == "" {
		variant = "FINAL"
	}

	reps := groundingEvalReps
	if v := strings.TrimSpace(os.Getenv(groundingEvalRepsEnvVar)); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			t.Fatalf("%s must be a positive integer (got %q)", groundingEvalRepsEnvVar, v)
		}
		reps = n
	}

	thinking := groundingEvalThinking
	if v := strings.TrimSpace(os.Getenv(groundingEvalThinkEnvVar)); v != "" {
		thinking = v
	}
	artistName := groundingEvalArtist
	if v := strings.TrimSpace(os.Getenv(groundingEvalArtistEnvVar)); v != "" {
		artistName = v
	}
	prompt := groundingVariant(t, variant)
	// Temperature is not sent unless set: the Gemini 3.8 Flash migration
	// guide says to strip it from generation configs.
	var temp float32
	omitTemp := true
	if v := strings.TrimSpace(os.Getenv(groundingEvalTempEnvVar)); v != "" {
		f, err := strconv.ParseFloat(v, 32)
		if err != nil {
			t.Fatalf("%s must be a number (got %q)", groundingEvalTempEnvVar, v)
		}
		temp, omitTemp = float32(f), false
	}

	gt, err := gemini.LoadGroundTruth()
	if err != nil {
		t.Fatalf("load ground truth: %v", err)
	}
	from, err := time.Parse("2006-01-02", gt.EvaluationFrom)
	if err != nil {
		t.Fatalf("parse evaluation_from: %v", err)
	}
	var artist gemini.GroundTruthArtist
	for _, a := range gt.Artists {
		if a.Name == artistName {
			artist = a
		}
	}
	if artist.ID == "" {
		t.Fatalf("artist %s not in fixture", artistName)
	}
	// The prompt's start date is today (JST), so fixture dates that have
	// already passed are no longer expected.
	today := time.Now().In(time.FixedZone("JST", 9*60*60)).Format("2006-01-02")
	upcoming := artist.Events[:0:0]
	for _, e := range artist.Events {
		if e.LocalDate >= today {
			upcoming = append(upcoming, e)
		}
	}
	artist.Events = upcoming
	ctx := context.Background()
	logger, err := logging.New()
	if err != nil {
		t.Fatalf("create logger: %v", err)
	}

	startedAt := time.Now().UTC()
	runStamp := startedAt.Format("20060102T150405Z")
	rawDir := filepath.Join(resultsDir, runStamp+"_raw")
	if err := os.MkdirAll(rawDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", rawDir, err)
	}

	results := make([]cellResult, 0, reps)
	totalCost := 0.0
	for r := 0; r < reps; r++ {
		cell := abCell{
			Model:           groundingEvalModel,
			Temperature:     temp,
			Thinking:        thinking,
			Artist:          artist,
			Repetition:      r,
			Variant:         variant,
			Prompt:          prompt,
			OmitTemperature: omitTemp,

			IncludeToolInvocations: os.Getenv(groundingEvalToolCallsEnv) == "1",
		}
		res := runCell(ctx, t, logger, cell, from, rawDir, r+1)
		t.Logf("artist=%s variant=%s temp=%s thinking=%s rep=%d recall_public=%.2f precision=%.2f returned=%d matched=%d fp=%d leaks=%d latency=%dms err=%q",
			artist.Name, variant, tempLabel(temp, omitTemp), thinking, r, res.RecallPublic, res.Precision, res.ReturnedCount, res.MatchedCount,
			res.FalsePositives, res.FestivalLeaks, res.LatencyMillis, res.Error)
		results = append(results, res)
		totalCost += res.CostUSD
	}

	meta := runMeta{
		SDKVersion:     "google.golang.org/genai@v1.69.0",
		EvaluationFrom: gt.EvaluationFrom,
		StartedAt:      startedAt.Format(time.RFC3339),
		FinishedAt:     time.Now().UTC().Format(time.RFC3339),
		CellsExecuted:  len(results),
		TotalCostUSD:   totalCost,
	}
	if err := writeOutputs(t, runFile{RunMetadata: meta, Cells: results}); err != nil {
		t.Fatalf("write outputs: %v", err)
	}
	t.Logf("variant %s complete: %d cells, started %s, finished %s (match against billing-export hour)",
		variant, len(results), meta.StartedAt, meta.FinishedAt)
}

// tempLabel renders the temperature for logs ("unset" when not sent).
func tempLabel(temp float32, omit bool) string {
	if omit {
		return "unset"
	}
	return strconv.FormatFloat(float64(temp), 'f', 1, 32)
}
