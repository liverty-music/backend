package gemini

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/cenkalti/backoff/v5"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/geo"
	"github.com/pannpers/go-logging/logging"
	"google.golang.org/genai"
)

// errInvalidJSON is a sentinel error returned by parseEvents when the Gemini
// response contains invalid JSON. Treated as a transient (retryable) error.
var errInvalidJSON = errors.New("gemini returned invalid JSON")

// Config holds the configuration for Gemini searcher.
type Config struct {
	ProjectID   string
	Location    string
	DataStoreID string

	// ModelName is the legacy fallback used when per-step model names are
	// unset.
	ModelName string

	// Per-step model names for the two-step pipeline.
	//   - ModelDiscovery / ModelExtract: Step 1 (grounded search + verbatim
	//     extract — GoogleSearch + URLContext, no schema). ModelExtract is
	//     preferred for new callers; ModelDiscovery is retained as a legacy
	//     alias and resolved together with ModelExtract.
	//   - ModelParse: Step 2 (structured-output JSON parse, no tools).
	// Empty fields fall back to ModelName.
	ModelDiscovery string
	ModelExtract   string
	ModelParse     string

	Temperature float32

	// ThinkingLevel is the legacy fallback used when ThinkingExtract /
	// ThinkingParse are unset for a given step.
	ThinkingLevel string

	// Per-step thinking levels for the two-step pipeline.
	//   - ThinkingExtract: Step 1 (grounded search + verbatim extract).
	//   - ThinkingParse:   Step 2 (XML → JSON transformation).
	// Empty fields fall back to ThinkingLevel.
	//
	// Recommended split for flash:
	//   - Extract: "medium" or "high" (agentic chain benefits from depth)
	//   - Parse:   "low" (mechanical transformation; schema bounds output)
	ThinkingExtract string
	ThinkingParse   string

	// APIKey, when non-empty, selects the Gemini API direct backend.
	// Required for URLContext / TimeRangeFilter / ExcludeDomains (not
	// supported via Vertex AI).
	APIKey string
}

func (c *Config) modelDiscovery() string {
	if c.ModelDiscovery != "" {
		return c.ModelDiscovery
	}
	return c.ModelName
}
func (c *Config) modelExtract() string {
	if c.ModelExtract != "" {
		return c.ModelExtract
	}
	if c.ModelDiscovery != "" {
		return c.ModelDiscovery
	}
	return c.ModelName
}
func (c *Config) modelParse() string {
	if c.ModelParse != "" {
		return c.ModelParse
	}
	return c.ModelName
}

// thinkingExtract / thinkingParse resolve the per-step thinking level
// with fallback to the legacy ThinkingLevel field.
func (c *Config) thinkingExtract() string {
	if c.ThinkingExtract != "" {
		return c.ThinkingExtract
	}
	return c.ThinkingLevel
}
func (c *Config) thinkingParse() string {
	if c.ThinkingParse != "" {
		return c.ThinkingParse
	}
	return c.ThinkingLevel
}

func thinkingLevelFromConfig(level string) genai.ThinkingLevel {
	switch strings.ToLower(level) {
	case "minimal":
		return genai.ThinkingLevelMinimal
	case "low":
		return genai.ThinkingLevelLow
	case "medium":
		return genai.ThinkingLevelMedium
	case "high":
		return genai.ThinkingLevelHigh
	default:
		return genai.ThinkingLevelUnspecified
	}
}

const (
	// systemInstructionStep1Tour is the Step 1 system instruction used by
	// the tour-focused slices (tours_near / tours_far). The output format
	// shows only <tour> blocks; the prompt narrows the time scope.
	systemInstructionStep1Tour = `You are an AI agent specialised in data extraction, running as a backend for a live-music information system.
Based on the artist name and time scope supplied by the user, you MUST use the Google Search tool to locate candidate detail pages, and you MUST use the URL Context tool to fetch those pages and read their actual contents. Search result snippets alone are insufficient — the per-show schedule table you need lives inside the detail page, so call URL Context on every candidate URL before extracting fields.

[Constraints]
1. Extract only future schedules — events on or after the current date.
2. ★IMPORTANT★: Do not omit any tour date present in the fetched pages. Do not stop after a handful; list every tour date you can find within the requested scope.
3. Extract only facts from the fetched pages; never fill in values by inference.
4. If a field value is not present on the fetched page, emit an EMPTY tag (e.g. <start_time></start_time>). Do NOT write placeholder strings such as "未定", "TBA", "TBD", or "to be announced" — those are hallucinations, not facts. In particular, NEVER copy the first day's time into the second day; if only one day's time is known, leave the other day's tags empty.
5. The output MUST be only the XML envelope structure shown below, with no Markdown decoration (e.g. ` + "```" + `xml).
6. Special characters appearing inside XML attribute values or tag contents (&, <, >, ", etc.) MUST be properly escaped (&amp;, &lt;, &gt;, &quot;) so the output parses as valid XML.

[Output XML format]
<extracted>
  <source url="https://www.uverworld.jp/feature/2026_live">
    <tour>
      <tour_title>UVERworld TYCOON LIVE -DOCUMENT-</tour_title>
      <event>
        <venue>Zepp Nagoya</venue>
        <country>JP</country>
        <local_date>2026年3月15日(土)</local_date>
        <start_time>開演 18:00</start_time>
        <open_time>開場 17:00</open_time>
      </event>
      <event>
        <venue>大阪府・Zepp Osaka Bayside</venue>
        <country>JP</country>
        <local_date>2026.3.16(日)</local_date>
        <start_time>18:00</start_time>
        <open_time>17:00</open_time>
      </event>
    </tour>
  </source>
  <source url="..."><empty/></source>
</extracted>
`

	// systemInstructionStep1Standalone is the Step 1 system instruction
	// used by the standalone-focused slice. Output format shows only
	// <standalone> blocks.
	systemInstructionStep1Standalone = `You are an AI agent specialised in data extraction, running as a backend for a live-music information system.
Based on the artist name supplied by the user, you MUST use the Google Search tool to locate candidate detail pages, and you MUST use the URL Context tool to fetch those pages and read their actual contents. Search result snippets alone are insufficient — per-show details (venue, date, start/open time) live inside the detail page, so call URL Context on every candidate URL before extracting fields.

[Constraints]
1. Extract only future schedules — events on or after the current date.
2. ★IMPORTANT★: Do not omit any standalone show present in the fetched pages. Do not stop after a handful; list every one-off concert you can find.
3. Extract only facts from the fetched pages; never fill in values by inference.
4. If a field value is not present on the fetched page, emit an EMPTY tag (e.g. <start_time></start_time>). Do NOT write placeholder strings such as "未定", "TBA", "TBD", or "to be announced" — those are hallucinations, not facts.
5. The output MUST be only the XML envelope structure shown below, with no Markdown decoration (e.g. ` + "```" + `xml).
6. Special characters appearing inside XML attribute values or tag contents (&, <, >, ", etc.) MUST be properly escaped (&amp;, &lt;, &gt;, &quot;) so the output parses as valid XML.

[Output XML format]
<extracted>
  <source url="https://www.uverworld.jp/news/detail/budokan">
    <standalone>
      <event_title>UVERworld 武道館単独公演 2026</event_title>
      <venue>日本武道館</venue>
      <country>JP</country>
      <local_date>2026/04/01</local_date>
      <start_time>19:00</start_time>
      <open_time></open_time>
    </standalone>
  </source>
  <source url="..."><empty/></source>
</extracted>
`

	// systemInstructionStep2Parse is the Step 2 system instruction. Static
	// (no placeholders) so it caches across all parse calls.
	systemInstructionStep2Parse = `You are an AI agent specialised in data transformation, running as a backend for a live-music information system.
You receive a JSON array of input events with raw venue, country, and date/time strings. Produce a JSON response per the schema in which each input event appears exactly once, with admin_area inferred from venue and date/time coerced to ISO formats.

[Constraints]
1. The output MUST contain exactly one entry per input event. Preserve the input index unchanged — it is the join key the caller uses to merge your output back with title / source_url fields you never see.
2. Per-field coercion rules (admin_area inference from venue, local_date YYYY-MM-DD, start_time / open_time RFC3339 composed from local_date + the raw time + country timezone, empty-string handling) are defined in each schema field's description — follow them.
3. Output only the JSON defined by the schema. No Markdown decoration or comments.
`

	// promptTemplateStep1Slice carries the per-call variables for one
	// Step 1 slice. Placeholders (4): today (YYYY-MM-DD), slice scope
	// instruction (e.g. "all multi-date tours beginning within the next
	// 12 months..."), artist name, official site host.
	promptTemplateStep1Slice = `Today: %s

%s

Artist: %s
Official site host: %s

Extract every entry discovered within the scope above. Do not omit any.
`

	// promptTemplateParse carries the JSON-list payload of Step 2 input
	// events. All task / rules live in systemInstructionStep2Parse.
	// Placeholder (1): the JSON list (output of json.Marshal on
	// []step2InputEvent).
	promptTemplateParse = `%s`

	// urlContextMaxURLs is the per-request limit URLContext enforces.
	urlContextMaxURLs = 20

	// maxOutputTokens is the default response cap.
	maxOutputTokens = int32(16384)

	maxRawTextLogLen  = 1000
	geminiCallTimeout = 120 * time.Second
)

