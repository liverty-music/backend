package gemini

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/cenkalti/backoff/v7"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/analytics/posthog"
	"github.com/pannpers/go-logging/logging"
	"google.golang.org/genai"
)

// errInvalidJSON returns the Internal error of a grounded call's response
// that is not JSON of the singleStepResponseSchema shape. It is not retried:
// the model produced a structurally broken response and the same prompt is
// unlikely to fix it.
func errInvalidJSON(cause error) error {
	return apperr.Wrap(cause, codes.Internal, "gemini returned invalid JSON")
}

// errNoCandidates returns the Unavailable error of an API answer that
// succeeded with no candidate. It is not retried: the empty response is billed
// (tens to hundreds of thousands of tool-use tokens) and a repeat of the same
// request is likely to fail the same way. Failing the Search marks the
// Artist's ConcertSearch failed so the next daily run searches again.
func errNoCandidates() error {
	return apperr.New(codes.Unavailable, "gemini returned no candidates")
}

// errTooManyToolCalls returns the Unavailable error of a model that stopped
// with TOO_MANY_TOOL_CALLS. It is not retried: the call has already run its
// search queries (40-70 in prod) and a repeat usually falls into the same
// search-only mode. Failing the Search marks the Artist's ConcertSearch failed
// so the next daily run searches again.
func errTooManyToolCalls() error {
	return apperr.New(codes.Unavailable, "gemini stopped after too many tool calls")
}

// errNoText returns the Internal error of a response that ends normally
// (STOP) but carries no text. It is not retried: like errInvalidJSON it is a
// structurally broken response, since even a search that found no concerts
// answers with JSON text. Failing the Search keeps it from being recorded as
// "no concerts".
func errNoText() error {
	return apperr.New(codes.Internal, "gemini response ended normally without text")
}

// Config holds the configuration for Gemini searcher.
//
// The searcher exclusively targets the Gemini API direct backend
// (BackendGeminiAPI). The grounded call depends on URLContext and
// GoogleSearch.TimeRangeFilter, neither of which is supported on Vertex AI;
// APIKey is therefore required.
type Config struct {
	// APIKey selects the Gemini API direct backend. REQUIRED — no Vertex
	// AI fallback exists for this workload.
	APIKey string

	// Model is the model of the grounded call (GoogleSearch + URLContext
	// with a JSON response schema). REQUIRED — the constructor errors on
	// empty.
	Model string

	Temperature float32
	// OmitTemperature leaves temperature unset in every request so the model
	// default applies, as the Gemini 3.8 Flash migration guide recommends
	// ("Strip temperature, top_p, and top_k from generation configs").
	// When set, Temperature is ignored.
	OmitTemperature bool
	// IncludeServerSideToolInvocations asks the API to return the server-side
	// tool calls (Preview, Gemini 3) in the response, so the Google Search
	// queries the model ran are visible even when groundingMetadata is absent.
	IncludeServerSideToolInvocations bool

	// ThinkingLevel is the thinking level of the grounded call. Empty leaves
	// the model default in place.
	ThinkingLevel string

	// Reporter receives one generation per request sent to the model. Nil
	// reports nothing.
	Reporter GenerationReporter
}

// temperature returns the request temperature, or nil when OmitTemperature
// is set so the field is not sent.
func (c *Config) temperature() *float32 {
	if c.OmitTemperature {
		return nil
	}
	t := c.Temperature
	return &t
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
	// systemInstruction states the purpose, the task, the official sources
	// and when the answer is complete, and nothing about how to use the
	// tools. It is written for gemini-4-hydrogen at thinking "low", which
	// answers from the pages it has read and stops searching unless it is
	// told what complete means: with the earlier procedural prompt it dropped
	// a whole tour in 1 of 3 runs on [Alexandros] and go!go!vanillas, and with
	// this one it found 47 of 48 such dates (OpenSpec change
	// pass-tour-detail-pages-to-concert-search; Gemini 4 Carbon developer
	// guide: state measurable goals and success criteria). The default-language
	// rule keeps multilingual official pages (e.g. a tour page with ?lang=en)
	// from yielding romanized venue names. Official tour sites on other
	// domains are named as sources because some artists publish tours there
	// (go!go!vanillas). Field formats and verbatim rules live in
	// singleStepResponseSchema; repeat removal is done in Go.
	systemInstruction = `Purpose: the shows you return are published as concert listings that fans use to find and buy tickets. A show you miss is a show a fan misses; a wrong date or venue misleads them.

Task: return every tour and show of the given artist taking place on or after the given start date: tours, one-off shows, and co-headliner bills (対バン) organized by the artist. Exclude music festivals and cancelled shows. Return each tour or show as one series with one event per date.

Sources: use only official sources: the artist's official site, including its subdomains, and the official tour sites it links to, which may be on other domains. Do not use third-party sites such as ticket vendors or news media. When a page is offered in several languages, read its default-language version (the URL without a language parameter such as ?lang=) and copy text in that language.

Done when: every tour that the official sources mention has all of its announced dates in your answer.
`

	// promptTemplate carries the per-call variables: %[1]s the start date
	// (YYYY-MM-DD), %[2]s the artist name, %[3]s the official-site URL. The
	// URL is passed in full because URL context only fetches URLs with a
	// scheme.
	promptTemplate = `Return every tour and show of %[2]s taking place on or after %[1]s.

Official site: %[3]s
`

	// linkedPagesHeader opens the prompt block that lists the official top
	// page's links, one "label: URL" per line (see officialPageLinks). URL
	// context fetches only URLs given in the request or found by search, so
	// listing them lets the model open the show pages among them without
	// searching; it tells them apart by their labels, including pages a
	// keyword rule missed (SUPER BEAVER's "武道館2027" banner) and official
	// tour sites on other domains (go!go!vanillas). In evaluation every
	// fixture show was found with no search query (OpenSpec change
	// pass-tour-detail-pages-to-concert-search).
	linkedPagesHeader = "\nLinks on the official top page (label: URL):\n"

	// searchWindowMonths bounds the GoogleSearch TimeRangeFilter to pages
	// from the last 2 months.
	searchWindowMonths = 2

	// maxOutputTokens caps thinking and response tokens together, which
	// gemini-4-hydrogen counts against one budget. Its migration checklist
	// says to drop low caps or raise them to 65,536; only the tokens used are
	// billed.
	maxOutputTokens = int32(65536)

	maxRawTextLogLen  = 1000
	geminiCallTimeout = 120 * time.Second
)

