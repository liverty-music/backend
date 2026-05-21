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

// errInvalidJSON is a sentinel error returned by parseEvents when the Gemini response
// contains invalid JSON. This is treated as a transient (retryable) error.
var errInvalidJSON = errors.New("gemini returned invalid JSON")

// Config holds the configuration for Gemini searcher.
type Config struct {
	ProjectID   string
	Location    string
	ModelName   string
	DataStoreID string

	// Temperature is the sampling temperature passed to GenerateContent.
	// Zero is a valid sampling parameter; callers MUST set this explicitly.
	Temperature float32

	// ThinkingLevel is the Gemini 3 thinking level for the searcher call.
	// Accepted: "", "low", "medium", "high" (case-insensitive). Empty
	// leaves the SDK/model default in effect (no ThinkingConfig sent).
	ThinkingLevel string

	// APIKey, when non-empty, selects the Gemini API direct backend
	// (BackendGeminiAPI) with API-key auth instead of Vertex AI + ADC.
	// Required to access URLContext, TimeRangeFilter and ExcludeDomains
	// which are not supported via the Vertex AI backend.
	APIKey string
}

// thinkingLevelFromConfig maps the lowercase env-style value to the SDK enum.
// Empty input returns ThinkingLevelUnspecified, signalling "do not set ThinkingConfig".
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
	// systemInstruction is intentionally short — every behavioural rule
	// (scope, INCLUDE/EXCLUDE, verbatim extraction) lives in the user prompt
	// below so it is co-located with the data the model is acting on. The
	// minimal-prompt variant produced the same UVERworld smoke result with
	// 4× lower cost, but on Vaundy (51 events) the long form recovered F1
	// from 0 → 0.95 by re-introducing explicit STEP 1 / STEP 2 directives.
	systemInstruction = `You are a concert-extraction agent.
PRIORITY: accuracy > completeness. When uncertain, omit rather than guess.`

	// promptTemplateWithSite (4 placeholders): artist, site, date, site.
	promptTemplateWithSite = `<goal>
Extract upcoming concerts for "%s" from the artist's official site %s
on or after %s.

PRIORITY: accuracy > completeness.
A missing concert is fixable later; a fabricated or wrong concert misleads users.
When uncertain about a field, return empty string for it.
When uncertain whether an event qualifies, omit it.

INCLUDE:
- The artist's own headline tours
- One-off solo shows
- Fan-club exclusive lives
- Split-bill / co-headline shows (対バン) where the artist is named
  alongside a small number (typically 2-4) of co-headliners

EXCLUDE:
- Multi-artist music festivals (10+ artists in a single lineup) where
  this artist is one of many performers
</goal>

<sources>
STEP 1 — MANDATORY: call url_context with the EXACT URL %s.
Read its full HTML and find every link to tour-feature, news-detail,
per-show, schedule, or live-information pages.

STEP 2 — MANDATORY: for EACH such linked URL discovered in step 1, call
url_context AGAIN to fetch its full content. Recurse: if a fetched page
reveals more event-detail URLs, fetch those too. Cross-domain official
URLs are allowed (e.g. member.<artist>.jp, sp.<artist>.com).
Example: if step 1 fetches https://www.uverworld.jp/ and that page links
to https://www.uverworld.jp/feature/2026_live, call url_context on that
feature URL next.
</sources>
`

	// promptTemplateWithoutSite (2 placeholders): artist, date.
	promptTemplateWithoutSite = `<goal>
Extract upcoming concerts for "%s" on or after %s.

PRIORITY: accuracy > completeness.
A missing concert is fixable later; a fabricated or wrong concert misleads users.
When uncertain about a field, return empty string for it.
When uncertain whether an event qualifies, omit it.

INCLUDE:
- The artist's own headline tours
- One-off solo shows
- Fan-club exclusive lives
- Split-bill / co-headline shows (対バン)

EXCLUDE:
- Multi-artist music festivals (10+ artists in a single lineup)
</goal>

<sources>
Use google_search with site:<artist-domain> queries to locate the
artist's official website. Once found, fetch it via url_context,
then RECURSIVELY use url_context on every tour-feature, news-detail,
per-show, or schedule URL it links to.
</sources>
`

	// maxOutputTokens defines the maximum length of Gemini's response.
	// 16384 tokens provides sufficient headroom for large batches of concert data
	// (e.g., a tour list with 30+ dates) in detailed JSON format.
	maxOutputTokens = int32(16384)

	// maxRawTextLogLen is the maximum number of characters of Gemini's raw response
	// text to include in WARN logs. Truncated to stay within Cloud Logging limits.
	maxRawTextLogLen = 1000

	// geminiCallTimeout is the per-attempt timeout for each Gemini API call.
	// Google Search grounding typically takes 25-110s; 120s provides sufficient
	// headroom for a single attempt.
	geminiCallTimeout = 120 * time.Second
)

