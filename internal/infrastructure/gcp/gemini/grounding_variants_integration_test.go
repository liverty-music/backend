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
	groundingEvalVariantEnvVar = "GEMINI_GROUNDING_EVAL_VARIANT"    // A (production baseline), C, D, D2, E, E2, E2U, E2UJ, E2UJA, FINAL, or E2P (one variant per run)
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

// groundingEvalSince is the "last search date" for variants D and E. The only
// Vaundy concerts first announced on or after it are the two Yokohama Arena
// dates (announced 2026-09-06), which makes the expected output exact.
var groundingEvalSince = time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)

// groundingEvalSinceDates are the fixture dates expected for variants D, D2,
// and E. D2 uses an earlier cutoff (2026-08-03) with the same expected set: the
// only other Vaundy item announced since then is バズリズム LIVE 2026 (an
// excluded multi-artist event), and the Zepp Sapporo shows announced on 08-03
// were held on 08-12/13.
var groundingEvalSinceDates = map[string]bool{"2027-11-27": true, "2027-11-28": true}

// systemInstructionMinimal is the simplified Step 1 system instruction under
// evaluation. It drops the exhaustive-discovery workflow ("comprehensively
// discover", off-domain pages, MECE verification) and restricts sources to
// official pages, while keeping the output format and verbatim rules that
// Step 2 and the Go-side XML parser depend on.
const systemInstructionMinimal = `You are a data-extraction agent for a live-music information system. Extract official concert information for the given artist.

Scope:
- Concerts and tours organized by the artist that take place on or after the given start date.
- Tours: multi-venue / multi-date runs. Emit one <tour> block per tour, with one <event> per date.
- Standalone shows: solo one-off shows, fan-club-only shows, and 2-4 act named co-headliner bills (対バン). Emit one <standalone> block with a single <event>.
- Exclude music festivals and other multi-artist events where the artist is one of many performers.

Sources: use only the official site given in the prompt, or official tour-specific pages. Do not use third-party sites.

Output format:

<extracted>
  <tour>
    <title>UVERworld TYCOON LIVE -DOCUMENT-</title>
    <source_url>https://www.uverworld.jp/feature/2026_live</source_url>
    <event>
      <venue>Zepp Nagoya</venue>
      <country>JP</country>
      <local_date>2026年3月15日(土)</local_date>
      <open_time>開場 17:00</open_time>
      <start_time>開演 18:00</start_time>
    </event>
  </tour>
  <standalone>
    <title>UVERworld 武道館単独公演 2026</title>
    <source_url>https://www.uverworld.jp/news/detail/budokan</source_url>
    <event>
      <venue>日本武道館</venue>
      <country>JP</country>
      <local_date>2026/04/01</local_date>
      <open_time></open_time>
      <start_time>19:00</start_time>
    </event>
  </standalone>
</extracted>

Extraction rules:
- source_url: the official page dedicated to THIS specific tour/show (a tour feature page or the news article announcing it).
- country: the ISO 3166-1 alpha-2 code of the country where the concert is held.
- Every field except country MUST be copied verbatim (character for character) in its ORIGINAL LANGUAGE as printed on the source page. Do NOT translate, romanize, or localize any value. Extract venue names in their native language exactly as written. Even when a page offers a multilingual or English view, always extract the Japanese-language form.
- Leave a tag empty when the page does not provide that information.
- When local_date has no year, infer the year from page context and prepend it to the verbatim date.
- Treat concerts that share the same venue, local_date, and start_time as duplicates and drop them.

Respond with the XML only — no extra text.
`

// promptMinimal is the variant C / E user prompt. Placeholders follow the
// Step1Slice contract: from_date, artist name, official site URL.
const promptMinimal = `Extract tours and standalone shows by %[2]s taking place on or after %[1]s.

Official site: %[3]s
`

// promptAnnounced is the variant D user prompt: promptMinimal plus an
// announcement-date filter that keeps undated concerts.
const promptAnnounced = `Extract tours and standalone shows by %[2]s taking place on or after %[1]s.
Only include concerts first announced on or after 2026-09-01. If the announcement date cannot be determined, include the concert.

Official site: %[3]s
`