// Prompt is the system instruction and user prompt template of the grounded
// call. Template carries the indexed verbs described on promptTemplate;
// LinkedPagesHeader opens the list of the official top page's links that
// follows it when there are any (see linkedPagesHeader).
type Prompt struct {
	SystemInstruction string
	Template          string
	LinkedPagesHeader string
}

// defaultPrompt is the production prompt.
var defaultPrompt = Prompt{SystemInstruction: systemInstruction, Template: promptTemplate, LinkedPagesHeader: linkedPagesHeader}

// EventDraft is one event of the grounded call's JSON response, flattened
// with its series-level fields, before the past-date filter, repeat removal
// and series grouping.
type EventDraft struct {
	Title     string // series title, verbatim.
	SourceURL string // series source page.
	Venue     string // venue name, verbatim.
	Country   string // ISO 3166-1 alpha-2.
	AdminArea string // ISO 3166-2 as returned by the model (resolved later).
	TimeZone  string // IANA time zone as returned by the model, or "".
	LocalDate string // YYYY-MM-DD.
	StartTime string // RFC3339 or "".
	OpenTime  string // RFC3339 or "".
	// Group ties together all drafts from the same series entry of the
	// response so the caller can persist them under one Series. It is an
	// intra-run handle: unique within a single parse only, NOT a cross-run
	// series key (series identity is adopted from already-persisted member
	// events downstream). Entries are numbered from 1.
	Group int
}

// ConcertSearcher implements entity.ConcertSearcher using Gemini with
// Google Search grounding.
type ConcertSearcher struct {
	client *genai.Client
	// areas resolves the admin area the model returns to a first-level Area
	// and gives the time zones of its country.
	areas  entity.AreaRepository
	config Config
	logger *logging.Logger
	prompt Prompt

	// pageClient fetches the official top page for link discovery. Nil
	// disables link discovery.
	pageClient *http.Client

	// withoutGoogleSearch leaves the Google Search tool out of the grounded
	// call, so the model reads only the listed pages with URL context. Only
	// the A/B harness sets it (export_test.go); production always searches.
	withoutGoogleSearch bool
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
	// RetryCount is the number of requests sent, across both passes when the
	// request was re-sent without the tool-invocation report.
	RetryCount      int
	PartsTotal      int
	ThoughtParts    int
	TextParts       int
	RawResponseText string

	WebSearchQueries     int
	WebSearchQueriesList []string
	GroundingChunkURLs   []string
	RenderedParts        int

	// URLContextRetrieved is the candidate's URLContextMetadata. It can omit
	// fetches that only failed; URLContextCalls and URLContextSucceeded count
	// from the server-side tool parts instead.
	URLContextRetrieved []URLRetrieval
	// URLContextCalls is the number of URL context tool calls the model made.
	URLContextCalls int
	// URLContextSucceeded is the number of URLs those calls retrieved with
	// URL_RETRIEVAL_STATUS_SUCCESS.
	URLContextSucceeded int

	// LinkedPageURLs are the URLs of the official top page's links listed in
	// the prompt.
	LinkedPageURLs []string

	// ToolReportDropped is true when the request was rejected as invalid and
	// re-sent without the server-side tool-invocation report, so the call's
	// search queries are not visible.
	ToolReportDropped bool

	// ExhaustedTransient is true when the retry policy ran to the end with
	// the call still in a transient-error state; SearchExt then fails with
	// Unavailable.
	ExhaustedTransient bool
}

// SearchMetadata captures per-call observation data used by the A/B
// evaluation harness.
type SearchMetadata struct {
	// Grounded is the metadata of the grounded call. Nil only when the
	// request could not be built.
	Grounded *PassMetadata

	// DiscoveredURLs surfaces URLs the model actually fetched via
	// url_context, for harness reporting convenience.
	DiscoveredURLs     []string
	DiscoveredURLCount int

	// DraftCount is the number of events in the JSON response before the
	// past-date filter and repeat removal.
	DraftCount  int
	InvalidJSON bool

	ToursCount       int
	StandalonesCount int
}

// URLRetrieval is one entry in URLContextMetadata.
type URLRetrieval struct {
	URL    string `json:"url"`
	Status string `json:"status"`
}

// NewConcertSearcher creates a new ConcertSearcher.
//
// The constructor fast-fails when APIKey or Model is empty. The grounded
// call targets the Gemini API direct backend exclusively (Vertex AI does not
// support URLContext or GoogleSearch.TimeRangeFilter), so a missing APIKey
// would only surface as an opaque API error on the first Search call.
//
// httpClient carries the Gemini API calls. pageClient fetches the official
// top page whose links go into the prompt; pass
// NewOfficialPageClient(), or nil to search without links.
func NewConcertSearcher(ctx context.Context, cfg Config, httpClient, pageClient *http.Client, areas entity.AreaRepository, logger *logging.Logger) (*ConcertSearcher, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("gemini.NewConcertSearcher: APIKey is empty; set GCP_GEMINI_SEARCH_API_KEY (Gemini API direct is the only supported backend for this workload)")
	}
	if cfg.Model == "" {
		return nil, fmt.Errorf("gemini.NewConcertSearcher: Model is empty; set GCP_GEMINI_SEARCH_MODEL_EXTRACT (grounded extract model)")
	}

	cc := &genai.ClientConfig{
		HTTPClient: httpClient,
		Backend:    genai.BackendGeminiAPI,
		APIKey:     cfg.APIKey,
	}

	client, err := genai.NewClient(ctx, cc)
	if err != nil {
		return nil, fmt.Errorf("failed to create genai client: %w", err)
	}

	return &ConcertSearcher{
		areas:      areas,
		client:     client,
		config:     cfg,
		logger:     logger,
		prompt:     defaultPrompt,
		pageClient: pageClient,
	}, nil
}

// Search discovers new concerts for a given artist with one grounded Gemini
// call.
func (s *ConcertSearcher) Search(
	ctx context.Context,
	artist *entity.Artist,
	officialSite *entity.OfficialSite,
	from time.Time,
) ([]*entity.DiscoveredSeries, error) {
	results, _, err := s.SearchExt(ctx, artist, officialSite, from)
	return results, err
}

