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
}

const (
	systemInstruction = `You are a specialized agent for extracting concert information from search results.
	Your goal is to extract structured event data with high precision.
	You must VALIDATE that every extracted event is a musical concert performance by the specified artist.
	Answer purely based on the provided Search Tool results. DO NOT use internal knowledge.`

	// promptTemplateWithSite is used when the artist's official site URL is known.
	promptTemplateWithSite = `
You are an agent extracting concert information for the artist "%s".
Focus on information related to the official site (%s) and the provided search results.
Find ALL future concert events (tour dates, festival appearances, etc.) happening on or after today (%s).

Constraints:
1. Itemize specific performances. Do NOT output a single summary item for a tour; instead, output individual items for each performance date and venue.
2. Do NOT infer dates or times if they are not explicitly stated.
3. Infer the time zone of the event based on the venue location or context of the website.
4. Extract ALL discovered events. De-duplication will be handled downstream.
5. Extract the venue's administrative area (prefecture/state/province) into "admin_area". Populate ONLY when explicitly stated in the text or unambiguously inferable from the venue name (e.g., "Zepp Nagoya" → "愛知県"). Return empty string "" if uncertain. A wrong value is worse than an empty value.
6. If no information is found conformant to the schema, return an empty list.
7. Return the response in JSON format matching the schema: { "events": [ { "artist_name": "string", "event_name": "string", "venue": "string", "admin_area": "string (optional)", "local_date": "YYYY-MM-DD", "start_time": "ISO8601 (e.g. 2026-02-14T18:30:00+09:00)", "open_time": "ISO8601", "source_url": "string" } ] }
`

	// promptTemplateWithoutSite is used when no official site URL is available.
	// The model is instructed to find the official site itself via Google Search grounding.
	promptTemplateWithoutSite = `
You are an agent extracting concert information for the artist "%s".
Search the official website of "%s" and related sources to find concert information.
Find ALL future concert events (tour dates, festival appearances, etc.) happening on or after today (%s).

Constraints:
1. Itemize specific performances. Do NOT output a single summary item for a tour; instead, output individual items for each performance date and venue.
2. Do NOT infer dates or times if they are not explicitly stated.
3. Infer the time zone of the event based on the venue location or context of the website.
4. Extract ALL discovered events. De-duplication will be handled downstream.
5. Extract the venue's administrative area (prefecture/state/province) into "admin_area". Populate ONLY when explicitly stated in the text or unambiguously inferable from the venue name (e.g., "Zepp Nagoya" → "愛知県"). Return empty string "" if uncertain. A wrong value is worse than an empty value.
6. If no information is found conformant to the schema, return an empty list.
7. Return the response in JSON format matching the schema: { "events": [ { "artist_name": "string", "event_name": "string", "venue": "string", "admin_area": "string (optional)", "local_date": "YYYY-MM-DD", "start_time": "ISO8601 (e.g. 2026-02-14T18:30:00+09:00)", "open_time": "ISO8601", "source_url": "string" } ] }
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

var (
	eventSchema = &genai.Schema{
		Type: genai.TypeObject,
		Properties: map[string]*genai.Schema{
			"artist_name": {Type: genai.TypeString},
			"event_name":  {Type: genai.TypeString, Description: "The exact title of the tour or event."},
			"venue":       {Type: genai.TypeString},
			"admin_area": {
				Type:        genai.TypeString,
				Description: "The administrative area (prefecture, state, province) where the venue is located. Populate only when explicitly stated or unambiguously inferable from the venue name or surrounding context. Return empty string if uncertain. Wrong values are strictly forbidden.",
			},
			"local_date": {Type: genai.TypeString, Description: "The date of the concert in YYYY-MM-DD format (local time)."},
			"start_time": {Type: genai.TypeString, Description: "The start time in ISO 8601 format including time zone (e.g. 2026-02-14T18:30:00+09:00). If time zone is unambiguous from context, apply it. Return empty string if unknown."},
			"open_time":  {Type: genai.TypeString, Description: "The door opening time in ISO 8601 format including time zone. Return empty string if unknown."},
			"source_url": {
				Type:        genai.TypeString,
				Description: "The specific URL where this event information was found. Prefer direct links to event details.",
			},
		},
		Required: []string{"artist_name", "event_name", "venue", "local_date", "source_url"},
	}
)

// ScrapedEvent matches the schema defined for Gemini output.
type ScrapedEvent struct {
	ArtistName string  `json:"artist_name"`
	EventName  string  `json:"event_name"`
	Venue      string  `json:"venue"`
	AdminArea  *string `json:"admin_area"`
	LocalDate  string  `json:"local_date"`
	StartTime  *string `json:"start_time"`
	OpenTime   *string `json:"open_time"`
	SourceURL  string  `json:"source_url"`
}

// EventsResponse matches the JSON output from Gemini.
type EventsResponse struct {
	Events []ScrapedEvent `json:"events"`
}

// ConcertSearcher implements entity.ConcertSearcher using Vertex AI Gemini.
// It leverages Gemini's reasoning capabilities combined with Google Search Grounding
// to discover and extract structured concert information from the web.
type ConcertSearcher struct {
	client *genai.Client
	config Config
	logger *logging.Logger
}

// NewConcertSearcher creates a new ConcertSearcher.
// The httpClient parameter allows for custom transport configuration, which is
// particularly useful for unit testing with httptest. If nil, the default transport is used.
func NewConcertSearcher(ctx context.Context, cfg Config, httpClient *http.Client, logger *logging.Logger) (*ConcertSearcher, error) {
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		Project:    cfg.ProjectID,
		Location:   cfg.Location,
		Backend:    genai.BackendVertexAI,
		HTTPClient: httpClient,
	})
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
	responseSchema := &genai.Schema{
		Type: genai.TypeObject,
		Properties: map[string]*genai.Schema{
			"events": {
				Type:  genai.TypeArray,
				Items: eventSchema,
			},
		},
		Required: []string{"events"},
	}

	// Tool setup for grounding
	tool := &genai.Tool{
		// NOTE: disabled because querying data store is not worked for some reasons.
		// TODO: enable it when it is worked.
		//
		// Retrieval: &genai.Retrieval{
		// 	VertexAISearch: &genai.VertexAISearch{
		// 		Datastore: s.config.DataStoreID,
		// 	},
		// },
		GoogleSearch: &genai.GoogleSearch{},
	}

	generateCfg := &genai.GenerateContentConfig{
		SystemInstruction: &genai.Content{
			Parts: []*genai.Part{{Text: systemInstruction}},
		},
		Tools:            []*genai.Tool{tool},
		Temperature:      new(float32(1.0)),
		MaxOutputTokens:  maxOutputTokens,
		ResponseMIMEType: "application/json",
		ResponseSchema:   responseSchema,
	}

	var prompt string
	var officialSiteURL string
	if officialSite != nil {
		officialSiteURL = officialSite.URL
		prompt = fmt.Sprintf(promptTemplateWithSite, artist.Name, officialSiteURL, from.Format("2006-01-02"))
	} else {
		prompt = fmt.Sprintf(promptTemplateWithoutSite, artist.Name, artist.Name, from.Format("2006-01-02"))
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

	var lastTransientErr error
	var lastPermanentErr error
	results, err := backoff.Retry(ctx, func() ([]*entity.ScrapedConcert, error) {
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
		respAttrs := []slog.Attr{
			slog.String("response_id", resp.ResponseID),
			slog.Group("usage_metadata",
				slog.Int("prompt", int(usageMD.PromptTokenCount)),
				slog.Int("candidates", int(usageMD.CandidatesTokenCount)),
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
		var webSearchQueries []string
		if groundingMD != nil {
			webSearchQueries = groundingMD.WebSearchQueries
		}
		candidateAttrs := append(respAttrs,
			slog.String("finish_reason", string(candidate.FinishReason)),
			slog.Group("grounding_metadata",
				slog.Any("web_search_queries", webSearchQueries),
			),
		)

		parts := candidate.Content.Parts
		if len(parts) == 0 || parts[0].Text == "" {
			s.logger.Debug(ctx, "concert candidate has no parts", append(attrs, candidateAttrs...)...)
			return nil, nil
		}

		s.logger.Info(ctx, "successfully found concert candidates", append(attrs, candidateAttrs...)...)

		// FinishReason whitelist: only STOP and "" (streaming in-progress) are considered complete.
		if candidate.FinishReason != genai.FinishReasonStop && candidate.FinishReason != "" {
			lastTransientErr = fmt.Errorf("gemini response not completed normally: finish_reason=%s", candidate.FinishReason)
			s.logger.Warn(ctx, "gemini response not completed normally, retrying",
				append(attrs, candidateAttrs...)...)
			return nil, lastTransientErr
		}

		parsed, err := s.parseEvents(ctx, parts[0].Text, from, attrs...)
		if err != nil {
			// parseEvents returns backoff.Permanent for both invalid JSON
			// (truncated output) and structural mismatches — neither is retryable.
			lastPermanentErr = err
			return nil, err
		}
		return parsed, nil
	}, backoff.WithBackOff(bo), backoff.WithMaxTries(3))
	if err != nil {
		// Structural mismatch errors are already wrapped by toAppErr in parseEvents — return as-is.
		// Check permanent errors first: they indicate genuine bugs and must not be swallowed
		// by a transient error from a prior retry attempt.
		if lastPermanentErr != nil {
			return nil, lastPermanentErr
		}
		// If all retries exhausted for transient issues, log WARN and return nil (graceful degradation).
		if lastTransientErr != nil {
			s.logger.Warn(ctx, "gemini concert search failed after retries, returning empty results",
				append(attrs, slog.String("last_error", lastTransientErr.Error()))...)
			return nil, nil
		}
		return nil, toAppErr(err, "failed to call Gemini API", attrs...)
	}

	return results, nil
}

func (s *ConcertSearcher) parseEvents(
	ctx context.Context,
	rawText string,
	from time.Time,
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
	if text == "" || text == "{}" || text == "{\"events\":[]}" {
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

	// Convert to DTOs
	var discovered []*entity.ScrapedConcert
	for _, ev := range eventsResp.Events {
		date, err := time.Parse("2006-01-02", ev.LocalDate)
		if err != nil {
			s.logger.Warn(ctx, "failed to parse event date and skip", append(attrs, slog.String("date", ev.LocalDate))...)
			continue
		}

		if date.Before(from.Truncate(24 * time.Hour)) {
			s.logger.Debug(ctx, "filtered past event",
				append(attrs, slog.String("title", ev.EventName), slog.String("date", ev.LocalDate))...,
			)
			continue
		}

		var startTime time.Time
		if ev.StartTime != nil && *ev.StartTime != "" && *ev.StartTime != "null" {
			st, err := time.Parse(time.RFC3339, *ev.StartTime)
			if err != nil {
				startTimeStr := *ev.StartTime
				s.logger.Warn(ctx, "failed to parse event start time, using zero",
					append(attrs, slog.String("start_time", startTimeStr))...,
				)
			} else {
				startTime = st
			}
		}

		var openTime time.Time
		if ev.OpenTime != nil && *ev.OpenTime != "" && *ev.OpenTime != "null" {
			if ot, err := time.Parse(time.RFC3339, *ev.OpenTime); err == nil {
				openTime = ot
			}
		}

		var adminArea *string
		if ev.AdminArea != nil && *ev.AdminArea != "" {
			adminArea = geo.NormalizeAdminArea(*ev.AdminArea)
		}

		discovered = append(discovered, &entity.ScrapedConcert{
			Title:           ev.EventName,
			ListedVenueName: ev.Venue,
			AdminArea:       adminArea,
			LocalDate:       date,
			StartTime:       startTime,
			OpenTime:        openTime,
			SourceURL:       ev.SourceURL,
		})
	}

	s.logger.Info(ctx, "successfully parsed new concerts",
		append(attrs, slog.Int("discovered_count", len(discovered)),
			slog.Any("concerts", discovered),
		)...,
	)
	return discovered, nil
}
