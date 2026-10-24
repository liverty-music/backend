package posthog

import (
	"context"
	"log/slog"
	"time"
	"uuid"

	"github.com/pannpers/go-logging/logging"
	"go.opentelemetry.io/otel/trace"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase"
)

// aiProvider is the $ai_provider of every generation: both searchers call the
// Gemini API.
const aiProvider = "google"

// LLMGeneration is one request to an external language model, reported to
// PostHog LLM analytics as a $ai_generation event. Both Gemini searchers fill
// it, so the two searches report usage under the same property names and
// types. The prompt text and the response body are never part of it.
type LLMGeneration struct {
	// Model is the model the request named; ModelVersion is the release the
	// API answered with (empty when no response came back).
	Model        string
	ModelVersion string

	// Request describes what was searched.
	Request LLMRequest

	// Usage is the response's token usage.
	Usage LLMUsage

	// Latency is the wall time of the request.
	Latency time.Duration
	// EndTime is when the request ended, the event's time.
	EndTime time.Time

	// WebSearchQueries are the Google Search queries the model issued.
	WebSearchQueries []string
	// GroundingURLs are the sources the response cited.
	GroundingURLs []string
	// URLContext is each page the model read with URL context and its result.
	URLContext []URLContextResult

	// FinishReason, FinishMessage and BlockReason are the response's stop
	// details; empty when there was no candidate or no response.
	FinishReason  string
	FinishMessage string
	BlockReason   string

	// ErrorKind is a short, bounded name of the failure (e.g. "transient",
	// "invalid_request", "no_candidates", "invalid_json"); empty for a request
	// that succeeded.
	ErrorKind string

	// Concerts is the parse result of a concert search; nil for other
	// searches or a request whose response was not parsed.
	Concerts *ConcertParseResult
	// Sales is the parse result of a published-sale search; nil for other
	// searches or a request whose response was not parsed.
	Sales *SaleParseResult
}

// LLMRequest is what a generation searched for.
type LLMRequest struct {
	ArtistID        string
	OfficialSiteURL string
	// LinkedPageURLs are the official site's pages listed in the prompt.
	LinkedPageURLs []string
	// SearchFrom is the start date of a concert search (YYYY-MM-DD).
	SearchFrom string
	// SeriesIDs are the series of a published-sale search.
	SeriesIDs []string
}

// LLMUsage is the token usage of one response, by kind.
type LLMUsage struct {
	PromptTokens     int
	CandidatesTokens int
	ThinkingTokens   int
	ToolUseTokens    int
	CachedTokens     int
}

// URLContextResult is one page read with URL context.
type URLContextResult struct {
	URL    string `json:"url"`
	Status string `json:"status"`
}

// ConcertParseResult is what a concert search parsed from the response.
type ConcertParseResult struct {
	// DraftCount is the number of events in the response before filters.
	DraftCount int
	// SeriesCount and EventCount are the series and events kept.
	SeriesCount int
	EventCount  int
}

// SaleParseResult is what a published-sale search kept from the response.
type SaleParseResult struct {
	Returned int
	Kept     int
	// Dropped counts the discarded sales per reason.
	Dropped map[string]int
}

// Properties maps the generation to $ai_generation properties: PostHog's
// $ai_* keys for the fields PostHog computes cost and latency from, and
// snake_case custom keys for the rest. traceID and spanID become $ai_trace_id
// and $ai_span_id.
func (g LLMGeneration) Properties(traceID, spanID string) usecase.AnalyticsProperties {
	p := usecase.AnalyticsProperties{
		"$ai_span_id":                 spanID,
		"$ai_model":                   g.Model,
		"$ai_provider":                aiProvider,
		"$ai_input_tokens":            g.Usage.PromptTokens + g.Usage.ToolUseTokens,
		"$ai_output_tokens":           g.Usage.CandidatesTokens + g.Usage.ThinkingTokens,
		"$ai_cache_read_input_tokens": g.Usage.CachedTokens,
		"$ai_latency":                 g.Latency.Seconds(),
		"$ai_is_error":                g.ErrorKind != "",
		"prompt_tokens":               g.Usage.PromptTokens,
		"candidates_tokens":           g.Usage.CandidatesTokens,
		"thinking_tokens":             g.Usage.ThinkingTokens,
		"tool_use_tokens":             g.Usage.ToolUseTokens,
		"web_search_queries":          nonNil(g.WebSearchQueries),
		"grounding_urls":              nonNil(g.GroundingURLs),
		"url_context":                 nonNil(g.URLContext),
	}
	if traceID != "" {
		p["$ai_trace_id"] = traceID
	}
	setIf(p, "model_version", g.ModelVersion)
	setIf(p, "$ai_stop_reason", g.FinishReason)
	setIf(p, "finish_message", g.FinishMessage)
	setIf(p, "block_reason", g.BlockReason)
	setIf(p, "$ai_error", g.ErrorKind)
	setIf(p, "artist_id", g.Request.ArtistID)
	setIf(p, "official_site_url", g.Request.OfficialSiteURL)
	setIf(p, "search_from", g.Request.SearchFrom)
	if g.Request.LinkedPageURLs != nil {
		p["linked_page_urls"] = g.Request.LinkedPageURLs
	}
	if g.Request.SeriesIDs != nil {
		p["series_ids"] = g.Request.SeriesIDs
	}
	if c := g.Concerts; c != nil {
		p["draft_count"] = c.DraftCount
		p["series_count"] = c.SeriesCount
		p["event_count"] = c.EventCount
	}
	if s := g.Sales; s != nil {
		dropped := s.Dropped
		if dropped == nil {
			dropped = map[string]int{}
		}
		p["returned_sales"] = s.Returned
		p["kept_sales"] = s.Kept
		p["dropped_sales"] = dropped
	}
	return p
}

// GenerationReporter reports LLM generations through the analytics client as
// personless $ai_generation events of the process. A nil reporter, or one
// without a client, reports nothing.
type GenerationReporter struct {
	client usecase.AnalyticsClient
	logger *logging.Logger
}

// NewGenerationReporter returns a reporter over client. client may be nil
// (local development without a PostHog key); the reporter then drops every
// generation.
func NewGenerationReporter(client usecase.AnalyticsClient, logger *logging.Logger) *GenerationReporter {
	return &GenerationReporter{client: client, logger: logger}
}

// ReportGeneration sends g under the trace of the span in ctx, with a new
// span id. A failure is logged and never returned: reporting must not change
// what the search returns.
func (r *GenerationReporter) ReportGeneration(ctx context.Context, g LLMGeneration) {
	if r == nil || r.client == nil {
		return
	}
	var traceID string
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		traceID = sc.TraceID().String()
	}
	endTime := g.EndTime
	if endTime.IsZero() {
		endTime = time.Now()
	}
	if err := r.client.Capture(ctx, usecase.AnalyticsEvent{
		Name:       usecase.EventAIGeneration,
		UUID:       entity.NewID(),
		Time:       endTime,
		Personless: true,
		Properties: g.Properties(traceID, uuid.NewV4().String()),
	}); err != nil {
		r.logger.Warn(ctx, "failed to report an LLM generation",
			slog.String("model", g.Model),
			slog.Any("error", err),
		)
	}
}

// setIf sets key to value when value is not empty.
func setIf(p usecase.AnalyticsProperties, key, value string) {
	if value != "" {
		p[key] = value
	}
}

// nonNil returns s, or an empty slice for nil, so a list property is always
// an array in PostHog.
func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