// Step 2 field descriptions. Per-field descriptions cover ONLY the
// fields Step 2 still produces (admin_area + coerced date/time);
// title / source_url / venue are now carried verbatim through Go-side
// XML parsing and never enter Step 2's schema.
var (
	indexField = map[string]any{
		"type":        "integer",
		"description": "Echo the input event's index unchanged. Used to align this output with the corresponding input event (positional order MAY differ; index is the authoritative key).",
	}
	adminAreaField = map[string]any{
		"type":        "string",
		"description": "Administrative area (prefecture / state / province) of the venue, in the local form (e.g. 愛知県, 東京都). Derived from the input event's venue. \"\" when uncertain.",
	}
	localDateField = map[string]any{
		"type":        "string",
		"description": "Calendar date in YYYY-MM-DD, coerced from the input event's local_date (whose raw value may be e.g. \"2026年3月1日\", \"2026.3.15(土)\", or \"March 1, 2026\"). \"\" when the input is empty or coercion is ambiguous.",
	}
	startTimeField = map[string]any{
		"type":        "string",
		"description": "RFC3339 (e.g. 2026-02-14T18:30:00+09:00). Composed from the input event's local_date + start_time + the timezone of country (JP → +09:00, KR → +09:00, HK → +08:00, TW → +08:00, CN → +08:00, etc.). \"\" when any of them is empty or coercion is ambiguous.",
	}
	openTimeField = map[string]any{
		"type":        "string",
		"description": "RFC3339. Composed from the input event's local_date + open_time + the timezone of country. \"\" when any of them is empty or coercion is ambiguous.",
	}
)

// responseJSONSchema is the Step 2 response schema. Step 2 receives a
// JSON list of input events (index + venue + country + raw date/time
// strings) and returns the coerced fields keyed back by index. Title,
// source_url and the verbatim venue are not part of Step 2's universe —
// Go carries them through from the Step 1 XML envelope.
var responseJSONSchema = map[string]any{
	"type":                 "object",
	"additionalProperties": false,
	"description":          "Coerced fields for each input event. For any field whose input is empty or unparseable, emit \"\"; never emit null.",
	"properties": map[string]any{
		"events": map[string]any{
			"type":        "array",
			"description": "One coerced entry per input event. The output MAY be in any order; the index field is the authoritative key. Every input event MUST appear in the output, even if all coerced fields end up empty.",
			"items": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"index":      indexField,
					"admin_area": adminAreaField,
					"local_date": localDateField,
					"start_time": startTimeField,
					"open_time":  openTimeField,
				},
				"required": []string{"index", "admin_area", "local_date", "start_time", "open_time"},
			},
		},
	},
	"required": []string{"events"},
}

// EventDraft is the intermediate working buffer between Step 1 (XML
// envelope) and Step 2 (coerced JSON). Go-side XML parsing populates
// every field; Step 2 only modifies AdminArea and coerces LocalDate /
// StartTime / OpenTime into ISO 8601 form. Title, SourceURL and Venue
// pass through verbatim and never enter Step 2's schema, eliminating
// the LLM-side hallucination paths observed in prior smokes (venue
// translation, source_url fabrication, title decoration).
type EventDraft struct {
	Title     string // <tour_title> for tour events; <event_title> for standalones.
	SourceURL string // url attribute of the enclosing <source> block.
	Venue     string // verbatim <venue> tag content.
	Country   string // verbatim <country> tag content (ISO 3166-1 alpha-2).
	LocalDate string // raw <local_date> tag content (un-coerced).
	StartTime string // raw <start_time> tag content (un-coerced).
	OpenTime  string // raw <open_time> tag content (un-coerced).
}

// step2InputEvent is the per-event payload sent to Step 2. It is a
// pure subset of EventDraft — only the fields that Step 2 needs to
// produce its coerced output. Title and SourceURL are intentionally
// absent; they never leave Go.
type step2InputEvent struct {
	Index     int    `json:"index"`
	Venue     string `json:"venue"`
	Country   string `json:"country"`
	LocalDate string `json:"local_date"`
	StartTime string `json:"start_time"`
	OpenTime  string `json:"open_time"`
}

// step2OutputEvent is Step 2's per-event response. Index is the join
// key back to the EventDraft list.
type step2OutputEvent struct {
	Index     int    `json:"index"`
	AdminArea string `json:"admin_area"`
	LocalDate string `json:"local_date"`
	StartTime string `json:"start_time"`
	OpenTime  string `json:"open_time"`
}