// Shared field definitions reused by tourEventSchema and standaloneSchema.
// Field-level descriptions are the model's only source of per-field rules
// (the prompt does not duplicate them).
var (
	venueField = map[string]any{
		"type":        "string",
		"description": "Venue name exactly as written on the source page. No translation, no normalization.",
	}
	adminAreaField = map[string]any{
		"type":        "string",
		"description": "Administrative area (prefecture / state / province) of the venue. Populate ONLY when explicitly stated or unambiguously inferable from the venue name or surrounding context. Empty string \"\" otherwise. Wrong values are strictly forbidden.",
	}
	localDateField = map[string]any{
		"type":        "string",
		"description": "Local calendar date in YYYY-MM-DD format.",
	}
	startTimeField = map[string]any{
		"type":        "string",
		"description": "Start time in ISO 8601 with timezone (e.g. 2026-02-14T18:30:00+09:00). Empty string \"\" if unknown.",
	}
	openTimeField = map[string]any{
		"type":        "string",
		"description": "Door opening time in ISO 8601 with timezone. Empty string \"\" if unknown.",
	}
	sourceURLField = map[string]any{
		"type":        "string",
		"description": "Specific URL of the page where this concert was found. Prefer the most-direct per-show or per-tour link over the site root.",
	}
)

// tourEventSchema is one date within a tour. event title is inherited from
// the parent tour_title and not repeated per date.
var tourEventSchema = map[string]any{
	"type":                 "object",
	"additionalProperties": false,
	"properties": map[string]any{
		"venue":      venueField,
		"admin_area": adminAreaField,
		"local_date": localDateField,
		"start_time": startTimeField,
		"open_time":  openTimeField,
		"source_url": sourceURLField,
	},
	"required": []string{"venue", "local_date", "source_url"},
}

// tourSchema is a single tour with one or more dates.
var tourSchema = map[string]any{
	"type":                 "object",
	"additionalProperties": false,
	"properties": map[string]any{
		"tour_title": map[string]any{
			"type":        "string",
			"description": "Tour title exactly as written on the source page (e.g. \"UVERworld TYCOON LIVE -DOCUMENT-\"). No translation.",
		},
		"events": map[string]any{
			"type":  "array",
			"items": tourEventSchema,
		},
	},
	"required": []string{"tour_title", "events"},
}

// standaloneSchema is a one-off show that is not part of a multi-date tour:
// solo one-night shows, fan-club exclusive lives, and 対バン / co-headline
// shows where the artist is one of a small number (~2-4) of co-headliners.
var standaloneSchema = map[string]any{
	"type":                 "object",
	"additionalProperties": false,
	"properties": map[string]any{
		"event_title": map[string]any{
			"type":        "string",
			"description": "Event title exactly as written on the source page. No translation.",
		},
		"venue":      venueField,
		"admin_area": adminAreaField,
		"local_date": localDateField,
		"start_time": startTimeField,
		"open_time":  openTimeField,
		"source_url": sourceURLField,
	},
	"required": []string{"event_title", "venue", "local_date", "source_url"},
}

// responseJSONSchema is the top-level JSON Schema returned by the searcher.
//
// The object-level description carries rules that apply to every field of
// every event — placed here instead of repeating them per-field. Per-field
// rules live on each property above.
var responseJSONSchema = map[string]any{
	"type":                 "object",
	"additionalProperties": false,
	"description": "Upcoming concerts for one artist, grouped into multi-date tours and one-off standalone events." +
		" GLOBAL RULES applied to EVERY string field:" +
		" (1) Extract values VERBATIM from the source page — no translation, no normalization, no fabrication." +
		" (2) For any field whose value is absent or uncertain, return the empty string \"\"; NEVER emit null and NEVER guess.",
	"properties": map[string]any{
		"tours": map[string]any{
			"type":  "array",
			"items": tourSchema,
		},
		"standalones": map[string]any{
			"type":  "array",
			"items": standaloneSchema,
		},
	},
	"required": []string{"tours", "standalones"},
}