// promptAnnouncedWithUpdates is the variant D2 user prompt. Variant D dropped
// dates newly added to an already-announced tour because the model judged the
// announcement date per tour; D2 makes per-date additions and newly announced
// venues / times explicitly in scope. It carries a single date (the
// announcement cutoff) and no event-date bound; past dates are dropped
// downstream by the from-date filter. The from_date argument is unused.
const promptAnnouncedWithUpdates = `Extract tours and standalone shows by %[2]s first announced on or after 2026-08-03, including added dates and changes to venues or schedules. If the announcement date cannot be determined, include the concert.

Official site: %[3]s
`

// systemInstructionAnnounced is systemInstructionMinimal without the
// event-date bound, which the D2 prompt no longer supplies.
var systemInstructionAnnounced = strings.Replace(systemInstructionMinimal,
	"- Concerts and tours organized by the artist that take place on or after the given start date.",
	"- Concerts and tours organized by the artist.", 1)

// systemInstructionReadWithURLContext is systemInstructionMinimal plus a
// tool-usage rule: the captured search queries showed the model using
// google_search to read page details (e.g. site: queries per show for open
// times) instead of reading the pages with url_context.
var systemInstructionReadWithURLContext = strings.Replace(systemInstructionMinimal,
	"Sources: use only the official site given in the prompt, or official tour-specific pages. Do not use third-party sites.\n",
	"Sources: use only the official site given in the prompt, or official tour-specific pages. Do not use third-party sites.\n\n"+
		"Tool usage: read page content with the url_context tool, starting from the official site URL. Use google_search only to discover the URL of a page you cannot reach otherwise; never use google_search to look up details (dates, venues, open/start times) that are on a page you can read.\n", 1)