// step2Response is the top-level Step 2 JSON shape (matches
// responseJSONSchema).
type step2Response struct {
	Events []step2OutputEvent `json:"events"`
}

// ----- Step 1 envelope XML parsing -----

type step1Envelope struct {
	XMLName xml.Name        `xml:"extracted"`
	Sources []step1Source   `xml:"source"`
}

type step1Source struct {
	URL         string             `xml:"url,attr"`
	Tours       []step1Tour        `xml:"tour"`
	Standalones []step1Event       `xml:"standalone"`
}

type step1Tour struct {
	TourTitle string       `xml:"tour_title"`
	Events    []step1Event `xml:"event"`
}

type step1Event struct {
	EventTitle string `xml:"event_title"` // only set when parsed as a <standalone>
	Venue      string `xml:"venue"`
	Country    string `xml:"country"`
	LocalDate  string `xml:"local_date"`
	StartTime  string `xml:"start_time"`
	OpenTime   string `xml:"open_time"`
}

// parseStep1Envelope unmarshals the merged Step 1 <extracted>...</extracted>
// envelope into a flat list of EventDraft. <tour> blocks contribute one
// draft per child <event>, with Title = the parent <tour_title>.
// <standalone> blocks contribute one draft each with Title = their own
// <event_title>. SourceURL on every draft is the url attribute of the
// enclosing <source>.
//
// Returns an empty slice (no error) on unparseable input — Step 1 may
// emit non-XML fallback text (e.g. when the model misbehaves), in which
// case we degrade gracefully rather than failing the whole Search.
func parseStep1Envelope(envelope string) []EventDraft {
	envelope = strings.TrimSpace(envelope)
	if envelope == "" {
		return nil
	}
	var env step1Envelope
	if err := xml.Unmarshal([]byte(envelope), &env); err != nil {
		return nil
	}
	var drafts []EventDraft
	for _, src := range env.Sources {
		for _, tour := range src.Tours {
			title := strings.TrimSpace(tour.TourTitle)
			for _, ev := range tour.Events {
				drafts = append(drafts, EventDraft{
					Title:     title,
					SourceURL: src.URL,
					Venue:     strings.TrimSpace(ev.Venue),
					Country:   strings.TrimSpace(ev.Country),
					LocalDate: strings.TrimSpace(ev.LocalDate),
					StartTime: strings.TrimSpace(ev.StartTime),
					OpenTime:  strings.TrimSpace(ev.OpenTime),
				})
			}
		}
		for _, ev := range src.Standalones {
			drafts = append(drafts, EventDraft{
				Title:     strings.TrimSpace(ev.EventTitle),
				SourceURL: src.URL,
				Venue:     strings.TrimSpace(ev.Venue),
				Country:   strings.TrimSpace(ev.Country),
				LocalDate: strings.TrimSpace(ev.LocalDate),
				StartTime: strings.TrimSpace(ev.StartTime),
				OpenTime:  strings.TrimSpace(ev.OpenTime),
			})
		}
	}
	return drafts
}

// ConcertSearcher implements entity.ConcertSearcher using Vertex AI Gemini.
type ConcertSearcher struct {
	client *genai.Client
	config Config
	logger *logging.Logger
}

// PassMetadata captures observation data for a single Gemini call.
type PassMetadata struct {
	PromptTokens     int32
	CandidatesTokens int32
	ThinkingTokens   int32
	ToolUseTokens    int32
	TotalTokens      int32
	FinishReason     string
	FinishMessage    string
	AvgLogprobs      float64
	RetryCount       int
	PartsTotal       int
	ThoughtParts     int
	TextParts        int
	RawResponseText  string

	WebSearchQueries     int
	WebSearchQueriesList []string
	GroundingChunkURLs   []string
	RenderedParts        int

	URLContextRetrieved []URLRetrieval
}

// SearchMetadata captures per-call observation data used by the A/B
// evaluation harness. Top-level token / finish-reason fields mirror
// Step2Parse.
type SearchMetadata struct {
	// Step1Grounded — aggregated metadata across all parallel Step 1
	// slices (sum of tokens, concatenated lists, etc.). Used by the cost
	// calculator and dashboards that expect a single "Step 1 total".
	Step1Grounded *PassMetadata
	// Step1Slices — per-slice metadata in slice-definition order (see
	// defaultStep1Slices). Surfaced for diagnostics so a failing slice
	// can be identified without re-running.
	Step1Slices []*PassMetadata
	// Step2Parse — JSON parse, schema enforced, no tools. Nil when every
	// Step 1 slice failed and no envelope was produced.
	Step2Parse *PassMetadata

	// DiscoveredURLs surfaces URLs the model actually fetched via
	// url_context during Step 1, for harness reporting convenience.
	DiscoveredURLs     []string
	DiscoveredURLCount int

	// DraftCount is the number of EventDraft entries Go parsed from the
	// merged Step 1 envelope before sending to Step 2. Useful for
	// diagnostics: a large drop between DraftCount and ToursCount +
	// StandalonesCount points to Step 2 losing rows during coercion.
	DraftCount int

	// Mirror Step2Parse onto top-level (back-compat with log consumers).
	PromptTokens     int32
	CandidatesTokens int32
	ThinkingTokens   int32
	ToolUseTokens    int32
	TotalTokens      int32
	FinishReason     string
	FinishMessage    string
	AvgLogprobs      float64
	RetryCount       int
	InvalidJSON      bool

	WebSearchQueries     int
	WebSearchQueriesList []string
	GroundingChunkURLs   []string
	RenderedParts        int

	ToursCount       int
	StandalonesCount int

	PartsTotal      int
	ThoughtParts    int
	TextParts       int
	RawResponseText string

	URLContextRetrieved []URLRetrieval
}

// URLRetrieval is one entry in URLContextMetadata.
type URLRetrieval struct {
	URL    string `json:"url"`
	Status string `json:"status"`
}

// NewConcertSearcher creates a new ConcertSearcher.
//
// Backend selection:
//   - cfg.APIKey != "": Gemini API direct (unlocks URLContext, TimeRangeFilter).
//   - cfg.APIKey == "": Vertex AI with ADC.
func NewConcertSearcher(ctx context.Context, cfg Config, httpClient *http.Client, useADC bool, logger *logging.Logger) (*ConcertSearcher, error) {
	cc := &genai.ClientConfig{HTTPClient: httpClient}

	if cfg.APIKey != "" {
		cc.Backend = genai.BackendGeminiAPI
		cc.APIKey = cfg.APIKey
	} else {
		cc.Backend = genai.BackendVertexAI
		cc.Project = cfg.ProjectID
		cc.Location = cfg.Location
		if httpClient != nil && useADC {
			if err := cc.UseDefaultCredentials(); err != nil {
				return nil, fmt.Errorf("setup default credentials: %w", err)
			}
		}
	}

	client, err := genai.NewClient(ctx, cc)
	if err != nil {
		return nil, fmt.Errorf("failed to create genai client: %w", err)
	}

	return &ConcertSearcher{
		client: client,
		config: cfg,
		logger: logger,
	}, nil
}