// ScrapedTourEvent is one date inside a ScrapedTour. The event title is
// carried by the parent ScrapedTour.TourTitle.
type ScrapedTourEvent struct {
	Venue     string `json:"venue"`
	AdminArea string `json:"admin_area"`
	LocalDate string `json:"local_date"`
	StartTime string `json:"start_time"`
	OpenTime  string `json:"open_time"`
	SourceURL string `json:"source_url"`
}

// ScrapedTour is a single multi-date tour.
type ScrapedTour struct {
	TourTitle string             `json:"tour_title"`
	Events    []ScrapedTourEvent `json:"events"`
}

// ScrapedStandalone is a one-off show (solo, FC live, 対バン).
type ScrapedStandalone struct {
	EventTitle string `json:"event_title"`
	Venue      string `json:"venue"`
	AdminArea  string `json:"admin_area"`
	LocalDate  string `json:"local_date"`
	StartTime  string `json:"start_time"`
	OpenTime   string `json:"open_time"`
	SourceURL  string `json:"source_url"`
}

// EventsResponse matches the JSON output from Gemini.
type EventsResponse struct {
	Tours       []ScrapedTour       `json:"tours"`
	Standalones []ScrapedStandalone `json:"standalones"`
}

// ConcertSearcher implements entity.ConcertSearcher using Vertex AI Gemini.
// It leverages Gemini's reasoning capabilities combined with Google Search Grounding
// to discover and extract structured concert information from the web.
type ConcertSearcher struct {
	client *genai.Client
	config Config
	logger *logging.Logger
}

// SearchMetadata captures per-call observation data used by the A/B evaluation
// harness. Production callers (entity.ScrapedConcert) do not see this.
type SearchMetadata struct {
	PromptTokens     int32
	CandidatesTokens int32
	ThinkingTokens   int32
	// ToolUseTokens is the count of tokens billed for tool invocations
	// (e.g. URLContext fetches). Treated as input tokens by the pricing
	// model.
	ToolUseTokens    int32
	TotalTokens      int32
	FinishReason     string
	// FinishMessage is the human-readable reason the model stopped (e.g. a
	// safety hit, a max-output cap reached internally). Often populated even
	// when FinishReason is STOP — useful for diagnosing truncation that
	// presents as a successful stop.
	FinishMessage string
	// AvgLogprobs is the candidate's average per-token log-probability.
	// Low values flag low-confidence emissions (often correlated with
	// fabricated content). Zero when the API does not return it.
	AvgLogprobs float64
	RetryCount  int
	InvalidJSON bool
	WebSearchQueries int
	RenderedParts    int
	// ToursCount / StandalonesCount are the structural sizes of the two
	// top-level arrays the model emitted under the tours/standalones
	// schema (before any flattening). Useful as a sanity check that the
	// model is using both buckets.
	ToursCount       int
	StandalonesCount int
	// Parts breakdown of the chosen candidate. PartsTotal counts every Part
	// (including FunctionCalls and Thoughts); ThoughtParts is those with
	// Thought=true; TextParts is non-thought parts with non-empty Text that
	// were joined to form RawResponseText.
	PartsTotal   int
	ThoughtParts int
	TextParts    int
	// RawResponseText is the model's final response text (the JSON body the
	// model produced, before parseEvents stripping). Populated for the last
	// retry attempt only. Empty if no attempt produced parseable output.
	RawResponseText string
	// WebSearchQueriesList is the raw list of grounding queries the model
	// issued — useful for understanding what it tried to look up.
	WebSearchQueriesList []string
	// GroundingChunkURLs is the list of source URLs surfaced by grounding
	// for offline analysis of why certain events were missed.
	GroundingChunkURLs []string
	// URLContextRetrieved is the list of URLs the URLContext tool actually
	// fetched (with their retrieval status). This is the concrete record
	// of which pages the model deep-read.
	URLContextRetrieved []URLRetrieval
}