// SearchExt is identical to Search but additionally returns per-call
// metadata. Used by the A/B evaluation harness.
//
// One grounded call (GoogleSearch + URLContext with a JSON response schema;
// structured output with built-in tools is supported on Gemini 3, Preview)
// returns the events as JSON. Go then drops past events, removes repeats and
// groups the events into series.
func (s *ConcertSearcher) SearchExt(
	ctx context.Context,
	artist *entity.Artist,
	officialSite *entity.OfficialSite,
	from time.Time,
) ([]*entity.DiscoveredSeries, *SearchMetadata, error) {
	officialSiteURL := ""
	if officialSite != nil {
		officialSiteURL = officialSite.URL
	}
	attrs := s.searchAttrs(artist, officialSiteURL, from)
	s.logger.Info(ctx, "start calling Gemini API to search concerts", attrs...)

	md := &SearchMetadata{}

	links, promptSiteURL := s.linkedPages(ctx, officialSiteURL, attrs)
	linkURLs := pageLinkURLs(links)
	attrs = append(attrs, slog.Int("linked_pages", len(links)), slog.Any("linked_page_urls", linkURLs))

	// Every request sent below is reported once, after the response is
	// parsed so the last one carries the parse result.
	base := posthog.LLMGeneration{
		Model: s.config.Model,
		Request: posthog.LLMRequest{
			ArtistID:        artist.ID,
			OfficialSiteURL: officialSiteURL,
			LinkedPageURLs:  linkURLs,
			SearchFrom:      from.Format(time.DateOnly),
		},
	}
	var gens []posthog.LLMGeneration
	defer func() { reportAll(ctx, s.config.Reporter, gens) }()

	pm, rawText, transient, err := s.groundedPass(ctx, artist.Name, promptSiteURL, links, attrs, base, &gens)
	pm.LinkedPageURLs = linkURLs
	md.Grounded = pm
	recordDiscoveredURLs(md, pm)
	if err := checkGroundedPass(ctx, pm, transient, err, attrs, s.logger); err != nil {
		return nil, md, err
	}

	drafts, err := parseSingleStepJSON(rawText)
	md.DraftCount = len(drafts)
	last := &gens[len(gens)-1]
	if err != nil {
		md.InvalidJSON = true
		last.ErrorKind = errorKindInvalidJSON
		return nil, md, invalidJSONError(err, rawText, attrs)
	}
	if len(drafts) == 0 {
		s.logger.Info(ctx, "grounded call returned 0 events", attrs...)
		last.Concerts = &posthog.ConcertParseResult{}
		return nil, md, nil
	}
	discovered := s.mergeDrafts(ctx, drafts, from, md, attrs)
	last.Concerts = &posthog.ConcertParseResult{
		DraftCount:  len(drafts),
		SeriesCount: len(discovered),
		EventCount:  entity.SumSeriesEvents(discovered),
	}
	return discovered, md, nil
}

// linkedPages returns the official top page's links to list in the prompt
// and the official-site URL to pass in it: the URL the top page was read
// from, which differs from officialSiteURL after a redirect or the www.
// fallback (a registered www. host that does not resolve would otherwise
// send the model to a dead URL). With no official site, link discovery
// disabled, or a page that cannot be read, it returns no links and
// officialSiteURL. A page that cannot be read never fails Search.
func (s *ConcertSearcher) linkedPages(ctx context.Context, officialSiteURL string, attrs []slog.Attr) ([]pageLink, string) {
	if officialSiteURL == "" || s.pageClient == nil {
		return nil, officialSiteURL
	}
	links, readURL, err := officialPageLinks(ctx, s.pageClient, officialSiteURL)
	if err != nil {
		s.logger.Info(ctx, "official top page could not be read, searching without linked pages",
			append(attrs, slog.String("error", err.Error()))...)
		return nil, officialSiteURL
	}
	return links, readURL
}

// pageLinkURLs returns the URLs of links, for logs and the generation report.
func pageLinkURLs(links []pageLink) []string {
	if links == nil {
		return nil
	}
	urls := make([]string, len(links))
	for i, l := range links {
		urls[i] = l.URL
	}
	return urls
}

// buildRequest returns the user prompt and the request config of the
// grounded call. links are listed after the official site when present; with
// none the prompt is the plain template. now anchors the prompt's start date
// and the GoogleSearch time range.
func (s *ConcertSearcher) buildRequest(artistName, officialSiteURL string, links []pageLink, now time.Time) (string, *genai.GenerateContentConfig) {
	prompt := fmt.Sprintf(s.prompt.Template, now.Format("2006-01-02"), artistName, officialSiteURL)
	if len(links) > 0 {
		var b strings.Builder
		b.WriteString(s.prompt.LinkedPagesHeader)
		for _, l := range links {
			if l.Label != "" {
				b.WriteString(l.Label + ": ")
			}
			b.WriteString(l.URL + "\n")
		}
		prompt += b.String()
	}

	// The API rejects sub-second precision in time_range_filter
	// ("Granularity of nano is not supported").
	end := now.UTC().Truncate(time.Second)
	searchTool := &genai.Tool{
		GoogleSearch: &genai.GoogleSearch{
			TimeRangeFilter: &genai.Interval{
				StartTime: end.AddDate(0, -searchWindowMonths, 0),
				EndTime:   end,
			},
		},
	}
	urlCtxTool := &genai.Tool{URLContext: &genai.URLContext{}}
	tools := []*genai.Tool{searchTool, urlCtxTool}
	if s.withoutGoogleSearch {
		tools = []*genai.Tool{urlCtxTool}
	}

	cfg := &genai.GenerateContentConfig{
		SystemInstruction: &genai.Content{
			Parts: []*genai.Part{{Text: s.prompt.SystemInstruction}},
		},
		Tools:              tools,
		Temperature:        s.config.temperature(),
		MaxOutputTokens:    maxOutputTokens,
		ResponseMIMEType:   "application/json",
		ResponseJsonSchema: singleStepResponseSchema,
	}
	if level := thinkingLevelFromConfig(s.config.ThinkingLevel); level != genai.ThinkingLevelUnspecified {
		cfg.ThinkingConfig = &genai.ThinkingConfig{ThinkingLevel: level}
	}
	if s.config.IncludeServerSideToolInvocations {
		include := true
		cfg.ToolConfig = &genai.ToolConfig{IncludeServerSideToolInvocations: &include}
	}
	return prompt, cfg
}