// Search discovers new concerts for a given artist using the two-step
// Gemini pipeline.
func (s *ConcertSearcher) Search(
	ctx context.Context,
	artist *entity.Artist,
	officialSite *entity.OfficialSite,
	from time.Time,
) ([]*entity.ScrapedConcert, error) {
	results, _, err := s.SearchExt(ctx, artist, officialSite, from)
	return results, err
}

// SearchExt is identical to Search but additionally returns per-call
// metadata. Used by the A/B evaluation harness.
//
// Two-step pipeline:
//  1. Grounded — GoogleSearch + URLContext together, no schema. The model
//     uses google_search to find brand-domain pages, then url_context to
//     fetch the most relevant ones, then emits an <extracted><source>...
//     verbatim envelope. Officially recommended per
//     https://ai.google.dev/gemini-api/docs/google-search.
//  2. Parse — responseJsonSchema enforced, no tools. The envelope is
//     parsed into the existing {tours[], standalones[]} JSON. Officially
//     supported tool/schema combination on gemini-3.5-flash.
func (s *ConcertSearcher) SearchExt(
	ctx context.Context,
	artist *entity.Artist,
	officialSite *entity.OfficialSite,
	from time.Time,
) ([]*entity.ScrapedConcert, *SearchMetadata, error) {
	var officialSiteURL string
	if officialSite != nil {
		officialSiteURL = officialSite.URL
	}

	attrs := []slog.Attr{
		slog.String("artistID", artist.ID),
		slog.String("model_grounded", s.config.modelExtract()),
		slog.String("model_parse", s.config.modelParse()),
		slog.String("artist", artist.Name),
		slog.String("official_site", officialSiteURL),
		slog.String("from", from.Format("2006-01-02")),
	}
	s.logger.Info(ctx, "start calling Gemini API to search concerts", attrs...)

	md := &SearchMetadata{}

	// ===== Step 1: Grounded search + verbatim extract (parallel slices) =====
	envelope, step1, step1Slices, err := s.runStep1Grounded(ctx, artist, officialSiteURL, attrs)
	md.Step1Grounded = step1
	md.Step1Slices = step1Slices
	if err != nil {
		s.logger.Warn(ctx, "step 1 (grounded) failed permanently, aborting Search",
			append(attrs, slog.String("error", err.Error()))...)
		return nil, md, err
	}
	// Surface URLs the model actually fetched via url_context.
	if step1 != nil {
		urls := make([]string, 0, len(step1.URLContextRetrieved))
		for _, u := range step1.URLContextRetrieved {
			urls = append(urls, u.URL)
		}
		md.DiscoveredURLs = urls
		md.DiscoveredURLCount = len(urls)
	}
	if envelope == "" {
		s.logger.Warn(ctx, "step 1 produced empty envelope, returning empty results", attrs...)
		return nil, md, nil
	}

	// Go-side XML parse: title / source_url / venue / country / raw
	// date-time fields are extracted verbatim from Step 1's <extracted>
	// envelope and held in EventDraft. Step 2 only sees the subset it
	// needs to coerce (venue + country + raw date-time strings).
	drafts := parseStep1Envelope(envelope)
	md.DraftCount = len(drafts)
	if len(drafts) == 0 {
		s.logger.Warn(ctx, "step 1 envelope produced 0 parseable events, returning empty results", attrs...)
		return nil, md, nil
	}

	// ===== Step 2: Structured parse (no tools, schema enforced) =====
	results, step2, err := s.runStep2Parse(ctx, drafts, from, md, attrs)
	md.Step2Parse = step2
	mirrorStep2(md, step2)
	if err != nil {
		return nil, md, err
	}
	return results, md, nil
}

// mirrorStep2 copies Step 2 values into top-level SearchMetadata fields.
// Existing log consumers expect a single token snapshot per Search.
func mirrorStep2(md *SearchMetadata, pm *PassMetadata) {
	if md == nil || pm == nil {
		return
	}
	md.PromptTokens = pm.PromptTokens
	md.CandidatesTokens = pm.CandidatesTokens
	md.ThinkingTokens = pm.ThinkingTokens
	md.ToolUseTokens = pm.ToolUseTokens
	md.TotalTokens = pm.TotalTokens
	md.FinishReason = pm.FinishReason
	md.FinishMessage = pm.FinishMessage
	md.AvgLogprobs = pm.AvgLogprobs
	md.RetryCount = pm.RetryCount
	md.PartsTotal = pm.PartsTotal
	md.ThoughtParts = pm.ThoughtParts
	md.TextParts = pm.TextParts
	md.RawResponseText = pm.RawResponseText
	md.WebSearchQueries = pm.WebSearchQueries
	md.WebSearchQueriesList = pm.WebSearchQueriesList
	md.GroundingChunkURLs = pm.GroundingChunkURLs
	md.RenderedParts = pm.RenderedParts
	md.URLContextRetrieved = pm.URLContextRetrieved
}

// Step1Slice describes one parallel search slice in Step 1. The slice
// design keeps each Gemini call narrowly scoped so the model is less
// likely to truncate output mid-discovery.
type Step1Slice struct {
	// Name is a stable identifier used in logs and per-slice metadata.
	Name string
	// SystemInstruction selects the tour-only or standalone-only Step 1
	// system instruction.
	SystemInstruction string
	// Scope is the slice-specific instruction sentence injected into the
	// prompt (e.g. "Find every multi-date tour beginning within the next
	// 12 months from today; exclude festivals and one-off shows.").
	Scope string
}

// defaultStep1Slices is the three-slice split used by SearchExt:
//  1. Tours beginning within the next 12 months.
//  2. Tours beginning more than 12 months from now.
//  3. Upcoming one-off / standalone shows.
//
// Each slice excludes the other two categories so the model focuses on
// one bucket at a time.
var defaultStep1Slices = []Step1Slice{
	{
		Name:              "tours_near",
		SystemInstruction: systemInstructionStep1Tour,
		Scope:             "Find every multi-date tour featuring the artist whose first show is scheduled on a date in the closed interval [today, today + 12 months] (the 12-month boundary date itself counts as 'near'). Exclude music festivals (multi-artist lineups) and one-off / standalone shows.",
	},
	{
		Name:              "tours_far",
		SystemInstruction: systemInstructionStep1Tour,
		Scope:             "Find every multi-date tour featuring the artist whose first show is scheduled strictly after today + 12 months (i.e. more than 12 months from today). Exclude music festivals (multi-artist lineups) and one-off / standalone shows.",
	},
	{
		Name:              "standalones",
		SystemInstruction: systemInstructionStep1Standalone,
		Scope:             "Find every upcoming one-off / standalone single-date show featuring the artist (solo shows, fan-club exclusive lives, 対バン with 2-4 named co-headliners). Exclude music festivals (multi-artist lineups) and multi-date tours.",
	},
}