// URLRetrieval is one entry in the URLContextMetadata list.
type URLRetrieval struct {
	URL    string `json:"url"`
	Status string `json:"status"`
}

// NewConcertSearcher creates a new ConcertSearcher.
//
// Backend selection:
//   - cfg.APIKey != "": Gemini API direct (BackendGeminiAPI) — unlocks
//     URLContext, TimeRangeFilter, ExcludeDomains. Used for the local PoC
//     and any future deploy that needs those features.
//   - cfg.APIKey == "": Vertex AI (BackendVertexAI) with ADC. Default for
//     production (existing IAM-managed flow).
//
// When httpClient is provided alongside Vertex AI + useADC, UseDefaultCredentials
// layers ADC auth on top of the custom transport (e.g., otelhttp). For the
// Gemini API direct path the API key is used directly and no ADC is needed.
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

// Search discovers new concerts for a given artist using Gemini with Grounding enabled.
// It sends a prompt to the model requesting a JSON list of upcoming events.
// Invalid dates are skipped, but invalid/null start times are preserved as nil pointers
// in the returned ScrapedConcert objects.
func (s *ConcertSearcher) Search(
	ctx context.Context,
	artist *entity.Artist,
	officialSite *entity.OfficialSite,
	from time.Time,
) ([]*entity.ScrapedConcert, error) {
	results, _, err := s.SearchExt(ctx, artist, officialSite, from)
	return results, err
}