// searchAttrs returns the log attributes that identify one concert search.
func (s *ConcertSearcher) searchAttrs(artist *entity.Artist, officialSiteURL string, from time.Time) []slog.Attr {
	return []slog.Attr{
		slog.String("artistID", artist.ID),
		slog.String("model", s.config.Model),
		slog.String("artist", artist.Name),
		slog.String("official_site", officialSiteURL),
		slog.String("from", from.Format("2006-01-02")),
	}
}

// checkGroundedPass returns the error the outcome of the grounded pass makes
// of the search: its own permanent failure, Unavailable when every attempt
// failed transiently (so the caller records the search as failed and searches
// again on the next run, instead of reading it as "no concerts"), or the end of
// the caller's context. Each call runs detached from ctx so an in-flight
// request is not cut off mid-response; a call that succeeds after the caller's
// deadline or cancellation must still fail the search: the caller has given up
// on it.
func checkGroundedPass(ctx context.Context, pm *PassMetadata, transient bool, err error, attrs []slog.Attr, logger *logging.Logger) error {
	if err != nil {
		logger.Warn(ctx, "grounded call failed permanently, aborting Search",
			append(attrs, slog.String("error", err.Error()))...)
		return err
	}
	if transient {
		pm.ExhaustedTransient = true
		return apperr.New(codes.Unavailable, "gemini concert search failed transiently on every attempt", attrs...)
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return toAppErr(ctxErr, "caller's context ended during the gemini concert search", attrs...)
	}
	return nil
}

// groundedPass runs the grounded search call. When the call is rejected as an
// invalid request, it re-sends it once without the tool-invocation report.
func (s *ConcertSearcher) groundedPass(
	ctx context.Context,
	artistName, officialSiteURL string,
	links []pageLink,
	attrs []slog.Attr,
	base posthog.LLMGeneration,
	gens *[]posthog.LLMGeneration,
) (*PassMetadata, string, bool, error) {
	prompt, cfg := s.buildRequest(artistName, officialSiteURL, links, time.Now().UTC())
	pm, rawText, transient, err := s.executePass(ctx, s.config.Model, prompt, cfg, attrs, base, gens)
	if err != nil && cfg.ToolConfig != nil && isInvalidRequest(err) {
		// Some pages break the Preview tool-invocation report after URL
		// context reads them, and the request fails with 400 (backend #542).
		// Re-send it once without the report.
		s.logger.Warn(ctx, "grounded call rejected as invalid, retrying once without the tool-invocation report",
			append(attrs, slog.String("error", err.Error()))...)
		reduced := *cfg
		reduced.ToolConfig = nil
		first := pm
		pm, rawText, transient, err = s.executePass(ctx, s.config.Model, prompt, &reduced,
			append(slices.Clone(attrs), slog.Bool("tool_report_dropped", true)), base, gens)
		pm.RetryCount += first.RetryCount
		pm.ToolReportDropped = true
	}
	return pm, rawText, transient, err
}

// recordDiscoveredURLs sets the URLs the grounded pass retrieved on md.
func recordDiscoveredURLs(md *SearchMetadata, pm *PassMetadata) {
	if pm == nil {
		return
	}
	urls := make([]string, 0, len(pm.URLContextRetrieved))
	for _, u := range pm.URLContextRetrieved {
		urls = append(urls, u.URL)
	}
	md.DiscoveredURLs = urls
	md.DiscoveredURLCount = len(urls)
}

// invalidJSONError wraps err, the failure to parse rawText, with the start of
// the text for the log.
func invalidJSONError(err error, rawText string, attrs []slog.Attr) error {
	truncated := rawText
	if len(truncated) > maxRawTextLogLen {
		truncated = truncated[:maxRawTextLogLen]
	}
	return toAppErr(err, "gemini returned invalid JSON",
		append(attrs,
			slog.String("raw_text_truncated", truncated),
			slog.Int("raw_text_len", len(rawText)),
		)...)
}

// executePass runs one Gemini call wrapped in exponential backoff
// (3 attempts, 1s/2s/4s, 60s max). It captures all observable metadata
// into a fresh PassMetadata. Returns:
//   - (pm, rawText, false, nil) on success
//   - (pm, "", true, nil) when retries are exhausted with transient errors
//   - (pm, "", false, err) on permanent error, including a response with no
//     candidate (errNoCandidates), one stopped by TOO_MANY_TOOL_CALLS
//     (errTooManyToolCalls) or one that ends normally without text
//     (errNoText), none retried
//
// Non-STOP finish_reason is treated as transient and retried.
//
// Each request sent is appended to gens as base filled from its response,
// with its latency and, for a failed one, its error kind.
func (s *ConcertSearcher) executePass(
	ctx context.Context,
	modelName string,
	prompt string,
	cfg *genai.GenerateContentConfig,
	attrs []slog.Attr,
	base posthog.LLMGeneration,
	gens *[]posthog.LLMGeneration,
) (*PassMetadata, string, bool, error) {
	pm := &PassMetadata{}
	bo := &backoff.ExponentialBackOff{
		InitialInterval: 1 * time.Second,
		Multiplier:      2.0,
		MaxInterval:     60 * time.Second,
	}

	var out passOutcome
	rawText, err := backoff.Retry(ctx, func() (string, error) {
		pm.RetryCount++
		return s.attemptPass(ctx, modelName, prompt, cfg, attrs, base, gens, pm, &out)
	}, backoff.WithBackOff(bo), backoff.WithMaxTries(3))

	if err != nil {
		return s.failedPass(ctx, pm, err, attrs, out)
	}
	return pm, rawText, false, nil
}

// passOutcome is what the attempts of one pass have shown so far: whether the
// last one ended on a non-STOP finish reason, and whether one failed
// permanently.
type passOutcome struct {
	lastWasFinish bool
	sawPermanent  bool
}