// runStep1Grounded executes Step 1 as a fan-out across defaultStep1Slices.
// Each slice fires its own Gemini call in parallel. Successful envelopes
// are merged with <source url> dedup before being returned.
//
// Returns the merged envelope, the aggregated PassMetadata (sum of all
// slice tokens), the per-slice metadata, and an error. Permanent errors
// from any slice are surfaced; transient exhaustion on a single slice is
// logged and that slice is skipped.
func (s *ConcertSearcher) runStep1Grounded(
	ctx context.Context,
	artist *entity.Artist,
	officialSiteURL string,
	attrs []slog.Attr,
) (string, *PassMetadata, []*PassMetadata, error) {
	host := hostOf(officialSiteURL)
	today := time.Now().UTC().Format("2006-01-02")

	type sliceResult struct {
		envelope string
		pm       *PassMetadata
		err      error
	}
	results := make([]sliceResult, len(defaultStep1Slices))
	var wg sync.WaitGroup
	for i, sl := range defaultStep1Slices {
		wg.Add(1)
		go func(idx int, slice Step1Slice) {
			defer wg.Done()
			env, pm, err := s.runStep1Slice(ctx, slice, artist.Name, host, today, attrs)
			results[idx] = sliceResult{envelope: env, pm: pm, err: err}
		}(i, sl)
	}
	wg.Wait()

	// Surface the first permanent error encountered.
	var firstErr error
	envelopes := make([]string, 0, len(results))
	perSlice := make([]*PassMetadata, len(results))
	for i, r := range results {
		perSlice[i] = r.pm
		if r.err != nil && firstErr == nil {
			firstErr = r.err
		}
		if r.envelope != "" {
			envelopes = append(envelopes, r.envelope)
		}
	}
	agg := aggregatePassMetadata(perSlice)
	if firstErr != nil {
		return "", agg, perSlice, firstErr
	}

	merged := mergeAndDedupEnvelopes(envelopes)
	return merged, agg, perSlice, nil
}

// runStep1Slice runs a single slice call.
//
// Returns (envelope, metadata, nil) on success; ("", metadata, err) on
// permanent error; ("", metadata, nil) on transient retry exhaustion.
func (s *ConcertSearcher) runStep1Slice(
	ctx context.Context,
	slice Step1Slice,
	artistName, officialSiteHost, today string,
	attrs []slog.Attr,
) (string, *PassMetadata, error) {
	prompt := fmt.Sprintf(promptTemplateStep1Slice, today, slice.Scope, artistName, officialSiteHost)

	now := time.Now().UTC().Truncate(time.Second)
	searchTool := &genai.Tool{
		GoogleSearch: &genai.GoogleSearch{
			TimeRangeFilter: &genai.Interval{
				StartTime: now.AddDate(0, -6, 0),
				EndTime:   now,
			},
		},
	}
	urlCtxTool := &genai.Tool{URLContext: &genai.URLContext{}}
	temperature := s.config.Temperature

	cfg := &genai.GenerateContentConfig{
		SystemInstruction: &genai.Content{
			Parts: []*genai.Part{{Text: slice.SystemInstruction}},
		},
		Tools:           []*genai.Tool{searchTool, urlCtxTool},
		Temperature:     &temperature,
		MaxOutputTokens: maxOutputTokens,
	}
	if level := thinkingLevelFromConfig(s.config.thinkingExtract()); level != genai.ThinkingLevelUnspecified {
		cfg.ThinkingConfig = &genai.ThinkingConfig{ThinkingLevel: level}
	}
	if err := assertStepInvariants("step1_grounded", cfg); err != nil {
		return "", nil, err
	}

	// Build stepAttrs on a fresh backing array. The parent `attrs` slice is
	// shared across slice goroutines; if `attrs` has spare capacity,
	// `append(attrs, ...)` writes through to the shared array — a data race
	// observed under `go test -race` and the cause of intermittent panics
	// during concurrent slice execution.
	stepAttrs := make([]slog.Attr, 0, len(attrs)+2)
	stepAttrs = append(stepAttrs, attrs...)
	stepAttrs = append(stepAttrs,
		slog.String("step", "1_grounded"),
		slog.String("slice", slice.Name),
	)
	pm, rawText, transient, err := s.executePass(ctx, s.config.modelExtract(), prompt, cfg, stepAttrs)
	if err != nil {
		return "", pm, err
	}
	if transient {
		s.logger.Warn(ctx, "step 1 slice exhausted retries with transient error", stepAttrs...)
		return "", pm, nil
	}
	return rawText, pm, nil
}

// aggregatePassMetadata sums per-slice token counts and concatenates
// list-valued fields. The returned PassMetadata stands in for the
// "Step 1 totals" so the existing cost calculator (which reads
// PromptTokens / CandidatesTokens / ThinkingTokens / ToolUseTokens) keeps
// working.
func aggregatePassMetadata(slices []*PassMetadata) *PassMetadata {
	agg := &PassMetadata{}
	for _, s := range slices {
		if s == nil {
			continue
		}
		agg.PromptTokens += s.PromptTokens
		agg.CandidatesTokens += s.CandidatesTokens
		agg.ThinkingTokens += s.ThinkingTokens
		agg.ToolUseTokens += s.ToolUseTokens
		agg.TotalTokens += s.TotalTokens
		agg.RetryCount += s.RetryCount
		agg.PartsTotal += s.PartsTotal
		agg.ThoughtParts += s.ThoughtParts
		agg.TextParts += s.TextParts
		agg.WebSearchQueries += s.WebSearchQueries
		agg.WebSearchQueriesList = append(agg.WebSearchQueriesList, s.WebSearchQueriesList...)
		agg.GroundingChunkURLs = append(agg.GroundingChunkURLs, s.GroundingChunkURLs...)
		agg.RenderedParts += s.RenderedParts
		agg.URLContextRetrieved = append(agg.URLContextRetrieved, s.URLContextRetrieved...)
		// FinishReason: keep STOP if any slice succeeded; otherwise show
		// the last non-empty non-STOP reason for diagnostics.
		if agg.FinishReason == "" || agg.FinishReason == string(genai.FinishReasonStop) {
			agg.FinishReason = s.FinishReason
		}
		if agg.FinishMessage == "" {
			agg.FinishMessage = s.FinishMessage
		}
		// AvgLogprobs: take the last reported (mean would be more accurate
		// but per-slice samples differ in size; not worth the complexity).
		if s.AvgLogprobs != 0 {
			agg.AvgLogprobs = s.AvgLogprobs
		}
		// RawResponseText: concatenate so diagnostics still surface every
		// emission. mergeAndDedupEnvelopes is what feeds Step 2.
		if s.RawResponseText != "" {
			if agg.RawResponseText != "" {
				agg.RawResponseText += "\n"
			}
			agg.RawResponseText += s.RawResponseText
		}
	}
	return agg
}

