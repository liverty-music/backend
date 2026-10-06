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

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/pkg/api"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
	"google.golang.org/genai"
)

// SalesPhaseConfig configures the Gemini sales-phase searcher. Like the concert
// searcher it targets the Gemini API direct backend (BackendGeminiAPI) so
// GoogleSearch grounding is available; APIKey is therefore required.
type SalesPhaseConfig struct {
	// APIKey selects the Gemini API direct backend. REQUIRED.
	APIKey string
	// Model is the grounded model that searches and returns JSON. REQUIRED.
	Model string
	// Thinking is the thinking level ("low", "medium", "high"). Empty leaves
	// the model default.
	Thinking string
}

const (
	// salesPhaseSearchWindow limits Google Search to pages from the last 30
	// days: sales are announced shortly before they open.
	salesPhaseSearchWindow = 30 * 24 * time.Hour

	// salesPhaseCallTimeout bounds the single grounded call. A call issues
	// around ten searches and reads several pages.
	salesPhaseCallTimeout = 240 * time.Second
)

// jst is Japan time. JST has no DST, so a fixed zone is exact and avoids a
// tzdata dependency.
var jst = time.FixedZone("JST", 9*60*60)

// systemInstructionSalesPhase is the evaluated system instruction (design D2).
const systemInstructionSalesPhase = `You extract ticket sales for concerts from official web pages, for a service that tells fans about upcoming sales.

Task: for each series in the user prompt, find the ticket sales whose application period has not started at the current time.

A sale is one application period under one method: for example a fan-club lottery, a playguide presale, or a general on-sale. Include lotteries and first-come sales, presales and general on-sales, and every later round of a series (2次, 3次, ...) that has not started yet. Do not include ticket trades or resales between ticket holders.

Sources: the artist's official site, official tour pages, and ticketing-service pages for the series. Do not use any other site.

Return a sale only if a source page states every required field. If any required field is missing, leave the sale out. Never infer or guess a value.

Return JSON that follows the response schema. Return an empty phases array if no sale qualifies.`

// salesPhaseDateTimeDesc is the shared description of the time fields.
const salesPhaseDateTimeDesc = "RFC 3339 date-time with the UTC offset of the time zone the source page uses, " +
	"e.g. 2026-11-01T10:00:00+09:00 for Japan or 2026-11-01T12:00:00+08:00 for a Taipei sale. " +
	"Always include the year; if the page omits it, use the year that places the date before the end of the series' event_period."

// salesPhaseResponseSchema returns the response JSON schema, with series_id
// limited to the given series.
func salesPhaseResponseSchema(seriesIDs []string) map[string]any {
	str := func(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
	nullable := func(desc string) map[string]any {
		return map[string]any{"type": []string{"string", "null"}, "description": desc}
	}
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"phases"},
		"properties": map[string]any{
			"phases": map[string]any{
				"type":        "array",
				"description": "Ticket sales whose apply_start_time is after the current time. Empty when none qualifies.",
				"items": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"required": []string{
						"series_id", "method", "apply_start_time", "apply_end_time", "lottery_result_time",
					},
					"properties": map[string]any{
						"series_id": map[string]any{
							"type": "string", "enum": seriesIDs,
							"description": "ID of the series this sale is for. Leave the sale out if it does not match exactly one series.",
						},
						"method": map[string]any{
							"type": "string", "enum": []string{salesMethodLottery, salesMethodFirstCome},
							"description": "lottery: applicants enter a draw (抽選). first_come: tickets are sold in order until sold out (先着).",
						},
						"apply_start_time": str(salesPhaseDateTimeDesc + " When applications or purchases open."),
						"apply_end_time": nullable(salesPhaseDateTimeDesc + " When applications or purchases close. Required for a lottery. " +
							"null only for a first_come sale that ends when tickets run out and states no end time."),
						"lottery_result_time": nullable(salesPhaseDateTimeDesc +
							" When lottery results are announced. null if not stated or if method is first_come."),
					},
				},
			},
		},
	}
}

