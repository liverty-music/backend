package gemini

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/cenkalti/backoff/v5"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/geo"
	"github.com/pannpers/go-logging/logging"
	"google.golang.org/genai"
)

var (
	// errInvalidJSON is returned when the grounded call's response is not
	// JSON of the singleStepResponseSchema shape. It is not retried: the model
	// produced a structurally broken response and the same prompt is unlikely
	// to fix it.
	errInvalidJSON = errors.New("gemini returned invalid JSON")

	// errNoCandidates is returned when the API answers successfully but with
	// no candidate. It is not retried: the empty response is billed (tens to
	// hundreds of thousands of tool-use tokens) and a repeat of the same
	// request is likely to fail the same way. Failing the Search marks the
	// Artist's SearchLog failed so the next daily run searches again.
	errNoCandidates = errors.New("gemini returned no candidates")

	// errTooManyToolCalls is returned when the model stops with
	// TOO_MANY_TOOL_CALLS. It is not retried: the call has already run its
	// search queries (40-70 in prod) and a repeat usually falls into the same
	// search-only mode. Failing the Search marks the Artist's SearchLog failed
	// so the next daily run searches again.
	errTooManyToolCalls = errors.New("gemini stopped after too many tool calls")
)

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
	// systemInstruction states only scope, sources and the output contract.
	// The default-language rule keeps multilingual official pages (e.g. a
	// tour page with ?lang=en) from yielding romanized venue names. The
	// tour-page rule keeps the model from rebuilding a tour's dates from search
	// snippets: runs that never used url_context issued 25-80 queries and
	// misread dates and venues.
	// Field formats and verbatim rules live in singleStepResponseSchema;
	// repeat removal is done in Go. Reading the top page is not forced: that
	// made the model read it every time but did not reduce searches, because
	// URL context does not follow links.
	systemInstruction = `You are a data-extraction agent for a live-music information system.

Extract the tours and shows of the given artist taking place on or after the given start date: tours, one-off shows, and co-headliner bills (対バン) organized by the artist. Exclude music festivals and cancelled shows. Return each tour or show as one series with one event per date.

Use only the artist's official site and official tour pages as sources. Do not use third-party sites. When a tour has a dedicated page, read that page with url_context and take every date of the tour from it. When a page is offered in several languages, read its default-language version (the URL without a language parameter such as ?lang=) and copy text in that language.

Respond with JSON that follows the response schema.
`

	// promptTemplate carries the per-call variables: %[1]s the start date
	// (YYYY-MM-DD), %[2]s the artist name, %[3]s the official-site URL. The
	// URL is passed in full because URL context only fetches URLs with a
	// scheme.
	promptTemplate = `Extract the tours and shows of %[2]s taking place on or after %[1]s.

Official site: %[3]s
`

	// searchWindowMonths bounds the GoogleSearch TimeRangeFilter to pages
	// from the last 2 months.
	searchWindowMonths = 2

	// maxOutputTokens is the default response cap.
	maxOutputTokens = int32(16384)

	maxRawTextLogLen  = 1000
	geminiCallTimeout = 120 * time.Second
)

// Prompt is the system instruction and user prompt template of the grounded
// call. Template carries the indexed verbs described on promptTemplate.
type Prompt struct {
	SystemInstruction string
	Template          string
}

// defaultPrompt is the production prompt.
var defaultPrompt = Prompt{SystemInstruction: systemInstruction, Template: promptTemplate}