// sourceInnerRe captures (url, inner-body) for a <source ...>...</source>
// block. Group 1 = url attribute value; Group 2 = inner XML between the
// open and close tags.
var sourceInnerRe = regexp.MustCompile(`(?s)<source\s+url="([^"]*)"[^>]*>(.*?)</source>`)

// mergeAndDedupEnvelopes merges several Step 1 slice envelopes into one
// <extracted>...</extracted> wrapper. <source> blocks sharing the same
// url attribute have their inner XML CONCATENATED into a single block;
// this preserves all per-slice content even when slices reuse the same
// URL (e.g. lite mode where the model writes the artist root for every
// slice because it has not actually called url_context).
//
// Dedup layering:
//   - This function (Go-side): URL-level GROUPING. Multiple <source>
//     blocks with the same URL collapse to one block whose body is the
//     concatenation of their inner content. No event-level dedup here.
//   - Step 2 (LLM-side): event-level dedup keyed on (local_date, venue).
//     Step 2 sees a deduped <source> set but may receive duplicated
//     <tour>/<standalone>/<event> entries from the concatenation; it
//     removes those by the documented (local_date, venue) rule.
//
// Fallback: if none of the envelopes contain parseable <source> blocks
// (e.g. unit-test mocks that return a JSON body where Step 1 would
// normally emit XML), the first non-empty envelope is returned verbatim
// so Step 2 still receives something to parse.
func mergeAndDedupEnvelopes(envelopes []string) string {
	if len(envelopes) == 0 {
		return ""
	}
	type group struct {
		url    string
		bodies []string
	}
	byURL := make(map[string]int) // url -> index into groups
	var groups []group

	for _, env := range envelopes {
		matches := sourceInnerRe.FindAllStringSubmatch(env, -1)
		for _, m := range matches {
			url := m[1]
			body := strings.TrimSpace(m[2])
			if idx, ok := byURL[url]; ok {
				groups[idx].bodies = append(groups[idx].bodies, body)
			} else {
				byURL[url] = len(groups)
				groups = append(groups, group{url: url, bodies: []string{body}})
			}
		}
	}

	if len(groups) == 0 {
		for _, e := range envelopes {
			if strings.TrimSpace(e) != "" {
				return e
			}
		}
		return ""
	}

	var out strings.Builder
	out.WriteString("<extracted>\n")
	for _, g := range groups {
		out.WriteString(`  <source url="`)
		out.WriteString(g.url)
		out.WriteString("\">\n")
		for _, body := range g.bodies {
			if body == "" {
				continue
			}
			out.WriteString(body)
			out.WriteByte('\n')
		}
		out.WriteString("  </source>\n")
	}
	out.WriteString("</extracted>")
	return out.String()
}

// runStep2Parse executes Step 2 — coercion of the per-event raw
// fields produced by Step 1 into ISO-formatted output, plus
// admin_area inference. No tools, responseJsonSchema enforced.
//
// drafts is the Go-side parsed Step 1 envelope. Title / SourceURL /
// Venue pass through Go untouched and are merged back with the
// coerced output by index. Step 2 never sees these fields.
func (s *ConcertSearcher) runStep2Parse(
	ctx context.Context,
	drafts []EventDraft,
	from time.Time,
	md *SearchMetadata,
	attrs []slog.Attr,
) ([]*entity.ScrapedConcert, *PassMetadata, error) {
	if len(drafts) == 0 {
		return nil, nil, nil
	}

	// Build the Step 2 input payload. JSON list of {index, venue,
	// country, local_date, start_time, open_time}.
	inputs := make([]step2InputEvent, len(drafts))
	for i, d := range drafts {
		inputs[i] = step2InputEvent{
			Index:     i,
			Venue:     d.Venue,
			Country:   d.Country,
			LocalDate: d.LocalDate,
			StartTime: d.StartTime,
			OpenTime:  d.OpenTime,
		}
	}
	payload, err := json.Marshal(inputs)
	if err != nil {
		// json.Marshal on a list of strings should never fail; treat as
		// permanent if it does.
		return nil, nil, backoff.Permanent(toAppErr(err, "failed to marshal step 2 input", attrs...))
	}
	prompt := fmt.Sprintf(promptTemplateParse, string(payload))
	temperature := s.config.Temperature

	cfg := &genai.GenerateContentConfig{
		SystemInstruction: &genai.Content{
			Parts: []*genai.Part{{Text: systemInstructionStep2Parse}},
		},
		// Tools intentionally empty — no URLContext, no GoogleSearch.
		Temperature:        &temperature,
		MaxOutputTokens:    maxOutputTokens,
		ResponseMIMEType:   "application/json",
		ResponseJsonSchema: responseJSONSchema,
	}
	if level := thinkingLevelFromConfig(s.config.thinkingParse()); level != genai.ThinkingLevelUnspecified {
		cfg.ThinkingConfig = &genai.ThinkingConfig{ThinkingLevel: level}
	}
	if err := assertStepInvariants("step2_parse", cfg); err != nil {
		return nil, nil, err
	}

	stepAttrs := append(attrs, slog.String("step", "2_parse"))
	pm, rawText, transient, err := s.executePass(ctx, s.config.modelParse(), prompt, cfg, stepAttrs)
	if err != nil {
		return nil, pm, err
	}
	if transient {
		s.logger.Warn(ctx, "step 2 exhausted retries, returning empty results", stepAttrs...)
		return nil, pm, nil
	}

	parsed, perr := s.parseStep2Response(ctx, rawText, drafts, from, md, stepAttrs...)
	if perr != nil {
		if errors.Is(perr, errInvalidJSON) {
			md.InvalidJSON = true
		}
		return nil, pm, perr
	}
	return parsed, pm, nil
}