// SearchExt is identical to Search but additionally returns per-call metadata
// (token counts, finish reason, retry count, grounding stats). Used by the
// A/B evaluation harness. Production callers should keep using Search.
func (s *ConcertSearcher) SearchExt(
	ctx context.Context,
	artist *entity.Artist,
	officialSite *entity.OfficialSite,
	from time.Time,
) ([]*entity.ScrapedConcert, *SearchMetadata, error) {
	// Tool setup for grounding: GoogleSearch + URLContext.
	//
	// URLContext-only PoC was insufficient: across 36 cells the model never
	// recursed into tour-detail pages and fell back to hallucinating dates.
	// GoogleSearch lets the model find news / fan-club / detail URLs that
	// URLContext can then deep-read.
	//
	// TimeRangeFilter narrows GoogleSearch to the last 6 months (artist news
	// rarely lives beyond that window). Only takes effect on the Gemini API
	// direct backend (BackendGeminiAPI); Vertex AI silently ignores it.
	//
	// References:
	//   - https://ai.google.dev/gemini-api/docs/url-context
	//   - https://ai.google.dev/gemini-api/docs/google-search
	now := time.Now().UTC().Truncate(time.Second)
	searchTool := &genai.Tool{
		GoogleSearch: &genai.GoogleSearch{
			TimeRangeFilter: &genai.Interval{
				StartTime: now.AddDate(0, -6, 0),
				EndTime:   now,
			},
		},
	}
	urlCtxTool := &genai.Tool{
		URLContext: &genai.URLContext{},
	}

	temperature := s.config.Temperature
	generateCfg := &genai.GenerateContentConfig{
		SystemInstruction: &genai.Content{
			Parts: []*genai.Part{{Text: systemInstruction}},
		},
		Tools:              []*genai.Tool{searchTool, urlCtxTool},
		Temperature:        &temperature,
		MaxOutputTokens:    maxOutputTokens,
		ResponseMIMEType:   "application/json",
		ResponseJsonSchema: responseJSONSchema,
	}

	// Wire ThinkingConfig only when an explicit level is configured;
	// otherwise the SDK/model default applies.
	if level := thinkingLevelFromConfig(s.config.ThinkingLevel); level != genai.ThinkingLevelUnspecified {
		generateCfg.ThinkingConfig = &genai.ThinkingConfig{ThinkingLevel: level}
	}

	var prompt string
	var officialSiteURL string
	if officialSite != nil {
		officialSiteURL = officialSite.URL
		fromStr := from.Format("2006-01-02")
		// 4 placeholders (long-form A/B): artist, site, fromStr, site (STEP 1).
		prompt = fmt.Sprintf(promptTemplateWithSite, artist.Name, officialSiteURL, fromStr, officialSiteURL)
	} else {
		fromStr := from.Format("2006-01-02")
		// 2 placeholders: artist, fromStr.
		prompt = fmt.Sprintf(promptTemplateWithoutSite, artist.Name, fromStr)
	}

	attrs := []slog.Attr{
		slog.String("artistID", artist.ID),
		slog.String("model_version", s.config.ModelName),
		slog.String("artist", artist.Name),
		slog.String("official_site", officialSiteURL),
		slog.String("from", from.Format("2006-01-02")),
	}

	s.logger.Info(ctx, "start calling Gemini API to search concerts", attrs...)

	bo := &backoff.ExponentialBackOff{
		InitialInterval: 1 * time.Second,
		Multiplier:      2.0,
		MaxInterval:     60 * time.Second,
	}

	md := &SearchMetadata{}
	var lastTransientErr error
	var lastPermanentErr error
	results, err := backoff.Retry(ctx, func() ([]*entity.ScrapedConcert, error) {
		md.RetryCount++
		// Create an independent context per attempt so that:
		// 1. Each retry gets a fresh 120s deadline (not constrained by parent RPC timeout).
		// 2. Client cancellation (e.g., page navigation) does not abort the Gemini call.
		// 3. Trace context (trace_id, span_id) is preserved via WithoutCancel.
		reqCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), geminiCallTimeout)
		defer cancel()

		resp, err := s.client.Models.GenerateContent(reqCtx, s.config.ModelName, genai.Text(prompt), generateCfg)
		if err != nil {
			s.logger.Warn(ctx, "gemini model call failed",
				append(attrs, slog.String("error", err.Error()))...)
			if !isRetryable(err) {
				return nil, backoff.Permanent(err)
			}
			return nil, err
		}

		usageMD := resp.UsageMetadata
		if usageMD != nil {
			md.PromptTokens = usageMD.PromptTokenCount
			md.CandidatesTokens = usageMD.CandidatesTokenCount
			md.ThinkingTokens = usageMD.ThoughtsTokenCount
			md.ToolUseTokens = usageMD.ToolUsePromptTokenCount
			md.TotalTokens = usageMD.TotalTokenCount
		}
		respAttrs := []slog.Attr{
			slog.String("response_id", resp.ResponseID),
			slog.Group("usage_metadata",
				slog.Int("prompt", int(usageMD.PromptTokenCount)),
				slog.Int("candidates", int(usageMD.CandidatesTokenCount)),
				slog.Int("thinking", int(usageMD.ThoughtsTokenCount)),
				slog.Int("total", int(usageMD.TotalTokenCount)),
				slog.Int("tool_use", int(usageMD.ToolUsePromptTokenCount)),
			),
		}

		if len(resp.Candidates) == 0 {
			s.logger.Info(ctx, "Gemini returned no concert candidates", append(attrs, respAttrs...)...)
			return nil, nil
		}

		candidate := resp.Candidates[0]
		groundingMD := candidate.GroundingMetadata
		var (
			webSearchQueries   []string
			groundingChunkURLs []string
			renderedPartsAgg   []int32
			supportCount       int
		)
		if groundingMD != nil {
			webSearchQueries = groundingMD.WebSearchQueries
			md.WebSearchQueries = len(webSearchQueries)
			md.WebSearchQueriesList = webSearchQueries
			for _, ch := range groundingMD.GroundingChunks {
				if ch != nil && ch.Web != nil {
					groundingChunkURLs = append(groundingChunkURLs, ch.Web.URI)
				}
			}
			md.GroundingChunkURLs = groundingChunkURLs
			for _, sup := range groundingMD.GroundingSupports {
				if sup == nil {
					continue
				}
				supportCount++
				renderedPartsAgg = append(renderedPartsAgg, sup.RenderedParts...)
			}
			md.RenderedParts = len(renderedPartsAgg)
		}
		md.FinishReason = string(candidate.FinishReason)
		md.FinishMessage = candidate.FinishMessage
		md.AvgLogprobs = candidate.AvgLogprobs

		// URLContext metadata: which pages the model deep-fetched.
		if candidate.URLContextMetadata != nil {
			for _, um := range candidate.URLContextMetadata.URLMetadata {
				if um == nil {
					continue
				}
				md.URLContextRetrieved = append(md.URLContextRetrieved, URLRetrieval{
					URL:    um.RetrievedURL,
					Status: string(um.URLRetrievalStatus),
				})
			}
		}
		candidateAttrs := append(respAttrs,
			slog.String("finish_reason", string(candidate.FinishReason)),
			slog.String("finish_message", candidate.FinishMessage),
			slog.Float64("avg_logprobs", candidate.AvgLogprobs),
			slog.Group("grounding_metadata",
				slog.Any("web_search_queries", webSearchQueries),
				slog.Any("grounding_chunk_urls", groundingChunkURLs),
				slog.Int("grounding_supports", supportCount),
				slog.Any("rendered_parts", renderedPartsAgg),
			),
		)

		// Join all non-thought Text parts. With ThinkingLevel set, the model
		// may interleave thought-summary parts (Thought=true) with the final
		// JSON; the final JSON itself can also be split across multiple
		// non-thought Text parts. Taking parts[0] alone misses both cases.
		parts := candidate.Content.Parts
		var textBuf strings.Builder
		var totalParts, thoughtParts, textParts int
		for _, p := range parts {
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
		md.PartsTotal = totalParts
		md.ThoughtParts = thoughtParts
		md.TextParts = textParts
		joined := textBuf.String()
		if joined == "" {
			s.logger.Debug(ctx, "concert candidate has no text parts",
				append(attrs, append(candidateAttrs,
					slog.Int("parts_total", totalParts),
					slog.Int("thought_parts", thoughtParts),
				)...)...)
			return nil, nil
		}
		md.RawResponseText = joined

		s.logger.Info(ctx, "successfully found concert candidates",
			append(attrs, append(candidateAttrs,
				slog.Int("parts_total", totalParts),
				slog.Int("thought_parts", thoughtParts),
				slog.Int("text_parts", textParts),
			)...)...)

		// FinishReason whitelist: only STOP and "" (streaming in-progress) are considered complete.
		if candidate.FinishReason != genai.FinishReasonStop && candidate.FinishReason != "" {
			lastTransientErr = fmt.Errorf("gemini response not completed normally: finish_reason=%s", candidate.FinishReason)
			s.logger.Warn(ctx, "gemini response not completed normally, retrying",
				append(attrs, candidateAttrs...)...)
			return nil, lastTransientErr
		}

		parsed, err := s.parseEvents(ctx, parts[0].Text, from, md, attrs...)
		if err != nil {
			// parseEvents returns backoff.Permanent for both invalid JSON
			// (truncated output) and structural mismatches — neither is retryable.
			lastPermanentErr = err
			return nil, err
		}
		return parsed, nil
	}, backoff.WithBackOff(bo), backoff.WithMaxTries(3))
	if errors.Is(lastPermanentErr, errInvalidJSON) {
		md.InvalidJSON = true
	}
	if err != nil {
		// Structural mismatch errors are already wrapped by toAppErr in parseEvents — return as-is.
		// Check permanent errors first: they indicate genuine bugs and must not be swallowed
		// by a transient error from a prior retry attempt.
		if lastPermanentErr != nil {
			return nil, md, lastPermanentErr
		}
		// If all retries exhausted for transient issues, log WARN and return nil (graceful degradation).
		if lastTransientErr != nil {
			s.logger.Warn(ctx, "gemini concert search failed after retries, returning empty results",
				append(attrs, slog.String("last_error", lastTransientErr.Error()))...)
			return nil, md, nil
		}
		return nil, md, toAppErr(err, "failed to call Gemini API", attrs...)
	}

	return results, md, nil
}