const (
	salesMethodLottery   = "lottery"
	salesMethodFirstCome = "first_come"
)

// salesPhaseResponse is the JSON the model returns.
type salesPhaseResponse struct {
	Phases []*salesPhaseJSON `json:"phases"`
}

// salesPhaseJSON is one sale in the model's response.
type salesPhaseJSON struct {
	SeriesID          string  `json:"series_id"`
	Method            string  `json:"method"`
	ApplyStartTime    string  `json:"apply_start_time"`
	ApplyEndTime      *string `json:"apply_end_time"`
	LotteryResultTime *string `json:"lottery_result_time"`
}

// generateContentFunc is the Gemini call, injectable for tests.
type generateContentFunc func(
	ctx context.Context, model string, contents []*genai.Content, cfg *genai.GenerateContentConfig,
) (*genai.GenerateContentResponse, error)

// SalesPhaseSearcher extracts upcoming ticket sales for an artist's series in
// one grounded Gemini call that returns JSON. It implements
// [entity.SalesPhaseSearcher].
type SalesPhaseSearcher struct {
	generate generateContentFunc
	now      func() time.Time
	config   SalesPhaseConfig
	logger   *logging.Logger
}

// Compile-time interface compliance check.
var _ entity.SalesPhaseSearcher = (*SalesPhaseSearcher)(nil)

// NewSalesPhaseSearcher creates a SalesPhaseSearcher targeting the Gemini
// API direct backend. It fast-fails when APIKey or Model is empty.
func NewSalesPhaseSearcher(
	ctx context.Context,
	cfg SalesPhaseConfig,
	httpClient *http.Client,
	logger *logging.Logger,
) (*SalesPhaseSearcher, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("gemini.NewSalesPhaseSearcher: APIKey is empty")
	}
	if cfg.Model == "" {
		return nil, fmt.Errorf("gemini.NewSalesPhaseSearcher: Model is empty")
	}

	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		HTTPClient: httpClient,
		Backend:    genai.BackendGeminiAPI,
		APIKey:     cfg.APIKey,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create genai client for SalesPhaseSearcher: %w", err)
	}

	return &SalesPhaseSearcher{
		generate: client.Models.GenerateContent,
		now:      time.Now,
		config:   cfg,
		logger:   logger,
	}, nil
}