// executePass runs one Gemini call wrapped in exponential backoff
// (3 attempts, 1s/2s/4s, 60s max). It captures all observable metadata
// into a fresh PassMetadata. Returns:
//   - (pm, rawText, false, nil) on success
//   - (pm, "", true, nil) when retries are exhausted with transient errors
//   - (pm, "", false, err) on permanent error
//
// Non-STOP finish_reason is treated as transient and retried.
func (s *ConcertSearcher) executePass(
	ctx context.Context,
	modelName string,
	prompt string,
	cfg *genai.GenerateContentConfig,
	attrs []slog.Attr,
) (*PassMetadata, string, bool, error) {
	pm := &PassMetadata{}
	bo := &backoff.ExponentialBackOff{
		InitialInterval: 1 * time.Second,
		Multiplier:      2.0,
		MaxInterval:     60 * time.Second,
	}

	var (
		lastWasFinish bool
		sawPermanent  bool
	)
	rawText, err := backoff.Retry(ctx, func() (string, error) {
		pm.RetryCount++
		reqCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), geminiCallTimeout)
		defer cancel()

		resp, err := s.client.Models.GenerateContent(reqCtx, modelName, genai.Text(prompt), cfg)
		if err != nil {
			lastWasFinish = false
			s.logger.Warn(ctx, "gemini model call failed",
				append(attrs, slog.String("error", err.Error()))...)
			if !isRetryable(err) {
				sawPermanent = true
				return "", backoff.Permanent(err)
			}
			return "", err
		}

		if u := resp.UsageMetadata; u != nil {
			pm.PromptTokens = u.PromptTokenCount
			pm.CandidatesTokens = u.CandidatesTokenCount
			pm.ThinkingTokens = u.ThoughtsTokenCount
			pm.ToolUseTokens = u.ToolUsePromptTokenCount
			pm.TotalTokens = u.TotalTokenCount
		}

		respAttrs := []slog.Attr{
			slog.String("response_id", resp.ResponseID),
			slog.Group("usage_metadata",
				slog.Int("prompt", int(pm.PromptTokens)),
				slog.Int("candidates", int(pm.CandidatesTokens)),
				slog.Int("thinking", int(pm.ThinkingTokens)),
				slog.Int("total", int(pm.TotalTokens)),
				slog.Int("tool_use", int(pm.ToolUseTokens)),
			),
		}

		if len(resp.Candidates) == 0 {
			s.logger.Info(ctx, "Gemini returned no candidates", append(attrs, respAttrs...)...)
			return "", nil
		}

		candidate := resp.Candidates[0]
		pm.FinishReason = string(candidate.FinishReason)
		pm.FinishMessage = candidate.FinishMessage
		pm.AvgLogprobs = candidate.AvgLogprobs

		if g := candidate.GroundingMetadata; g != nil {
			pm.WebSearchQueriesList = g.WebSearchQueries
			pm.WebSearchQueries = len(g.WebSearchQueries)
			for _, ch := range g.GroundingChunks {
				if ch != nil && ch.Web != nil {
					pm.GroundingChunkURLs = append(pm.GroundingChunkURLs, ch.Web.URI)
				}
			}
			var renderedParts int
			for _, sup := range g.GroundingSupports {
				if sup == nil {
					continue
				}
				renderedParts += len(sup.RenderedParts)
			}
			pm.RenderedParts = renderedParts
		}

		if candidate.URLContextMetadata != nil {
			for _, um := range candidate.URLContextMetadata.URLMetadata {
				if um == nil {
					continue
				}
				pm.URLContextRetrieved = append(pm.URLContextRetrieved, URLRetrieval{
					URL:    um.RetrievedURL,
					Status: string(um.URLRetrievalStatus),
				})
			}
		}

		candidateAttrs := append(respAttrs,
			slog.String("finish_reason", pm.FinishReason),
			slog.String("finish_message", pm.FinishMessage),
			slog.Float64("avg_logprobs", pm.AvgLogprobs),
			slog.Int("web_search_queries", pm.WebSearchQueries),
			slog.Int("url_context_retrieved", len(pm.URLContextRetrieved)),
		)

		var textBuf strings.Builder
		var totalParts, thoughtParts, textParts int
		for _, p := range candidate.Content.Parts {
			if p == nil {
				continue
			}
			totalParts++
			if p.Thought {
				thoughtParts++
				continue
			}
			if p.Text == "" {
				continue
			}
			textParts++
			textBuf.WriteString(p.Text)
		}
		pm.PartsTotal = totalParts
		pm.ThoughtParts = thoughtParts
		pm.TextParts = textParts
		joined := textBuf.String()
		pm.RawResponseText = joined
		if joined == "" {
			s.logger.Debug(ctx, "candidate has no text parts",
				append(attrs, candidateAttrs...)...)
			return "", nil
		}

		s.logger.Info(ctx, "successfully received Gemini response",
			append(attrs, append(candidateAttrs,
				slog.Int("parts_total", totalParts),
				slog.Int("thought_parts", thoughtParts),
				slog.Int("text_parts", textParts),
			)...)...)

		if candidate.FinishReason != genai.FinishReasonStop && candidate.FinishReason != "" {
			lastWasFinish = true
			finishErr := fmt.Errorf("gemini response not completed normally: finish_reason=%s", candidate.FinishReason)
			s.logger.Warn(ctx, "gemini response not completed normally, retrying",
				append(attrs, candidateAttrs...)...)
			return "", finishErr
		}

		lastWasFinish = false
		return joined, nil
	}, backoff.WithBackOff(bo), backoff.WithMaxTries(3))

	if err != nil {
		if sawPermanent {
			return pm, "", false, toAppErr(err, "failed to call Gemini API", attrs...)
		}
		if lastWasFinish {
			s.logger.Warn(ctx, "executePass exhausted retries with non-STOP finish_reason",
				append(attrs, slog.String("last_error", err.Error()))...)
			return pm, "", true, nil
		}
		return pm, "", false, toAppErr(err, "failed to call Gemini API", attrs...)
	}
	return pm, rawText, false, nil
}

// assertStepInvariants enforces the per-step tool / schema contract.
//
// Step contracts (two-step pipeline):
//   - "step1_grounded" → tools MUST be exactly {GoogleSearch, URLContext};
//     no schema.
//   - "step2_parse"    → no tools; schema MUST be set.
//
// Rationale: gemini-3.1-flash-lite does not officially support combining
// responseJsonSchema with built-in tools. The pipeline keeps Step 1
// tools-only and Step 2 schema-only so every individual call is in a
// supported configuration.
func assertStepInvariants(step string, cfg *genai.GenerateContentConfig) error {
	if cfg == nil {
		return fmt.Errorf("internal error: nil GenerateContentConfig for step %q", step)
	}
	var hasURLCtx, hasGSearch, otherTool bool
	for _, t := range cfg.Tools {
		if t == nil {
			continue
		}
		switch {
		case t.GoogleSearch != nil:
			hasGSearch = true
		case t.URLContext != nil:
			hasURLCtx = true
		default:
			otherTool = true
		}
	}
	hasSchema := cfg.ResponseJsonSchema != nil
	switch step {
	case "step1_grounded":
		if !hasGSearch || !hasURLCtx || otherTool {
			return fmt.Errorf("internal error: step1_grounded MUST have exactly {GoogleSearch, URLContext}")
		}
		if hasSchema {
			return fmt.Errorf("internal error: step1_grounded MUST NOT set ResponseJsonSchema")
		}
	case "step2_parse":
		if hasGSearch || hasURLCtx || otherTool {
			return fmt.Errorf("internal error: step2_parse MUST NOT set any tools")
		}
		if !hasSchema {
			return fmt.Errorf("internal error: step2_parse MUST set ResponseJsonSchema")
		}
	default:
		return fmt.Errorf("internal error: unknown step %q", step)
	}
	return nil
}

// hostOf parses u and returns its host (lowercased, without scheme/port/path).
func hostOf(u string) string {
	if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		return ""
	}
	rest := u
	if strings.HasPrefix(rest, "http://") {
		rest = rest[len("http://"):]
	} else {
		rest = rest[len("https://"):]
	}
	if i := strings.IndexAny(rest, "/?#"); i >= 0 {
		rest = rest[:i]
	}
	if at := strings.LastIndex(rest, "@"); at >= 0 {
		rest = rest[at+1:]
	}
	if colon := strings.LastIndex(rest, ":"); colon >= 0 {
		rest = rest[:colon]
	}
	return strings.ToLower(rest)
}