// systemInstructionJSON is the single-step (variant E2UJ) system
// instruction: the E2U scope, sources and tool-usage rules, with the XML
// envelope replaced by the structured-output schema and the Step 2 coercion
// rules folded in.
const systemInstructionJSON = `You are a data-extraction agent for a live-music information system. Extract official concert information for the given artist.

Scope:
- Concerts and tours organized by the artist that take place on or after the given start date.
- Tours: multi-venue / multi-date runs. Emit one entry in "tours" per tour, with one event per date.
- Standalone shows: solo one-off shows, fan-club-only shows, and 2-4 act named co-headliner bills (対バン). Emit one entry in "standalones" with exactly one event.
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

// systemInstructionFinal is the candidate production instruction:
// systemInstructionJSON (mild url_context guidance, no forced first step)
// with the scope wording fixed so co-headliner bills hosted by another
// artist are in scope (they were dropped as "not organized by the artist").
var systemInstructionFinal = strings.Replace(systemInstructionJSON,
	"- Concerts and tours organized by the artist that take place on or after the given start date.\n",
	"- Concerts and tours the artist performs in that take place on or after the given start date: the artist's own tours and shows, and 2-4 act co-headliner bills even when another artist hosts them.\n", 1)

// groundingEvalSchedulePages maps fixture artists to their official
// live-schedule page (variant E2P).
var groundingEvalSchedulePages = map[string]string{
	"UVERworld":    "https://www.uverworld.jp/feature/2026_live",
	"Vaundy":       "https://vaundy.jp/live/",
	"SUPER BEAVER": "https://sp.super-beaver.com/live_information/list/?range=future&sort=asc",
	"BRADIO":       "https://bradio.jp/show/?range=future_event_end_time&sort=asc",
}

// groundingVariant returns the Step 1 slices for a variant and whether the
// fixture must be narrowed to the concerts announced since groundingEvalSince.
func groundingVariant(t *testing.T, name, artistName string) ([]gemini.Step1Slice, bool) {
	t.Helper()
	base := gemini.Step1Slice{
		Name:              "all",
		SystemInstruction: systemInstructionMinimal,
		PromptTemplate:    promptMinimal,
		UseFullURL:        true,
	}
	switch name {
	case "A":
		// Baseline: the production Step 1 slices (current prompt, bare host).
		return nil, false
	case "C":
		return []gemini.Step1Slice{base}, false
	case "D":
		base.PromptTemplate = promptAnnounced
		return []gemini.Step1Slice{base}, true
	case "D2":
		base.SystemInstruction = systemInstructionAnnounced
		base.PromptTemplate = promptAnnouncedWithUpdates
		return []gemini.Step1Slice{base}, true
	case "E":
		base.SearchStart = groundingEvalSince
		return []gemini.Step1Slice{base}, true
	case "E2":
		// All concerts on or after today (no announcement-date condition), with
		// the GoogleSearch window narrowed to the last 2 months.
		base.SearchStart = time.Now().UTC().AddDate(0, -2, 0)
		return []gemini.Step1Slice{base}, false
	case "E2U":
		// E2 plus an instruction to read pages with url_context and use
		// google_search only to discover URLs.
		base.SearchStart = time.Now().UTC().AddDate(0, -2, 0)
		base.SystemInstruction = systemInstructionReadWithURLContext
		return []gemini.Step1Slice{base}, false
	case "E2UJ":
		// E2U with the parse step folded in: structured JSON output, no Step 2.
		base.SearchStart = time.Now().UTC().AddDate(0, -2, 0)
		base.SystemInstruction = systemInstructionJSON
		base.JSONOutput = true
		return []gemini.Step1Slice{base}, false
	case "E2UJA":
		// E2UJ with the url_context-first instruction at the top.
		base.SearchStart = time.Now().UTC().AddDate(0, -2, 0)
		base.SystemInstruction = systemInstructionJSONReadFirst
		base.JSONOutput = true
		return []gemini.Step1Slice{base}, false
	case "FINAL":
		// Candidate production configuration.
		base.SearchStart = time.Now().UTC().AddDate(0, -2, 0)
		base.SystemInstruction = systemInstructionFinal
		base.JSONOutput = true
		return []gemini.Step1Slice{base}, false
	case "E2P":
		// E2 plus the artist's official live-schedule page URL in the prompt.
		page, ok := groundingEvalSchedulePages[artistName]
		if !ok {
			t.Fatalf("no live schedule page registered for %s", artistName)
		}
		base.SearchStart = time.Now().UTC().AddDate(0, -2, 0)
		base.PromptTemplate = promptMinimal + "Live schedule page: " + page + "\n"
		return []gemini.Step1Slice{base}, false
	default:
		t.Fatalf("%s must be A, C, D, D2, E, E2, E2U, E2UJ, E2UJA, FINAL, or E2P (got %q)", groundingEvalVariantEnvVar, name)
		return nil, false
	}
}

// TestConcertSearcher_GroundingVariants measures how Step 1 prompt and search
// window variants affect grounding (search-query fan-out) and recall on
// Vaundy. Run one variant per invocation, each in its own clock hour, so the
// billing export's hourly "search query" SKU count isolates the variant: the
// response carries no per-call search count (groundingMetadata is absent).
func TestConcertSearcher_GroundingVariants(t *testing.T) {
	if os.Getenv(groundingEvalEnvVar) != "1" {
		t.Skipf("set %s=1 to run the grounding variant evaluation", groundingEvalEnvVar)
	}
	if os.Getenv(abEvalAPIKeyVar) == "" {
		t.Fatalf("%s is required", abEvalAPIKeyVar)
	}
	variant := strings.ToUpper(strings.TrimSpace(os.Getenv(groundingEvalVariantEnvVar)))

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
	slices, narrow := groundingVariant(t, variant, artistName)
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
	if narrow {
		// Keep the in-scope events announced since groundingEvalSince plus the
		// excluded entries, so festival leaks are still classified as such.
		kept := artist.Events[:0:0]
		for _, e := range artist.Events {
			if groundingEvalSinceDates[e.LocalDate] || e.ExcludedPerSpec {
				kept = append(kept, e)
			}
		}
		artist.Events = kept
	}

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
			Slices:          slices,
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