// attemptPass makes one Gemini call of a pass and reads its response into pm,
// which lives across the attempts. A permanent failure is returned wrapped in
// [backoff.Permanent]; out records how the attempt ended.
func (s *ConcertSearcher) attemptPass(
	ctx context.Context,
	modelName string,
	prompt string,
	cfg *genai.GenerateContentConfig,
	attrs []slog.Attr,
	base posthog.LLMGeneration,
	gens *[]posthog.LLMGeneration,
	pm *PassMetadata,
	out *passOutcome,
) (string, error) {
	// Detach from the parent context for the duration of one Gemini call,
	// then re-impose a per-attempt 120 s budget. Parent cancellation
	// (handler timeout, SIGTERM on the CronJob) still stops `backoff.Retry`
	// from scheduling another attempt — the outer `ctx` is what backoff
	// monitors — so cancellation propagation is preserved at the retry
	// level. We only protect the in-flight `GenerateContent` from mid-
	// response cancellation, which would otherwise leave Gemini in an
	// ambiguous state (URLContext fetch partially started, grounding
	// chunks recorded but not delivered). Worst-case teardown is one
	// stuck request × 120 s on top of the parent's deadline.
	reqCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), geminiCallTimeout)
	defer cancel()

	start := time.Now()
	resp, err := s.client.Models.GenerateContent(reqCtx, modelName, genai.Text(prompt), cfg)
	gen := base
	gen.Model = modelName
	gen.Latency = time.Since(start)
	gen.EndTime = start.Add(gen.Latency)
	fillFromResponse(&gen, resp)
	record := func(errorKind string) {
		gen.ErrorKind = errorKind
		*gens = append(*gens, gen)
	}
	if err != nil {
		return "", s.callFailed(ctx, err, attrs, record, out)
	}

	// Reset per-attempt accumulators BEFORE we read the new response.
	// `pm` lives across retries; without this reset the grounding
	// slices would accumulate URLs/URLRetrievals from every attempt
	// (including the failed ones), producing inflated counts. Scalar
	// fields are unconditionally overwritten below, but slice
	// appends need an explicit reset.
	pm.GroundingChunkURLs = nil
	pm.URLContextRetrieved = nil
	pm.WebSearchQueriesList = nil
	pm.WebSearchQueries = 0
	pm.RenderedParts = 0
	pm.URLContextCalls, pm.URLContextSucceeded = 0, 0

	recordUsage(pm, resp)

	respAttrs := usageAttrs(pm, resp)
	if len(resp.Candidates) == 0 {
		return "", s.noCandidates(ctx, resp, respAttrs, attrs, record, out)
	}
	candidate := resp.Candidates[0]
	joined, candidateAttrs := recordCandidate(pm, candidate, respAttrs)

	return s.judgeCandidate(ctx, candidate, joined, attrs, candidateAttrs, record, out)
}

// callFailed records a failed Gemini call and returns its error, wrapped in
// [backoff.Permanent] when it is not retryable.
func (s *ConcertSearcher) callFailed(ctx context.Context, err error, attrs []slog.Attr, record func(string), out *passOutcome) error {
	record(callErrorKind(ctx, err))
	out.lastWasFinish = false
	s.logger.Warn(ctx, "gemini model call failed",
		append(attrs, slog.String("error", err.Error()))...)
	if !isRetryable(err) {
		out.sawPermanent = true
		return backoff.Permanent(err)
	}
	return err
}

// usageAttrs returns the log attributes of a response's id and token usage.
func usageAttrs(pm *PassMetadata, resp *genai.GenerateContentResponse) []slog.Attr {
	return []slog.Attr{
		slog.String("response_id", resp.ResponseID),
		slog.Group("usage_metadata",
			slog.Int("prompt", int(pm.PromptTokens)),
			slog.Int("candidates", int(pm.CandidatesTokens)),
			slog.Int("thinking", int(pm.ThinkingTokens)),
			slog.Int("total", int(pm.TotalTokens)),
			slog.Int("tool_use", int(pm.ToolUseTokens)),
		),
	}
}

// noCandidates records a response with no candidate, which is a permanent
// failure, and returns its error.
func (s *ConcertSearcher) noCandidates(ctx context.Context, resp *genai.GenerateContentResponse, respAttrs, attrs []slog.Attr, record func(string), out *passOutcome) error {
	record(errorKindNoCandidates)
	out.lastWasFinish = false
	out.sawPermanent = true
	if pf := resp.PromptFeedback; pf != nil {
		respAttrs = append(respAttrs, slog.Group("prompt_feedback",
			slog.String("block_reason", string(pf.BlockReason)),
			slog.String("block_reason_message", pf.BlockReasonMessage),
		))
	}
	s.logger.Warn(ctx, "Gemini returned no candidates (permanent, not retrying)", append(attrs, respAttrs...)...)
	return backoff.Permanent(errNoCandidates())
}

// judgeCandidate decides what the first candidate's finish reason and text
// make of the attempt: too many tool calls and an empty text are permanent
// failures, an incomplete response is retried, and a text is the result.
func (s *ConcertSearcher) judgeCandidate(
	ctx context.Context,
	candidate *genai.Candidate,
	joined string,
	attrs, candidateAttrs []slog.Attr,
	record func(string),
	out *passOutcome,
) (string, error) {
	if candidate.FinishReason == genai.FinishReasonTooManyToolCalls {
		record(errorKindTooManyToolCalls)
		out.lastWasFinish = false
		out.sawPermanent = true
		s.logger.Warn(ctx, "gemini stopped after too many tool calls (permanent, not retrying)",
			append(attrs, candidateAttrs...)...)
		return "", backoff.Permanent(errTooManyToolCalls())
	}

	// The finish reason is checked before the text: an incomplete response
	// often carries no text at all and must be retried, not read as
	// "no concerts".
	if candidate.FinishReason != genai.FinishReasonStop && candidate.FinishReason != "" {
		record(errorKindIncomplete)
		out.lastWasFinish = true
		finishErr := fmt.Errorf("gemini response not completed normally: finish_reason=%s", candidate.FinishReason)
		s.logger.Warn(ctx, "gemini response not completed normally, retrying",
			append(attrs, candidateAttrs...)...)
		return "", finishErr
	}
	out.lastWasFinish = false

	if joined == "" {
		record(errorKindNoText)
		out.sawPermanent = true
		s.logger.Warn(ctx, "candidate has no text parts (permanent, not retrying)",
			append(attrs, candidateAttrs...)...)
		return "", backoff.Permanent(errNoText())
	}
	record("")

	s.logger.Info(ctx, "successfully received Gemini response",
		append(attrs, candidateAttrs...)...)
	return joined, nil
}

// recordUsage copies the response's token counts into pm.
func recordUsage(pm *PassMetadata, resp *genai.GenerateContentResponse) {
	if u := resp.UsageMetadata; u != nil {
		pm.PromptTokens = u.PromptTokenCount
		pm.CandidatesTokens = u.CandidatesTokenCount
		pm.ThinkingTokens = u.ThoughtsTokenCount
		pm.ToolUseTokens = u.ToolUsePromptTokenCount
		pm.TotalTokens = u.TotalTokenCount
	}
}