// SearchSalesPhases implements [entity.SalesPhaseSearcher]. It makes one
// grounded call (GoogleSearch limited to the last 30 days, plus URL context)
// that returns JSON, then drops every sale that fails the deterministic checks
// in validateSalesPhase. Any failure of the call fails the search; there is no
// retry, because a failed grounded call is still billed.
func (s *SalesPhaseSearcher) SearchSalesPhases(
	ctx context.Context,
	in *entity.SalesPhaseSearchInput,
) ([]*entity.SalesPhaseCandidate, error) {
	if in == nil || len(in.Series) == 0 {
		return nil, nil
	}
	now := s.now().Truncate(time.Second)
	series := make(map[string]*entity.SalesSeriesRef, len(in.Series))
	seriesIDs := make([]string, 0, len(in.Series))
	for _, sr := range in.Series {
		if sr == nil || sr.SeriesID == "" {
			continue
		}
		if _, dup := series[sr.SeriesID]; dup {
			continue
		}
		series[sr.SeriesID] = sr
		seriesIDs = append(seriesIDs, sr.SeriesID)
	}
	if len(seriesIDs) == 0 {
		return nil, nil
	}

	attrs := []slog.Attr{
		slog.String("artist_name", in.ArtistName),
		slog.String("official_site_url", in.OfficialSiteURL),
		slog.Int("series_count", len(seriesIDs)),
		slog.String("model", s.config.Model),
	}

	cfg := s.buildConfig(now, seriesIDs)
	prompt := salesPhaseUserPrompt(now, in, seriesIDs, series)

	reqCtx, cancel := context.WithTimeout(ctx, salesPhaseCallTimeout)
	defer cancel()
	resp, err := s.generate(reqCtx, s.config.Model, genai.Text(prompt), cfg)
	if err != nil {
		return nil, salesPhaseCallErr(err, attrs...)
	}

	text, err := s.readResponse(ctx, resp, attrs)
	if err != nil {
		return nil, err
	}

	var out salesPhaseResponse
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		return nil, apperr.Wrap(err, codes.Internal, "SalesPhaseSearcher: response is not valid JSON", attrs...)
	}

	candidates := make([]*entity.SalesPhaseCandidate, 0, len(out.Phases))
	for _, p := range out.Phases {
		if p == nil {
			continue
		}
		c, reason := validateSalesPhase(p, now, series)
		if reason != "" {
			s.logger.Info(ctx, "SalesPhaseSearcher: dropping sale",
				append(attrs,
					slog.String("reason", reason),
					slog.String("series_id", p.SeriesID),
					slog.String("method", p.Method),
					slog.String("apply_start_time", p.ApplyStartTime),
				)...)
			continue
		}
		candidates = append(candidates, c)
	}

	s.logger.Info(ctx, "SalesPhaseSearcher: search complete",
		append(attrs,
			slog.Int("returned_count", len(out.Phases)),
			slog.Int("kept_count", len(candidates)),
		)...)
	return candidates, nil
}

// buildConfig builds the request configuration (design D1). Temperature is
// deliberately not sent.
func (s *SalesPhaseSearcher) buildConfig(now time.Time, seriesIDs []string) *genai.GenerateContentConfig {
	include := true
	utcNow := now.UTC()
	cfg := &genai.GenerateContentConfig{
		SystemInstruction: &genai.Content{
			Parts: []*genai.Part{{Text: systemInstructionSalesPhase}},
		},
		Tools: []*genai.Tool{
			{GoogleSearch: &genai.GoogleSearch{
				TimeRangeFilter: &genai.Interval{StartTime: utcNow.Add(-salesPhaseSearchWindow), EndTime: utcNow},
			}},
			{URLContext: &genai.URLContext{}},
		},
		ToolConfig:         &genai.ToolConfig{IncludeServerSideToolInvocations: &include},
		MaxOutputTokens:    maxOutputTokens,
		ResponseMIMEType:   "application/json",
		ResponseJsonSchema: salesPhaseResponseSchema(seriesIDs),
	}
	if level := thinkingLevelFromConfig(s.config.Thinking); level != genai.ThinkingLevelUnspecified {
		cfg.ThinkingConfig = &genai.ThinkingConfig{ThinkingLevel: level}
	}
	return cfg
}

// salesPhaseUserPrompt renders the per-artist prompt: the current time, the
// artist, the official site, and each series with its event period.
func salesPhaseUserPrompt(
	now time.Time,
	in *entity.SalesPhaseSearchInput,
	seriesIDs []string,
	series map[string]*entity.SalesSeriesRef,
) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Current time: %s\nArtist: %s\nOfficial site: %s\n\nSeries:\n",
		now.In(jst).Format(time.RFC3339), in.ArtistName, in.OfficialSiteURL)
	for _, id := range seriesIDs {
		sr := series[id]
		fmt.Fprintf(&b, "- id: %s\n  title: %s\n", sr.SeriesID, sr.Title)
		if first, last, ok := eventPeriod(sr); ok {
			fmt.Fprintf(&b, "  event_period: %s to %s\n", first.Format(time.DateOnly), last.Format(time.DateOnly))
		}
	}
	return b.String()
}