func (s *ConcertSearcher) parseEvents(
	ctx context.Context,
	rawText string,
	from time.Time,
	md *SearchMetadata,
	attrs ...slog.Attr,
) ([]*entity.ScrapedConcert, error) {
	// Strip markdown code fences if present
	text := strings.TrimSpace(rawText)
	if strings.Contains(text, "```") {
		// Try to find content inside the block
		parts := strings.SplitSeq(text, "```")
		for p := range parts {
			p = strings.TrimSpace(p)
			if after, ok := strings.CutPrefix(p, "json"); ok {
				text = after
				break
			}
			if len(p) > 0 {
				// Fallback to first non-empty part if no json prefix
				text = p
			}
		}
	}
	text = strings.TrimSpace(text)

	// If empty or effectively empty after stripping, return nil
	if text == "" || text == "{}" || text == "{\"tours\":[],\"standalones\":[]}" {
		s.logger.Info(ctx, "Gemini response is effectively empty", append(attrs, slog.String("raw_text", rawText))...)
		return nil, nil
	}

	// Pre-check JSON validity. With structured output mode (ResponseMIMEType +
	// ResponseSchema), invalid JSON indicates output truncation due to maxOutputTokens
	// exhaustion. Retrying the same prompt produces the same truncation, so this is
	// treated as a permanent error.
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

	var eventsResp EventsResponse
	if err := json.Unmarshal([]byte(text), &eventsResp); err != nil {
		// Valid JSON but wrong structure — this is a permanent error (likely a code bug or schema change).
		return nil, backoff.Permanent(toAppErr(err, "failed to unmarshal gemini response",
			append(attrs, slog.String("text", text))...,
		))
	}

	if md != nil {
		md.ToursCount = len(eventsResp.Tours)
		md.StandalonesCount = len(eventsResp.Standalones)
	}

	// Flatten tours + standalones into a single ScrapedConcert list.
	// A tour with N dates produces N concerts sharing the tour title.
	var discovered []*entity.ScrapedConcert
	for _, tour := range eventsResp.Tours {
		for _, ev := range tour.Events {
			c := s.toScrapedConcert(ctx, tour.TourTitle, ev.Venue, ev.AdminArea, ev.LocalDate, ev.StartTime, ev.OpenTime, ev.SourceURL, from, attrs)
			if c != nil {
				discovered = append(discovered, c)
			}
		}
	}
	for _, ev := range eventsResp.Standalones {
		c := s.toScrapedConcert(ctx, ev.EventTitle, ev.Venue, ev.AdminArea, ev.LocalDate, ev.StartTime, ev.OpenTime, ev.SourceURL, from, attrs)
		if c != nil {
			discovered = append(discovered, c)
		}
	}

	s.logger.Info(ctx, "successfully parsed new concerts",
		append(attrs,
			slog.Int("discovered_count", len(discovered)),
			slog.Int("tours_count", len(eventsResp.Tours)),
			slog.Int("standalones_count", len(eventsResp.Standalones)),
			slog.Any("concerts", discovered),
		)...,
	)
	return discovered, nil
}