// parseStep2Response parses the Step 2 JSON output and merges its
// coerced fields back into the input EventDraft list (matched by
// index), producing the final []*entity.ScrapedConcert. Drafts with
// no matching Step 2 entry are skipped; drafts whose coerced
// local_date is empty or in the past are also dropped.
//
// drafts is the source-of-truth for Title / SourceURL / Venue —
// those values pass through verbatim and never see Step 2.
func (s *ConcertSearcher) parseStep2Response(
	ctx context.Context,
	rawText string,
	drafts []EventDraft,
	from time.Time,
	md *SearchMetadata,
	attrs ...slog.Attr,
) ([]*entity.ScrapedConcert, error) {
	text := strings.TrimSpace(rawText)
	if strings.Contains(text, "```") {
		parts := strings.SplitSeq(text, "```")
		for p := range parts {
			p = strings.TrimSpace(p)
			if after, ok := strings.CutPrefix(p, "json"); ok {
				text = after
				break
			}
			if len(p) > 0 {
				text = p
			}
		}
	}
	text = strings.TrimSpace(text)

	if text == "" || text == "{}" || text == `{"events":[]}` {
		s.logger.Info(ctx, "Gemini response is effectively empty", append(attrs, slog.String("raw_text", rawText))...)
		return nil, nil
	}

	if !json.Valid([]byte(text)) {
		truncated := rawText
		if len(truncated) > maxRawTextLogLen {
			truncated = truncated[:maxRawTextLogLen]
		}
		s.logger.Warn(ctx, "gemini returned invalid JSON (permanent, not retrying)",
			append(attrs,
				slog.String("raw_text_truncated", truncated),
				slog.Int("raw_text_len", len(rawText)),
			)...)
		return nil, backoff.Permanent(errInvalidJSON)
	}

	var resp step2Response
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		return nil, backoff.Permanent(toAppErr(err, "failed to unmarshal gemini response",
			append(attrs, slog.String("text", text))...,
		))
	}

	// Build a lookup from index → step2OutputEvent so out-of-order or
	// partial returns still match correctly.
	byIndex := make(map[int]step2OutputEvent, len(resp.Events))
	for _, ev := range resp.Events {
		if ev.Index < 0 || ev.Index >= len(drafts) {
			s.logger.Warn(ctx, "step 2 returned event with out-of-range index, skipping",
				append(attrs, slog.Int("index", ev.Index))...)
			continue
		}
		byIndex[ev.Index] = ev
	}

	// Merge drafts + coerced output → final concerts, applying past-date
	// filter and (local_date, venue) dedup along the way.
	type dedupKey struct {
		date  string
		venue string
	}
	seen := make(map[dedupKey]struct{}, len(drafts))
	var discovered []*entity.ScrapedConcert
	var toursCount, standalonesCount int
	for i, draft := range drafts {
		coerced, ok := byIndex[i]
		if !ok {
			// Step 2 dropped this event. Possible causes: model truncated
			// the response, or it deduped silently. Log and move on.
			s.logger.Warn(ctx, "step 2 omitted event from response, skipping",
				append(attrs, slog.Int("index", i), slog.String("title", draft.Title))...)
			continue
		}
		c := s.toScrapedConcert(ctx, draft, coerced, from, attrs)
		if c == nil {
			continue
		}
		key := dedupKey{date: coerced.LocalDate, venue: draft.Venue}
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		discovered = append(discovered, c)
		// We can't classify tour vs standalone from EventDraft alone
		// (the same EventDraft shape covers both). Track structural
		// counts by checking whether multiple drafts share the same
		// title — done after the loop.
		_ = toursCount
		_ = standalonesCount
	}

	// Compute tours / standalones counts by grouping discovered events
	// on Title (multi-event title = tour; single-event title =
	// standalone). This mirrors the structural classification the
	// previous schema enforced.
	byTitle := make(map[string]int, len(discovered))
	for _, c := range discovered {
		byTitle[c.Title]++
	}
	for _, cnt := range byTitle {
		if cnt >= 2 {
			toursCount++
		} else {
			standalonesCount++
		}
	}
	if md != nil {
		md.ToursCount = toursCount
		md.StandalonesCount = standalonesCount
	}

	s.logger.Info(ctx, "successfully parsed new concerts",
		append(attrs,
			slog.Int("draft_count", len(drafts)),
			slog.Int("step2_returned", len(resp.Events)),
			slog.Int("discovered_count", len(discovered)),
			slog.Int("tours_count", toursCount),
			slog.Int("standalones_count", standalonesCount),
		)...,
	)
	return discovered, nil
}

// toScrapedConcert merges a Go-side EventDraft (Title / Venue /
// SourceURL pass-through) with the Step 2 coerced output
// (AdminArea / LocalDate / StartTime / OpenTime in ISO form) into an
// entity.ScrapedConcert. Returns nil if the event must be skipped
// (unparseable date, or local_date is before `from`).
func (s *ConcertSearcher) toScrapedConcert(
	ctx context.Context,
	draft EventDraft,
	coerced step2OutputEvent,
	from time.Time,
	attrs []slog.Attr,
) *entity.ScrapedConcert {
	date, err := time.Parse("2006-01-02", coerced.LocalDate)
	if err != nil {
		s.logger.Warn(ctx, "failed to parse event date and skip",
			append(attrs, slog.String("date", coerced.LocalDate), slog.String("title", draft.Title))...)
		return nil
	}

	if date.Before(from.Truncate(24 * time.Hour)) {
		s.logger.Debug(ctx, "filtered past event",
			append(attrs, slog.String("title", draft.Title), slog.String("date", coerced.LocalDate))...,
		)
		return nil
	}

	var startTime time.Time
	if coerced.StartTime != "" && coerced.StartTime != "null" {
		if st, err := time.Parse(time.RFC3339, coerced.StartTime); err != nil {
			s.logger.Warn(ctx, "failed to parse event start time, using zero",
				append(attrs, slog.String("start_time", coerced.StartTime))...,
			)
		} else {
			startTime = st
		}
	}

	var openTime time.Time
	if coerced.OpenTime != "" && coerced.OpenTime != "null" {
		if ot, err := time.Parse(time.RFC3339, coerced.OpenTime); err == nil {
			openTime = ot
		}
	}

	var adminArea *string
	if coerced.AdminArea != "" {
		adminArea = geo.NormalizeAdminArea(coerced.AdminArea)
	}

	return &entity.ScrapedConcert{
		Title:           draft.Title,
		ListedVenueName: draft.Venue,
		AdminArea:       adminArea,
		LocalDate:       date,
		StartTime:       startTime,
		OpenTime:        openTime,
		SourceURL:       draft.SourceURL,
	}
}