// recordCandidate copies the first candidate's finish reason, grounding, URL
// context and parts into pm, and returns its text with the log attributes that
// describe it.
func recordCandidate(pm *PassMetadata, candidate *genai.Candidate, respAttrs []slog.Attr) (string, []slog.Attr) {
	pm.FinishReason = string(candidate.FinishReason)
	pm.FinishMessage = candidate.FinishMessage
	pm.AvgLogprobs = candidate.AvgLogprobs
	recordGrounding(pm, candidate)

	joined, toolQueries := recordParts(pm, candidate)
	if len(pm.WebSearchQueriesList) == 0 && len(toolQueries) > 0 {
		// groundingMetadata is often absent on Gemini 3 when the search
		// runs during thinking; fall back to the tool-call queries.
		pm.WebSearchQueriesList = toolQueries
		pm.WebSearchQueries = len(toolQueries)
	}
	pm.RawResponseText = joined

	candidateAttrs := append(respAttrs,
		slog.String("finish_reason", pm.FinishReason),
		slog.String("finish_message", pm.FinishMessage),
		slog.Float64("avg_logprobs", pm.AvgLogprobs),
		slog.Any("search_queries", pm.WebSearchQueriesList),
		slog.Int("web_search_queries", pm.WebSearchQueries),
		slog.Int("url_context_retrieved", len(pm.URLContextRetrieved)),
		slog.Int("url_context_calls", pm.URLContextCalls),
		slog.Int("url_context_succeeded", pm.URLContextSucceeded),
		slog.Int("parts_total", pm.PartsTotal),
		slog.Int("thought_parts", pm.ThoughtParts),
		slog.Int("text_parts", pm.TextParts),
	)
	return joined, candidateAttrs
}

// recordGrounding copies the candidate's grounding and URL-context metadata
// into pm.
func recordGrounding(pm *PassMetadata, candidate *genai.Candidate) {
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
}