// eventPeriod returns the first and last event dates of a series in Japan time.
func eventPeriod(sr *entity.SalesSeriesRef) (first, last time.Time, ok bool) {
	for _, d := range sr.EventDates {
		if d.IsZero() {
			continue
		}
		local := d.In(jst)
		day := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, jst)
		if !ok || day.Before(first) {
			first = day
		}
		if !ok || day.After(last) {
			last = day
		}
		ok = true
	}
	return first, last, ok
}

// readResponse logs the response metadata and returns the JSON text of the
// first candidate. An empty or cut-off response is an Internal error.
func (s *SalesPhaseSearcher) readResponse(
	ctx context.Context,
	resp *genai.GenerateContentResponse,
	attrs []slog.Attr,
) (string, error) {
	if resp == nil || len(resp.Candidates) == 0 || resp.Candidates[0] == nil || resp.Candidates[0].Content == nil {
		s.logResponseMetadata(ctx, resp, nil, attrs)
		return "", apperr.New(codes.Internal, "SalesPhaseSearcher: response has no candidate", attrs...)
	}
	candidate := resp.Candidates[0]

	var (
		text    strings.Builder
		queries []string
	)
	for _, p := range candidate.Content.Parts {
		if p == nil {
			continue
		}
		if tc := p.ToolCall; tc != nil {
			if tc.ToolType == genai.ToolTypeGoogleSearchWeb {
				queries = append(queries, searchQueriesOf(tc.Args)...)
			}
			continue
		}
		if p.Thought || p.Text == "" {
			continue
		}
		text.WriteString(p.Text)
	}
	if len(queries) == 0 && candidate.GroundingMetadata != nil {
		queries = candidate.GroundingMetadata.WebSearchQueries
	}
	s.logResponseMetadata(ctx, resp, queries, attrs)

	if candidate.FinishReason != genai.FinishReasonStop && candidate.FinishReason != "" {
		return "", apperr.New(codes.Internal, "SalesPhaseSearcher: response did not finish",
			append(attrs, slog.String("finish_reason", string(candidate.FinishReason)))...)
	}
	if strings.TrimSpace(text.String()) == "" {
		return "", apperr.New(codes.Internal, "SalesPhaseSearcher: response has no text", attrs...)
	}
	return text.String(), nil
}

// salesPhaseCallErr maps a failed Gemini call to an apperr code (design D4):
// a context deadline is DeadlineExceeded, a 5xx or a transport error is
// Unavailable, and a 4xx (including the 429 spend cap) maps by status.
func salesPhaseCallErr(err error, attrs ...slog.Attr) error {
	const msg = "SalesPhaseSearcher: Gemini call failed"
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return apperr.Wrap(err, codes.DeadlineExceeded, msg, attrs...)
	case errors.Is(err, context.Canceled):
		return apperr.Wrap(err, codes.Canceled, msg, attrs...)
	}
	if apiErr, ok := errors.AsType[genai.APIError](err); ok {
		if apiErr.Code >= http.StatusInternalServerError {
			return apperr.Wrap(err, codes.Unavailable, msg, attrs...)
		}
		return api.FromStatus(apiErr.Code, err, msg, attrs...)
	}
	return apperr.Wrap(err, codes.Unavailable, msg, attrs...)
}