// EventDraft is one event of the grounded call's JSON response, flattened
// with its series-level fields, before the past-date filter, repeat removal
// and series grouping.
type EventDraft struct {
	Title     string // series title, verbatim.
	SourceURL string // series source page.
	Venue     string // venue name, verbatim.
	Country   string // ISO 3166-1 alpha-2.
	AdminArea string // ISO 3166-2 as returned by the model (normalized later).
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
	config Config
	logger *logging.Logger
	prompt Prompt
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

	// ExhaustedTransient is true when the retry policy ran to the end with
	// the call still in a transient-error state and the result was forced to
	// empty. Surfaced so observability can distinguish "model found nothing"
	// (false) from "infra failed, output was forced to empty" (true).
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
func NewConcertSearcher(ctx context.Context, cfg Config, httpClient *http.Client, logger *logging.Logger) (*ConcertSearcher, error) {
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
		client: client,
		config: cfg,
		logger: logger,
		prompt: defaultPrompt,
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
	var officialSiteURL string
	if officialSite != nil {
		officialSiteURL = officialSite.URL
	}

	attrs := []slog.Attr{
		slog.String("artistID", artist.ID),
		slog.String("model", s.config.Model),
		slog.String("artist", artist.Name),
		slog.String("official_site", officialSiteURL),
		slog.String("from", from.Format("2006-01-02")),
	}
	s.logger.Info(ctx, "start calling Gemini API to search concerts", attrs...)

	md := &SearchMetadata{}

	prompt, cfg := s.buildRequest(artist.Name, officialSiteURL, time.Now().UTC())
	pm, rawText, transient, err := s.executePass(ctx, s.config.Model, prompt, cfg, attrs)
	md.Grounded = pm
	if pm != nil {
		urls := make([]string, 0, len(pm.URLContextRetrieved))
		for _, u := range pm.URLContextRetrieved {
			urls = append(urls, u.URL)
		}
		md.DiscoveredURLs = urls
		md.DiscoveredURLCount = len(urls)
	}
	if err != nil {
		s.logger.Warn(ctx, "grounded call failed permanently, aborting Search",
			append(attrs, slog.String("error", err.Error()))...)
		return nil, md, err
	}
	if transient {
		s.logger.Warn(ctx, "grounded call exhausted retries with transient error, returning empty results", attrs...)
		pm.ExhaustedTransient = true
		return nil, md, nil
	}
	if rawText == "" {
		s.logger.Warn(ctx, "grounded call returned no text, returning empty results", attrs...)
		return nil, md, nil
	}

	drafts, err := parseSingleStepJSON(rawText)
	md.DraftCount = len(drafts)
	if err != nil {
		md.InvalidJSON = true
		truncated := rawText
		if len(truncated) > maxRawTextLogLen {
			truncated = truncated[:maxRawTextLogLen]
		}
		return nil, md, toAppErr(err, "gemini returned invalid JSON",
			append(attrs,
				slog.String("raw_text_truncated", truncated),
				slog.Int("raw_text_len", len(rawText)),
			)...)
	}
	if len(drafts) == 0 {
		s.logger.Info(ctx, "grounded call returned 0 events", attrs...)
		return nil, md, nil
	}
	return s.mergeDrafts(ctx, drafts, from, md, attrs), md, nil
}

// buildRequest returns the user prompt and the request config of the
// grounded call. now anchors the prompt's start date and the GoogleSearch
// time range.
func (s *ConcertSearcher) buildRequest(artistName, officialSiteURL string, now time.Time) (string, *genai.GenerateContentConfig) {
	prompt := fmt.Sprintf(s.prompt.Template, now.Format("2006-01-02"), artistName, officialSiteURL)

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

	cfg := &genai.GenerateContentConfig{
		SystemInstruction: &genai.Content{
			Parts: []*genai.Part{{Text: s.prompt.SystemInstruction}},
		},
		Tools:              []*genai.Tool{searchTool, urlCtxTool},
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

// executePass runs one Gemini call wrapped in exponential backoff
// (3 attempts, 1s/2s/4s, 60s max). It captures all observable metadata
// into a fresh PassMetadata. Returns:
//   - (pm, rawText, false, nil) on success
//   - (pm, "", true, nil) when retries are exhausted with transient errors
//   - (pm, "", false, err) on permanent error, including a response with no
//     candidate (errNoCandidates) or one stopped by TOO_MANY_TOOL_CALLS
//     (errTooManyToolCalls), neither retried
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
			lastWasFinish = false
			sawPermanent = true
			if pf := resp.PromptFeedback; pf != nil {
				respAttrs = append(respAttrs, slog.Group("prompt_feedback",
					slog.String("block_reason", string(pf.BlockReason)),
					slog.String("block_reason_message", pf.BlockReasonMessage),
				))
			}
			s.logger.Warn(ctx, "Gemini returned no candidates (permanent, not retrying)", append(attrs, respAttrs...)...)
			return "", backoff.Permanent(errNoCandidates)
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

		var textBuf strings.Builder
		var totalParts, thoughtParts, textParts int
		// toolQueries collects the Google Search queries from server-side
		// tool-call parts (present only with IncludeServerSideToolInvocations).
		var toolQueries []string
		// Content can be nil when the response was filtered out (SAFETY,
		// RECITATION, etc.). The FinishReason check below would surface the
		// failure, but a nil-pointer dereference here would panic the goroutine
		// before that guard runs. Treat nil Content as "no text" and let the
		// FinishReason / empty-text branches handle the diagnostic.
		if candidate.Content != nil {
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
		if len(pm.WebSearchQueriesList) == 0 && len(toolQueries) > 0 {
			// groundingMetadata is often absent on Gemini 3 when the search
			// runs during thinking; fall back to the tool-call queries.
			pm.WebSearchQueriesList = toolQueries
			pm.WebSearchQueries = len(toolQueries)
		}

		candidateAttrs := append(respAttrs,
			slog.String("finish_reason", pm.FinishReason),
			slog.String("finish_message", pm.FinishMessage),
			slog.Float64("avg_logprobs", pm.AvgLogprobs),
			slog.Any("search_queries", pm.WebSearchQueriesList),
			slog.Int("web_search_queries", pm.WebSearchQueries),
			slog.Int("url_context_retrieved", len(pm.URLContextRetrieved)),
		)
		joined := textBuf.String()
		pm.RawResponseText = joined
		candidateAttrs = append(candidateAttrs,
			slog.Int("parts_total", totalParts),
			slog.Int("thought_parts", thoughtParts),
			slog.Int("text_parts", textParts),
		)

		if candidate.FinishReason == genai.FinishReasonTooManyToolCalls {
			lastWasFinish = false
			sawPermanent = true
			s.logger.Warn(ctx, "gemini stopped after too many tool calls (permanent, not retrying)",
				append(attrs, candidateAttrs...)...)
			return "", backoff.Permanent(errTooManyToolCalls)
		}

		// The finish reason is checked before the text: an incomplete response
		// often carries no text at all and must be retried, not read as
		// "no concerts".
		if candidate.FinishReason != genai.FinishReasonStop && candidate.FinishReason != "" {
			lastWasFinish = true
			finishErr := fmt.Errorf("gemini response not completed normally: finish_reason=%s", candidate.FinishReason)
			s.logger.Warn(ctx, "gemini response not completed normally, retrying",
				append(attrs, candidateAttrs...)...)
			return "", finishErr
		}
		lastWasFinish = false

		if joined == "" {
			s.logger.Warn(ctx, "candidate has no text parts",
				append(attrs, candidateAttrs...)...)
			return "", nil
		}

		s.logger.Info(ctx, "successfully received Gemini response",
			append(attrs, candidateAttrs...)...)
		return joined, nil
	}, backoff.WithBackOff(bo), backoff.WithMaxTries(3))

	if err != nil {
		if sawPermanent {
			return pm, "", false, toAppErr(err, "failed to call Gemini API", attrs...)
		}
		// Parent context cancellation (handler deadline, SIGTERM) is a
		// caller-driven hard stop, not a remote transient failure — propagate
		// it as a permanent error so SearchExt surfaces the cancellation
		// up the stack rather than silently returning an empty result.
		if ctx.Err() != nil {
			return pm, "", false, toAppErr(err, "failed to call Gemini API", attrs...)
		}
		if lastWasFinish {
			s.logger.Warn(ctx, "executePass exhausted retries with non-STOP finish_reason",
				append(attrs, slog.String("last_error", err.Error()))...)
			return pm, "", true, nil
		}
		// Transient network exhaustion (HTTP 503, rate-limit, etc.): the
		// per-attempt RPC failed three times without ever reaching a
		// permanent / non-STOP-FinishReason path. Degrade to no results.
		s.logger.Warn(ctx, "executePass exhausted retries with transient network error",
			append(attrs, slog.String("last_error", err.Error()))...)
		return pm, "", true, nil
	}
	return pm, rawText, false, nil
}

// mergeDrafts turns the drafts into discovered series: it drops events dated
// before from, removes repeats by (local_date, normalized venue, start_time)
// keeping the first, groups events under their originating series entry, and
// classifies each series by its venues (seriesTypeOf).
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
	type dedupKey struct {
		date      string
		venue     string
		startTime string
	}
	seen := make(map[dedupKey]struct{}, len(drafts))
	buckets := make(map[int]*entity.DiscoveredSeries, len(drafts))
	var order []int
	for _, draft := range drafts {
		ev := s.toDiscoveredEvent(ctx, draft, from, attrs)
		if ev == nil {
			continue
		}
		// The normalized venue only forms the key; the returned event keeps
		// the venue as written.
		key := dedupKey{date: draft.LocalDate, venue: NormalizeVenue(draft.Venue), startTime: draft.StartTime}
		if _, dup := seen[key]; dup {
			s.logger.Warn(ctx, "duplicate event dropped by (local_date, normalized_venue, start_time) dedup",
				append(attrs,
					slog.String("title", draft.Title),
					slog.String("date", draft.LocalDate),
					slog.String("venue_raw", draft.Venue),
					slog.String("venue_normalized", key.venue),
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

	discovered := make([]*entity.DiscoveredSeries, 0, len(order))
	var toursCount, standalonesCount, eventCount int
	for _, k := range order {
		ds := buckets[k]
		ds.Type = seriesTypeOf(ds.Events)
		discovered = append(discovered, ds)
		eventCount += len(ds.Events)
		if ds.Type == entity.SeriesTypeTour {
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
			slog.Int("series_count", len(discovered)),
			slog.Int("event_count", eventCount),
			slog.Int("tours_count", toursCount),
			slog.Int("standalones_count", standalonesCount),
		)...,
	)
	return discovered
}

// seriesTypeOf classifies a series by its venues, following the SeriesType
// definitions: TOUR when its events are at two or more venues (compared by
// NormalizeVenue, ignoring venues not yet announced), SINGLE otherwise — a
// one-off show or several days at one venue.
func seriesTypeOf(events []*entity.DiscoveredEvent) entity.SeriesType {
	venues := make(map[string]struct{}, len(events))
	for _, ev := range events {
		if v := NormalizeVenue(ev.ListedVenueName); v != "" {
			venues[v] = struct{}{}
		}
	}
	if len(venues) >= 2 {
		return entity.SeriesTypeTour
	}
	return entity.SeriesTypeSingle
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

	if date.Before(from.Truncate(24 * time.Hour)) {
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

	var adminArea *string
	if draft.AdminArea != "" {
		adminArea = geo.NormalizeAdminArea(draft.AdminArea)
	}

	return &entity.DiscoveredEvent{
		ListedVenueName: draft.Venue,
		AdminArea:       adminArea,
		LocalDate:       date,
		StartTime:       startTime,
		OpenTime:        openTime,
	}
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

// singleStepEvent is one event in the JSON response.
type singleStepEvent struct {
	Venue     string `json:"venue"`
	Country   string `json:"country"`
	AdminArea string `json:"admin_area"`
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
	"required": []string{"venue", "country", "admin_area", "local_date", "open_time", "start_time"},
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
		return nil, fmt.Errorf("%w: %w", errInvalidJSON, err)
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