// recordParts counts the candidate's parts into pm and returns the joined
// text and the Google Search queries of its server-side tool-call parts
// (present only with IncludeServerSideToolInvocations).
func recordParts(pm *PassMetadata, candidate *genai.Candidate) (string, []string) {
	var textBuf strings.Builder
	var totalParts, thoughtParts, textParts int
	var toolQueries []string
	// Content can be nil when the response was filtered out (SAFETY,
	// RECITATION, etc.). The FinishReason check below would surface the
	// failure, but a nil-pointer dereference here would panic the goroutine
	// before that guard runs. Treat nil Content as "no text" and let the
	// FinishReason / empty-text branches handle the diagnostic.
	if candidate.Content != nil {
		pm.URLContextCalls, pm.URLContextSucceeded = urlContextCounts(candidate.Content.Parts)
		for _, p := range candidate.Content.Parts {
			if p == nil {
				continue
			}
			totalParts++
			if tc := p.ToolCall; tc != nil {
				if tc.ToolType == genai.ToolTypeGoogleSearchWeb {
					toolQueries = append(toolQueries, searchQueriesOf(tc.Args)...)
				}
				continue
			}
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
	}
	pm.PartsTotal = totalParts
	pm.ThoughtParts = thoughtParts
	pm.TextParts = textParts
	return textBuf.String(), toolQueries
}

// failedPass turns the error of an exhausted or permanently failed pass into
// the result of executePass: a permanent error or a cancellation of the
// caller is returned as an error; retries exhausted on a transient failure or
// a non-STOP finish reason are reported as exhausted.
func (s *ConcertSearcher) failedPass(ctx context.Context, pm *PassMetadata, err error, attrs []slog.Attr, out passOutcome) (*PassMetadata, string, bool, error) {
	if out.sawPermanent {
		return pm, "", false, toAppErr(err, "failed to call Gemini API", attrs...)
	}
	// Parent context cancellation (handler deadline, SIGTERM) is a
	// caller-driven hard stop, not a remote transient failure — propagate
	// it as a permanent error so SearchExt surfaces the cancellation
	// up the stack rather than silently returning an empty result.
	if ctx.Err() != nil {
		return pm, "", false, toAppErr(err, "failed to call Gemini API", attrs...)
	}
	if out.lastWasFinish {
		s.logger.Warn(ctx, "executePass exhausted retries with non-STOP finish_reason",
			append(attrs, slog.String("last_error", err.Error()))...)
		return pm, "", true, nil
	}
	// Transient network exhaustion (HTTP 503, rate-limit, etc.): the
	// per-attempt RPC failed three times without ever reaching a
	// permanent / non-STOP-FinishReason path. SearchExt fails with Unavailable.
	s.logger.Warn(ctx, "executePass exhausted retries with transient network error",
		append(attrs, slog.String("last_error", err.Error()))...)
	return pm, "", true, nil
}

// classifySeries classifies each series of the buckets, in order, by its
// venues and returns them with the counts of tours, standalone series and
// events.
func classifySeries(buckets map[int]*entity.DiscoveredSeries, order []int) (discovered []*entity.DiscoveredSeries, tours, standalones, events int) {
	discovered = make([]*entity.DiscoveredSeries, 0, len(order))
	for _, k := range order {
		ds := buckets[k]
		ds.ClassifyType()
		discovered = append(discovered, ds)
		events += len(ds.Events)
		if ds.Type == entity.SeriesTypeTour {
			tours++
		} else {
			standalones++
		}
	}
	return discovered, tours, standalones, events
}

// mergeDrafts turns the drafts into discovered series: it drops events dated
// before the current date at their Venue at from, removes repeats by
// (local_date, venue as [entity.ListedVenueKey] gives it, start_time) keeping the
// first, groups events under their originating series entry, and classifies
// each series by its venues ([entity.DiscoveredSeries.ClassifyType]).
//
// start_time is part of the repeat key so that two shows on the same date at
// the same venue (e.g. Billboard Live 1st stage / 2nd stage) survive as
// distinct events. Two events that share (local_date, venue) AND both lack a
// published start_time collapse to one: we prefer to dedup conservatively
// when the disambiguator is missing. The trade-off is recall loss for the
// rare case of two genuinely distinct shows announced before their start
// times are published.
func (s *ConcertSearcher) mergeDrafts(
	ctx context.Context,
	drafts []EventDraft,
	from time.Time,
	md *SearchMetadata,
	attrs []slog.Attr,
) []*entity.DiscoveredSeries {
	seen := make(map[entity.DiscoveredEventKey]struct{}, len(drafts))
	buckets := make(map[int]*entity.DiscoveredSeries, len(drafts))
	var order []int
	for _, draft := range drafts {
		ev := s.toDiscoveredEvent(ctx, draft, from, attrs)
		if ev == nil {
			continue
		}
		// The normalized venue only forms the key; the returned event keeps
		// the venue as written.
		key := ev.RepeatKey()
		if _, dup := seen[key]; dup {
			s.logger.Warn(ctx, "duplicate event dropped by (local_date, normalized_venue, start_time) dedup",
				append(attrs,
					slog.String("title", draft.Title),
					slog.String("date", draft.LocalDate),
					slog.String("venue_raw", draft.Venue),
					slog.String("venue_normalized", key.Venue),
					slog.String("start_time", draft.StartTime),
				)...)
			continue
		}
		seen[key] = struct{}{}

		series, ok := buckets[draft.Group]
		if !ok {
			series = &entity.DiscoveredSeries{Title: draft.Title, SourceURL: draft.SourceURL}
			buckets[draft.Group] = series
			order = append(order, draft.Group)
		}
		series.Events = append(series.Events, ev)
	}

	discovered, toursCount, standalonesCount, eventCount := classifySeries(buckets, order)
	if md != nil {
		md.ToursCount = toursCount
		md.StandalonesCount = standalonesCount
	}

	s.logger.Info(ctx, "successfully parsed new concerts",
		append(attrs,
			slog.Int("draft_count", len(drafts)),
			slog.Int("series_count", len(discovered)),
			slog.Int("event_count", eventCount),
			slog.Int("tours_count", toursCount),
			slog.Int("standalones_count", standalonesCount),
		)...,
	)
	return discovered
}

// toDiscoveredEvent converts a draft into an entity.DiscoveredEvent.
// Series-level fields (Title / SourceURL / Type) are NOT carried here — the
// caller places the event under its parent DiscoveredSeries. Returns nil if
// the event must be skipped (unparseable date, or local_date is before
// `from`).
func (s *ConcertSearcher) toDiscoveredEvent(
	ctx context.Context,
	draft EventDraft,
	from time.Time,
	attrs []slog.Attr,
) *entity.DiscoveredEvent {
	date, err := time.Parse("2006-01-02", draft.LocalDate)
	if err != nil {
		s.logger.Warn(ctx, "failed to parse event date and skip",
			append(attrs, slog.String("date", draft.LocalDate), slog.String("title", draft.Title))...)
		return nil
	}

	// The event's local date is the date at its Venue, so it is past when it
	// is before the current date there at the search time, not before the
	// UTC day: at 23:30 UTC the Venue is already on the next day.
	if (&entity.DiscoveredEvent{LocalDate: date}).HasPassedAt(from) {
		s.logger.Debug(ctx, "filtered past event",
			append(attrs, slog.String("title", draft.Title), slog.String("date", draft.LocalDate))...,
		)
		return nil
	}

	var startTime time.Time
	if draft.StartTime != "" && draft.StartTime != "null" {
		if st, err := time.Parse(time.RFC3339, draft.StartTime); err != nil {
			s.logger.Warn(ctx, "failed to parse event start time, using zero",
				append(attrs, slog.String("start_time", draft.StartTime))...,
			)
		} else {
			startTime = st
		}
	}

	var openTime time.Time
	if draft.OpenTime != "" && draft.OpenTime != "null" {
		if ot, err := time.Parse(time.RFC3339, draft.OpenTime); err != nil {
			s.logger.Warn(ctx, "failed to parse event open time, using zero",
				append(attrs, slog.String("open_time", draft.OpenTime))...,
			)
		} else {
			openTime = ot
		}
	}

	adminArea, timeZone := s.resolveArea(ctx, draft, attrs)

	return &entity.DiscoveredEvent{
		ListedVenueName: draft.Venue,
		AdminArea:       adminArea,
		TimeZone:        timeZone,
		LocalDate:       date,
		StartTime:       startTime,
		OpenTime:        openTime,
	}
}

// resolveArea turns the model's admin area into the code of the first-level
// Area it resolves to without a country, and picks the event's time zone: the
// Area's own, else the model's when it is one of the country's zones. An area
// that does not resolve gives neither; so does a failure to read the
// reference data, which is logged, so the event is staged for review rather
// than lost.
func (s *ConcertSearcher) resolveArea(ctx context.Context, draft EventDraft, attrs []slog.Attr) (adminArea, timeZone *string) {
	if draft.AdminArea == "" {
		return nil, nil
	}
	area, err := s.areas.Resolve(ctx, draft.AdminArea, nil)
	if err != nil {
		if !errors.Is(err, apperr.ErrNotFound) && !errors.Is(err, apperr.ErrInvalidArgument) {
			s.logger.Warn(ctx, "failed to resolve the event's admin area",
				append(attrs, slog.String("admin_area", draft.AdminArea), slog.String("error", err.Error()))...)
		}
		return nil, nil
	}
	code := area.Code
	if area.TimeZone != nil {
		return &code, area.TimeZone
	}
	if draft.TimeZone == "" {
		return &code, nil
	}
	zones, err := s.areas.ListByCountry(ctx, area.CountryCode)
	if err != nil {
		s.logger.Warn(ctx, "failed to read the time zones of the event's country",
			append(attrs, slog.String("country", area.CountryCode), slog.String("error", err.Error()))...)
		return &code, nil
	}
	for _, z := range zones.TimeZones {
		if z.Name == draft.TimeZone {
			zone := z.Name
			return &code, &zone
		}
	}
	return &code, nil
}

// searchQueriesOf extracts the "queries" argument of a Google Search
// server-side tool call.
func searchQueriesOf(args map[string]any) []string {
	raw, ok := args["queries"].([]any)
	if !ok {
		return nil
	}
	queries := make([]string, 0, len(raw))
	for _, q := range raw {
		if s, ok := q.(string); ok && s != "" {
			queries = append(queries, s)
		}
	}
	return queries
}

// urlContextCounts counts, among server-side tool parts, the URL context
// calls and the URLs those calls retrieved with
// URL_RETRIEVAL_STATUS_SUCCESS. A URL context tool response carries
// {"url_metadata": [{"retrieved_url": ..., "url_retrieval_status": ...}]}.
func urlContextCounts(parts []*genai.Part) (calls, succeeded int) {
	for _, p := range parts {
		if p == nil {
			continue
		}
		if tc := p.ToolCall; tc != nil && tc.ToolType == genai.ToolTypeURLContext {
			calls++
		}
		tr := p.ToolResponse
		if tr == nil || tr.ToolType != genai.ToolTypeURLContext {
			continue
		}
		metadata, _ := tr.Response["url_metadata"].([]any)
		for _, m := range metadata {
			entry, _ := m.(map[string]any)
			if entry["url_retrieval_status"] == string(genai.URLRetrievalStatusSuccess) {
				succeeded++
			}
		}
	}
	return calls, succeeded
}

// isInvalidRequest reports whether err is the Gemini API rejecting the
// request as invalid (HTTP 400).
func isInvalidRequest(err error) bool {
	apiErr, ok := errors.AsType[genai.APIError](err)
	return ok && apiErr.Code == http.StatusBadRequest
}

// singleStepEvent is one event in the JSON response.
type singleStepEvent struct {
	Venue     string `json:"venue"`
	Country   string `json:"country"`
	AdminArea string `json:"admin_area"`
	TimeZone  string `json:"time_zone"`
	LocalDate string `json:"local_date"`
	OpenTime  string `json:"open_time"`
	StartTime string `json:"start_time"`
}

// singleStepSeries is one tour or show in the JSON response.
type singleStepSeries struct {
	Title     string            `json:"title"`
	SourceURL string            `json:"source_url"`
	Events    []singleStepEvent `json:"events"`
}

// singleStepResponse is the top-level JSON shape (matches
// singleStepResponseSchema).
type singleStepResponse struct {
	Series []singleStepSeries `json:"series"`
}

// singleStepEventSchema describes one concert date. Field formats and the
// verbatim rules live here, not in the system instruction.
var singleStepEventSchema = map[string]any{
	"type":                 "object",
	"additionalProperties": false,
	"properties": map[string]any{
		"venue": map[string]any{
			"type":        "string",
			"description": "Venue name exactly as printed on the source page for this date, character for character in its original language and spacing. Keep annotations about the venue itself, such as a former name in parentheses (e.g. 「クロコくんホール（旧 日本ガイシホール）」), but leave out show titles and subtitles printed next to it (e.g. 「～TAKUYA∞ 生誕祭～」). Do not translate it or replace it with a name you know.",
		},
		"country": map[string]any{
			"type":        "string",
			"description": "ISO 3166-1 alpha-2 code of the country where the concert is held (e.g. JP, TW).",
		},
		"admin_area": map[string]any{
			"type":        "string",
			"description": "ISO 3166-2 code of the venue's first-level subdivision (e.g. JP-13, TW-TPE, KR-11, US-CA). \"\" when uncertain.",
		},
		"time_zone": map[string]any{
			"type":        "string",
			"description": "IANA time zone name of the venue (e.g. Asia/Tokyo, Asia/Taipei, America/Chicago). \"\" when uncertain.",
		},
		"local_date": map[string]any{
			"type":        "string",
			"description": "Calendar date in YYYY-MM-DD. When the page omits the year, infer it from page context.",
		},
		"open_time": map[string]any{
			"type":        "string",
			"description": "Doors-open time in RFC3339 with the venue's UTC offset (e.g. 2026-02-14T17:30:00+09:00). \"\" when not published.",
		},
		"start_time": map[string]any{
			"type":        "string",
			"description": "Show start time in RFC3339 with the venue's UTC offset (e.g. 2026-02-14T18:30:00+09:00). \"\" when not published.",
		},
	},
	"required": []string{"venue", "country", "admin_area", "time_zone", "local_date", "open_time", "start_time"},
}

// singleStepSeriesSchema describes one tour or show (one or more dates).
var singleStepSeriesSchema = map[string]any{
	"type":                 "object",
	"additionalProperties": false,
	"properties": map[string]any{
		"title": map[string]any{
			"type":        "string",
			"description": "Tour or show title exactly as printed on the source page, character for character in its original language. Do not translate it.",
		},
		"source_url": map[string]any{
			"type":        "string",
			"description": "URL of the tour's dedicated page; if there is none, the URL of the official site's detail page for this concert.",
		},
		"events": map[string]any{
			"type":        "array",
			"description": "One entry per concert date of this tour or show.",
			"minItems":    1,
			"items":       singleStepEventSchema,
		},
	},
	"required": []string{"title", "source_url", "events"},
}

// singleStepResponseSchema is the structured-output schema of the grounded
// call. Tours and one-off shows share one list; Go classifies each series by
// its venues.
var singleStepResponseSchema = map[string]any{
	"type":                 "object",
	"additionalProperties": false,
	"description":          "Use \"\" for unknown string fields; never null.",
	"properties": map[string]any{
		"series": map[string]any{
			"type":        "array",
			"description": "One entry per tour or show. Put all dates of one tour or show in the same entry; put different shows in separate entries, even when their titles match.",
			"items":       singleStepSeriesSchema,
		},
	},
	"required": []string{"series"},
}

// parseSingleStepJSON converts the JSON response into a flat list of drafts
// in response order. Leading/trailing whitespace and a Markdown code fence are
// tolerated; anything else that does not unmarshal fails with errInvalidJSON.
func parseSingleStepJSON(raw string) ([]EventDraft, error) {
	text := strings.TrimSpace(raw)
	text = strings.TrimPrefix(text, "```json")
	text = strings.TrimPrefix(text, "```")
	text = strings.TrimSuffix(text, "```")
	var resp singleStepResponse
	if err := json.Unmarshal([]byte(strings.TrimSpace(text)), &resp); err != nil {
		return nil, errInvalidJSON(err)
	}
	var drafts []EventDraft
	for i, sr := range resp.Series {
		for _, ev := range sr.Events {
			drafts = append(drafts, EventDraft{
				Title:     strings.TrimSpace(sr.Title),
				SourceURL: strings.TrimSpace(sr.SourceURL),
				Venue:     strings.TrimSpace(ev.Venue),
				Country:   strings.TrimSpace(ev.Country),
				AdminArea: strings.TrimSpace(ev.AdminArea),
				TimeZone:  strings.TrimSpace(ev.TimeZone),
				LocalDate: strings.TrimSpace(ev.LocalDate),
				StartTime: strings.TrimSpace(ev.StartTime),
				OpenTime:  strings.TrimSpace(ev.OpenTime),
				// Entries are numbered from 1.
				Group: i + 1,
			})
		}
	}
	return drafts, nil
}