// toScrapedConcert converts the flat per-date fields (shared between tour
// dates and standalone events) into an entity.ScrapedConcert.
// Returns nil if the event must be skipped (invalid date or past event).
func (s *ConcertSearcher) toScrapedConcert(
	ctx context.Context,
	title, venue, adminAreaRaw, localDate, startTimeRaw, openTimeRaw, sourceURL string,
	from time.Time,
	attrs []slog.Attr,
) *entity.ScrapedConcert {
	date, err := time.Parse("2006-01-02", localDate)
	if err != nil {
		s.logger.Warn(ctx, "failed to parse event date and skip", append(attrs, slog.String("date", localDate))...)
		return nil
	}

	if date.Before(from.Truncate(24 * time.Hour)) {
		s.logger.Debug(ctx, "filtered past event",
			append(attrs, slog.String("title", title), slog.String("date", localDate))...,
		)
		return nil
	}

	var startTime time.Time
	if startTimeRaw != "" && startTimeRaw != "null" {
		if st, err := time.Parse(time.RFC3339, startTimeRaw); err != nil {
			s.logger.Warn(ctx, "failed to parse event start time, using zero",
				append(attrs, slog.String("start_time", startTimeRaw))...,
			)
		} else {
			startTime = st
		}
	}

	var openTime time.Time
	if openTimeRaw != "" && openTimeRaw != "null" {
		if ot, err := time.Parse(time.RFC3339, openTimeRaw); err == nil {
			openTime = ot
		}
	}

	var adminArea *string
	if adminAreaRaw != "" {
		adminArea = geo.NormalizeAdminArea(adminAreaRaw)
	}

	return &entity.ScrapedConcert{
		Title:           title,
		ListedVenueName: venue,
		AdminArea:       adminArea,
		LocalDate:       date,
		StartTime:       startTime,
		OpenTime:        openTime,
		SourceURL:       sourceURL,
	}
}