// validateSalesPhase applies the deterministic checks of design D3 and returns
// the candidate, or the reason the sale is dropped.
func validateSalesPhase(
	p *salesPhaseJSON,
	now time.Time,
	series map[string]*entity.SalesSeriesRef,
) (*entity.SalesPhaseCandidate, string) {
	sr, ok := series[p.SeriesID]
	if !ok {
		return nil, "unknown series_id"
	}

	var method entity.SalesMethod
	switch p.Method {
	case salesMethodLottery:
		method = entity.SalesMethodLottery
	case salesMethodFirstCome:
		method = entity.SalesMethodFirstCome
	default:
		return nil, "unknown method"
	}

	start, err := time.Parse(time.RFC3339, strings.TrimSpace(p.ApplyStartTime))
	if err != nil {
		return nil, "unparseable apply_start_time"
	}
	if !start.After(now) {
		return nil, "already started"
	}

	end, ok := parseOptionalTime(p.ApplyEndTime)
	if !ok {
		return nil, "unparseable apply_end_time"
	}
	result, ok := parseOptionalTime(p.LotteryResultTime)
	if !ok {
		return nil, "unparseable lottery_result_time"
	}

	c := &entity.SalesPhaseCandidate{
		SeriesID:          p.SeriesID,
		Method:            method,
		ApplyStartTime:    start,
		ApplyEndTime:      end,
		LotteryResultTime: result,
	}
	if err := c.Validate(); err != nil {
		return nil, err.Error()
	}

	if _, last, ok := eventPeriod(sr); ok && start.After(last.AddDate(0, 0, 1)) {
		return nil, "apply_start_time after the series' last event"
	}
	return c, ""
}

// parseOptionalTime parses a nullable RFC 3339 field. A null or empty value is
// the zero time; ok is false only for a value that does not parse.
func parseOptionalTime(s *string) (time.Time, bool) {
	if s == nil || strings.TrimSpace(*s) == "" {
		return time.Time{}, true
	}
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(*s))
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// logResponseMetadata logs the metadata of a Gemini response for tuning and
// cost tracking: token usage, finish reason, the search queries the call
// issued (each one is billed), grounding sources and URL-context retrieval.
// One line is emitted per call, including empty responses.
func (s *SalesPhaseSearcher) logResponseMetadata(
	ctx context.Context,
	resp *genai.GenerateContentResponse,
	queries []string,
	attrs []slog.Attr,
) {
	fields := make([]slog.Attr, 0, len(attrs)+8)
	fields = append(fields, attrs...)
	fields = append(fields,
		slog.Int("search_query_count", len(queries)),
		slog.Any("search_queries", queries),
	)
	if resp == nil {
		s.logger.Info(ctx, "SalesPhaseSearcher: gemini response metadata", fields...)
		return
	}
	fields = append(fields,
		slog.String("response_id", resp.ResponseID),
		slog.String("model_version", resp.ModelVersion),
	)
	if u := resp.UsageMetadata; u != nil {
		fields = append(fields, slog.Group("usage",
			slog.Int("prompt", int(u.PromptTokenCount)),
			slog.Int("candidates", int(u.CandidatesTokenCount)),
			slog.Int("thinking", int(u.ThoughtsTokenCount)),
			slog.Int("tool_use", int(u.ToolUsePromptTokenCount)),
			slog.Int("total", int(u.TotalTokenCount)),
		))
	}
	if len(resp.Candidates) == 0 || resp.Candidates[0] == nil {
		fields = append(fields, slog.Bool("has_candidate", false))
		s.logger.Info(ctx, "SalesPhaseSearcher: gemini response metadata", fields...)
		return
	}
	c := resp.Candidates[0]
	fields = append(fields,
		slog.String("finish_reason", string(c.FinishReason)),
		slog.String("finish_message", c.FinishMessage),
	)
	if g := c.GroundingMetadata; g != nil {
		urls := make([]string, 0, len(g.GroundingChunks))
		for _, ch := range g.GroundingChunks {
			if ch != nil && ch.Web != nil {
				urls = append(urls, ch.Web.URI)
			}
		}
		fields = append(fields, slog.Any("source_urls", urls))
	}
	if uc := c.URLContextMetadata; uc != nil {
		retrieved := make([]string, 0, len(uc.URLMetadata))
		for _, um := range uc.URLMetadata {
			if um != nil {
				retrieved = append(retrieved, fmt.Sprintf("%s [%s]", um.RetrievedURL, um.URLRetrievalStatus))
			}
		}
		fields = append(fields, slog.Any("url_context_retrieved", retrieved))
	}
	s.logger.Info(ctx, "SalesPhaseSearcher: gemini response metadata", fields...)
}
