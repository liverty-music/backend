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
	"github.com/liverty-music/backend/internal/infrastructure/analytics/posthog"
	"github.com/liverty-music/backend/pkg/api"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
	"google.golang.org/genai"
)

// TicketSaleSearcherConfig configures the Gemini published-sale searcher. Like the concert
// searcher it targets the Gemini API direct backend (BackendGeminiAPI) so
// GoogleSearch grounding is available; APIKey is therefore required.
type TicketSaleSearcherConfig struct {
	// APIKey selects the Gemini API direct backend. REQUIRED.
	APIKey string
	// Model is the grounded model that searches and returns JSON. REQUIRED.
	Model string
	// Thinking is the thinking level ("low", "medium", "high"). Empty leaves
	// the model default.
	Thinking string

	// Reporter receives one generation per request sent to the model. Nil
	// reports nothing.
	Reporter GenerationReporter
}

const (
	// publishedSalesSearchWindow limits Google Search to pages from the last 30
	// days: sales are announced shortly before they open.
	publishedSalesSearchWindow = 30 * 24 * time.Hour

	// publishedSalesCallTimeout bounds the single grounded call. A call issues
	// around ten searches and reads several pages.
	publishedSalesCallTimeout = 240 * time.Second
)

// jst is Japan time. JST has no DST, so a fixed zone is exact and avoids a
// tzdata dependency.
var jst = time.FixedZone("JST", 9*60*60)

// systemInstructionPublishedSales is the evaluated system instruction (design
// D2 of tune-sales-phase-search). The completion criterion after the task line
// was added with gemini-4-hydrogen, which at thinking low otherwise stopped
// after the first sale of a series it found (OpenSpec change
// move-ticket-sale-search-to-gemini-4-hydrogen, design D4).
const systemInstructionPublishedSales = `You extract ticket sales for concerts from official web pages, for a service that tells fans about upcoming sales.

Task: for each series in the user prompt, find the ticket sales whose application period has not started at the current time.

You are done when every sale that the sources announce for each series and that has not started at the current time is in the answer. One sale found does not mean the others are absent: check the official site, the official tour pages and each ticketing service that sells the series.

A sale is one application period under one method: for example a fan-club lottery, a playguide presale, or a general on-sale. Include lotteries and first-come sales, presales and general on-sales, and every later round of a series (2次, 3次, ...) that has not started yet. Do not include ticket trades or resales between fans.

Sources: the artist's official site, official tour pages, and ticketing-service pages for the series. Do not use any other site.

Return a sale only if a source page states every required field. If any required field is missing, leave the sale out. Never infer or guess a value.

For a lottery, also return result_date when a source page states the day its results are announced. If no page states that day, omit result_date and still return the sale.

Return JSON that follows the response schema. Return an empty phases array if no sale qualifies.`

// publishedSaleDateTimeDesc is the shared description of the time fields.
const publishedSaleDateTimeDesc = "Local date and time as YYYY-MM-DDTHH:MM exactly as the source page states it, " +
	"with no UTC offset and without converting it to another time zone, " +
	"e.g. 2026-11-01T10:00 for a page that says 10:00 in Japan or 2026-11-01T12:00 for a sale a Taipei page opens at 12:00. " +
	"Always include the year; if the page omits it, use the year that places the date before the end of the series' event_period."

// publishedSalesResponseSchema returns the response JSON schema, with series_id
// limited to the given series.
func publishedSalesResponseSchema(seriesIDs []string) map[string]any {
	str := func(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
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
						"series_id", "name", "method", "time_zone", "apply_start_time", "apply_end_time",
					},
					"properties": map[string]any{
						"series_id": map[string]any{
							"type": "string", "enum": seriesIDs,
							"description": "ID of the series this sale is for. Leave the sale out if it does not match exactly one series.",
						},
						"name": str("The sale's name exactly as the source page gives it, e.g. オフィシャル先行 or 一般発売. " +
							"Leave the sale out if the page does not name it."),
						"method": map[string]any{
							"type": "string", "enum": []string{salesMethodLottery, salesMethodFirstCome},
							"description": "lottery: fans enter a draw (抽選). first_come: tickets are sold in order until sold out (先着).",
						},
						"time_zone": str("IANA time zone name of the time the source page states, e.g. Asia/Tokyo for Japan time or " +
							"Asia/Taipei for a Taipei sale, which both apply_start_time and apply_end_time are read in. " +
							"Empty when the page implies none."),
						"apply_start_time": str(publishedSaleDateTimeDesc + " When applications or purchases open."),
						"apply_end_time": str(publishedSaleDateTimeDesc + " When applications or purchases close. " +
							"Leave the sale out if the page states no end time, also for a sale that ends when tickets run out."),
						"result_date": str("Lottery only. YYYY-MM-DD date, in the time zone of the source page, of the first day " +
							"the page says the lottery's results are announced (当落発表, 抽選結果発表, 当選発表, or the start of " +
							"抽選結果確認期間), e.g. 2026-11-03 for 「当落発表：11月3日(月)〜11月5日(水)」 or " +
							"「抽選結果確認/入金期間：11月3日(火・祝)15:00～11月8日(日)23:59」. Omit it when the page states no such day, and for a first_come sale; " +
							"the sale is still returned."),
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

// publishedSalesResponse is the JSON the model returns.
type publishedSalesResponse struct {
	Phases []*publishedSaleJSON `json:"phases"`
}

// publishedSaleJSON is one sale in the model's response.
type publishedSaleJSON struct {
	SeriesID       string `json:"series_id"`
	Name           string `json:"name"`
	Method         string `json:"method"`
	TimeZone       string `json:"time_zone"`
	ApplyStartTime string `json:"apply_start_time"`
	ApplyEndTime   string `json:"apply_end_time"`
	ResultDate     string `json:"result_date"`
}

// generateContentFunc is the Gemini call, injectable for tests.
type generateContentFunc func(
	ctx context.Context, model string, contents []*genai.Content, cfg *genai.GenerateContentConfig,
) (*genai.GenerateContentResponse, error)

// TicketSaleSearcher extracts upcoming ticket sales for an artist's series in
// one grounded Gemini call that returns JSON. It implements
// [entity.TicketSaleSearcher].
type TicketSaleSearcher struct {
	generate generateContentFunc
	now      func() time.Time
	config   TicketSaleSearcherConfig
	logger   *logging.Logger
}

// Compile-time interface compliance check.
var _ entity.TicketSaleSearcher = (*TicketSaleSearcher)(nil)

// NewTicketSaleSearcher creates a TicketSaleSearcher targeting the Gemini
// API direct backend. It fast-fails when APIKey or Model is empty.
func NewTicketSaleSearcher(
	ctx context.Context,
	cfg TicketSaleSearcherConfig,
	httpClient *http.Client,
	logger *logging.Logger,
) (*TicketSaleSearcher, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("gemini.NewTicketSaleSearcher: APIKey is empty")
	}
	if cfg.Model == "" {
		return nil, fmt.Errorf("gemini.NewTicketSaleSearcher: Model is empty")
	}

	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		HTTPClient: httpClient,
		Backend:    genai.BackendGeminiAPI,
		APIKey:     cfg.APIKey,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create genai client for TicketSaleSearcher: %w", err)
	}

	return &TicketSaleSearcher{
		generate: client.Models.GenerateContent,
		now:      time.Now,
		config:   cfg,
		logger:   logger,
	}, nil
}

// callWithFallback sends the request. When it is rejected as invalid, it
// sends it once more without the tool-invocation report, and the returned
// attributes then say so.
func (s *TicketSaleSearcher) callWithFallback(
	ctx context.Context,
	prompt string,
	cfg *genai.GenerateContentConfig,
	base posthog.LLMGeneration,
	gens *[]posthog.LLMGeneration,
	attrs []slog.Attr,
) (*genai.GenerateContentResponse, []slog.Attr, error) {
	resp, err := s.call(ctx, prompt, cfg, base, gens)
	if err != nil && cfg.ToolConfig != nil && isInvalidRequest(err) {
		s.logger.Warn(ctx, "TicketSaleSearcher: request rejected as invalid, retrying once without the tool-invocation report",
			append(attrs, slog.String("error", err.Error()))...)
		reduced := *cfg
		reduced.ToolConfig = nil
		attrs = append(attrs, slog.Bool("tool_report_dropped", true))
		resp, err = s.call(ctx, prompt, &reduced, base, gens)
	}
	return resp, attrs, err
}

// baseGeneration returns the report every request of the search starts from.
func (s *TicketSaleSearcher) baseGeneration(in *entity.PublishedSaleSearchInput, seriesIDs []string) posthog.LLMGeneration {
	return posthog.LLMGeneration{
		Model: s.config.Model,
		Request: posthog.LLMRequest{
			ArtistID:        in.ArtistID,
			OfficialSiteURL: in.OfficialSiteURL,
			SeriesIDs:       seriesIDs,
		},
	}
}

// distinctSeries indexes the series by id, skipping nil ones, ones with no id
// and repeats, and returns the ids in input order.
func distinctSeries(in []*entity.SalesSeriesRef) (map[string]*entity.SalesSeriesRef, []string) {
	series := make(map[string]*entity.SalesSeriesRef, len(in))
	seriesIDs := make([]string, 0, len(in))
	for _, sr := range in {
		if sr == nil || sr.SeriesID == "" {
			continue
		}
		if _, dup := series[sr.SeriesID]; dup {
			continue
		}
		series[sr.SeriesID] = sr
		seriesIDs = append(seriesIDs, sr.SeriesID)
	}
	return series, seriesIDs
}

// keepValidSales validates the phases Gemini returned and returns the sales to
// keep with the counts of what was returned, kept and dropped, logging each
// drop.
func (s *TicketSaleSearcher) keepValidSales(
	ctx context.Context,
	phases []*publishedSaleJSON,
	now time.Time,
	series map[string]*entity.SalesSeriesRef,
	attrs []slog.Attr,
) ([]*entity.DiscoveredTicketSale, *posthog.SaleParseResult) {
	candidates := make([]*entity.DiscoveredTicketSale, 0, len(phases))
	dropped := map[string]int{}
	droppedResultDates := map[string]int{}
	resultDates := 0
	for _, p := range phases {
		if p == nil {
			continue
		}
		c, reason, resultDateReason := validatePublishedSale(p, now, series)
		if reason != "" {
			dropped[reason]++
			s.logger.Info(ctx, "TicketSaleSearcher: dropping sale",
				append(attrs,
					slog.String("reason", reason),
					slog.String("series_id", p.SeriesID),
					slog.String("method", p.Method),
					slog.String("apply_start_time", p.ApplyStartTime),
				)...)
			continue
		}
		if resultDateReason != "" {
			droppedResultDates[resultDateReason]++
			s.logger.Info(ctx, "TicketSaleSearcher: dropping result date",
				append(attrs,
					slog.String("reason", resultDateReason),
					slog.String("series_id", p.SeriesID),
					slog.String("result_date", p.ResultDate),
					slog.String("apply_end_time", p.ApplyEndTime),
				)...)
		}
		if c.ResultDate != nil {
			resultDates++
		}
		candidates = append(candidates, c)
	}
	return candidates, &posthog.SaleParseResult{
		Returned:           len(phases),
		Kept:               len(candidates),
		Dropped:            dropped,
		ResultDates:        resultDates,
		DroppedResultDates: droppedResultDates,
	}
}

// SearchPublishedSales implements [entity.TicketSaleSearcher]. It makes one
// grounded call (GoogleSearch limited to the last 30 days, plus URL context)
// that returns JSON, then drops every sale that fails the deterministic checks
// in validatePublishedSale. Any failure of the call fails the search; there is no
// retry, because a failed grounded call is still billed. The one exception is
// a request rejected as invalid: some pages break the Preview tool-invocation
// report after URL context reads them (backend #542), so the request is sent
// once more without the report.
func (s *TicketSaleSearcher) SearchPublishedSales(
	ctx context.Context,
	in *entity.PublishedSaleSearchInput,
) ([]*entity.DiscoveredTicketSale, error) {
	if in == nil || len(in.Series) == 0 {
		return nil, nil
	}
	now := s.now().Truncate(time.Second)
	series, seriesIDs := distinctSeries(in.Series)
	if len(seriesIDs) == 0 {
		return nil, nil
	}

	attrs := []slog.Attr{
		slog.String("artist_name", in.ArtistName),
		slog.String("official_site_url", in.OfficialSiteURL),
		slog.Int("series_count", len(seriesIDs)),
		slog.String("model", s.config.Model),
	}
	base := s.baseGeneration(in, seriesIDs)
	cfg := s.buildConfig(now, seriesIDs)
	prompt := publishedSalesUserPrompt(now, in, seriesIDs, series)

	// Every request sent below is reported once, after the response is
	// read so the last one carries what was kept.
	var gens []posthog.LLMGeneration
	defer func() { reportAll(ctx, s.config.Reporter, gens) }()
	// fail marks the last request with errorKind.
	fail := func(errorKind string) {
		if len(gens) > 0 {
			gens[len(gens)-1].ErrorKind = errorKind
		}
	}

	resp, attrs, err := s.callWithFallback(ctx, prompt, cfg, base, &gens, attrs)
	if err != nil {
		return nil, publishedSalesCallErr(err, attrs...)
	}

	text, errorKind, err := s.readResponse(ctx, resp, attrs)
	if err != nil {
		fail(errorKind)
		return nil, err
	}

	var out publishedSalesResponse
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		fail(errorKindInvalidJSON)
		return nil, apperr.Wrap(err, codes.Internal, "TicketSaleSearcher: response is not valid JSON", attrs...)
	}

	candidates, result := s.keepValidSales(ctx, out.Phases, now, series, attrs)
	gens[len(gens)-1].Sales = result

	s.logger.Info(ctx, "TicketSaleSearcher: search complete",
		append(attrs,
			slog.Int("returned_count", len(out.Phases)),
			slog.Int("kept_count", len(candidates)),
		)...)
	return candidates, nil
}

// call makes one grounded call within publishedSalesCallTimeout and appends
// it to gens as base filled from the response, with its latency and, for a
// failed call, its error kind.
func (s *TicketSaleSearcher) call(
	ctx context.Context,
	prompt string,
	cfg *genai.GenerateContentConfig,
	base posthog.LLMGeneration,
	gens *[]posthog.LLMGeneration,
) (*genai.GenerateContentResponse, error) {
	reqCtx, cancel := context.WithTimeout(ctx, publishedSalesCallTimeout)
	defer cancel()
	start := time.Now()
	resp, err := s.generate(reqCtx, s.config.Model, genai.Text(prompt), cfg)
	gen := base
	gen.Latency = time.Since(start)
	gen.EndTime = start.Add(gen.Latency)
	fillFromResponse(&gen, resp)
	if err != nil {
		gen.ErrorKind = callErrorKind(reqCtx, err)
	}
	*gens = append(*gens, gen)
	return resp, err
}

// buildConfig builds the request configuration (design D1). Temperature is
// deliberately not sent.
func (s *TicketSaleSearcher) buildConfig(now time.Time, seriesIDs []string) *genai.GenerateContentConfig {
	include := true
	utcNow := now.UTC()
	cfg := &genai.GenerateContentConfig{
		SystemInstruction: &genai.Content{
			Parts: []*genai.Part{{Text: systemInstructionPublishedSales}},
		},
		Tools: []*genai.Tool{
			{GoogleSearch: &genai.GoogleSearch{
				TimeRangeFilter: &genai.Interval{StartTime: utcNow.Add(-publishedSalesSearchWindow), EndTime: utcNow},
			}},
			{URLContext: &genai.URLContext{}},
		},
		ToolConfig:         &genai.ToolConfig{IncludeServerSideToolInvocations: &include},
		MaxOutputTokens:    maxOutputTokens,
		ResponseMIMEType:   "application/json",
		ResponseJsonSchema: publishedSalesResponseSchema(seriesIDs),
	}
	if level := thinkingLevelFromConfig(s.config.Thinking); level != genai.ThinkingLevelUnspecified {
		cfg.ThinkingConfig = &genai.ThinkingConfig{ThinkingLevel: level}
	}
	return cfg
}

// publishedSalesUserPrompt renders the per-artist prompt: the current time, the
// artist, the official site, and each series with its event period.
func publishedSalesUserPrompt(
	now time.Time,
	in *entity.PublishedSaleSearchInput,
	seriesIDs []string,
	series map[string]*entity.SalesSeriesRef,
) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Current time: %s\nArtist: %s\nOfficial site: %s\n\nSeries:\n",
		now.In(jst).Format(time.RFC3339), in.ArtistName, in.OfficialSiteURL)
	for _, id := range seriesIDs {
		sr := series[id]
		fmt.Fprintf(&b, "- id: %s\n  title: %s\n", sr.SeriesID, sr.Title)
		if first, last, ok := sr.EventPeriod(); ok {
			fmt.Fprintf(&b, "  event_period: %s to %s\n", first.Format(time.DateOnly), last.Format(time.DateOnly))
		}
	}
	return b.String()
}

// textAndToolQueries returns the answer text of the parts (thought parts
// left out) and the Google Search queries of their server-side tool calls.
func textAndToolQueries(parts []*genai.Part) (string, []string) {
	var (
		text    strings.Builder
		queries []string
	)
	for _, p := range parts {
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
	return text.String(), queries
}

// readResponse logs the response metadata and returns the JSON text of the
// first candidate. An empty or cut-off response is an Internal error, returned
// with its generation error kind.
func (s *TicketSaleSearcher) readResponse(
	ctx context.Context,
	resp *genai.GenerateContentResponse,
	attrs []slog.Attr,
) (string, string, error) {
	if resp == nil || len(resp.Candidates) == 0 || resp.Candidates[0] == nil || resp.Candidates[0].Content == nil {
		s.logResponseMetadata(ctx, resp, nil, attrs)
		return "", errorKindNoCandidates, apperr.New(codes.Internal, "TicketSaleSearcher: response has no candidate", attrs...)
	}
	candidate := resp.Candidates[0]

	text, queries := textAndToolQueries(candidate.Content.Parts)
	if len(queries) == 0 && candidate.GroundingMetadata != nil {
		queries = candidate.GroundingMetadata.WebSearchQueries
	}
	s.logResponseMetadata(ctx, resp, queries, attrs)

	if candidate.FinishReason != genai.FinishReasonStop && candidate.FinishReason != "" {
		return "", errorKindIncomplete, apperr.New(codes.Internal, "TicketSaleSearcher: response did not finish",
			append(attrs, slog.String("finish_reason", string(candidate.FinishReason)))...)
	}
	if strings.TrimSpace(text) == "" {
		return "", errorKindNoText, apperr.New(codes.Internal, "TicketSaleSearcher: response has no text", attrs...)
	}
	return text, "", nil
}

// publishedSalesCallErr maps a failed Gemini call to an apperr code (design D4):
// a context deadline is DeadlineExceeded, a 5xx or a transport error is
// Unavailable, and a 4xx (including the 429 spend cap) maps by status.
func publishedSalesCallErr(err error, attrs ...slog.Attr) error {
	const msg = "TicketSaleSearcher: Gemini call failed"
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

// validatePublishedSale applies the deterministic checks of design D3 and returns
// the candidate, or the reason the sale is dropped. A result date that cannot
// be kept drops only the date: the candidate is returned without one, with
// the reason in resultDateReason.
func validatePublishedSale(
	p *publishedSaleJSON,
	now time.Time,
	series map[string]*entity.SalesSeriesRef,
) (c *entity.DiscoveredTicketSale, reason, resultDateReason string) {
	sr, ok := series[p.SeriesID]
	if !ok {
		return nil, "unknown series_id", ""
	}

	var method entity.TicketSaleMethod
	switch p.Method {
	case salesMethodLottery:
		method = entity.TicketSaleMethodLottery
	case salesMethodFirstCome:
		method = entity.TicketSaleMethodFirstCome
	default:
		return nil, "unknown method", ""
	}

	// The page's wall-clock times are read in the zone the page implies, else
	// the zone the series' venues share; with neither the sale is dropped.
	zone := strings.TrimSpace(p.TimeZone)
	if zone == "" {
		zone = sr.TimeZone
	}
	loc, err := entity.LoadZone(zone)
	if err != nil {
		return nil, "no time zone", ""
	}
	start, reason := publishedSaleTime(p.ApplyStartTime, loc)
	if reason != "" {
		return nil, reason + " apply_start_time", ""
	}
	var end time.Time
	if strings.TrimSpace(p.ApplyEndTime) != "" {
		if end, reason = publishedSaleTime(p.ApplyEndTime, loc); reason != "" {
			return nil, reason + " apply_end_time", ""
		}
	}

	c = &entity.DiscoveredTicketSale{
		SeriesID:      p.SeriesID,
		Name:          strings.TrimSpace(p.Name),
		Method:        method,
		TimeZone:      zone,
		SaleStartTime: start,
		SaleEndTime:   end,
	}
	if reason := c.FoundReason(now, sr); reason != "" {
		return nil, reason, ""
	}
	c.ResultDate, resultDateReason = publishedResultDate(p.ResultDate, method, end, loc)
	return c, "", resultDateReason
}

// publishedSaleTime reads a sale's wall-clock time (YYYY-MM-DDTHH:MM, seconds
// cut off) in loc and returns its instant. The reason is "unparseable" or
// "nonexistent" (the clocks skip the time in loc) when it cannot.
func publishedSaleTime(raw string, loc *time.Location) (instant time.Time, reason string) {
	raw = strings.TrimSpace(raw)
	civil, err := time.Parse("2006-01-02T15:04", raw)
	if err != nil {
		civil, err = time.Parse("2006-01-02T15:04:05", raw)
		if err != nil {
			return time.Time{}, "unparseable"
		}
	}
	civil = civil.Truncate(time.Minute)
	instant, err = entity.LocalDateTimeInstant(civil, loc)
	if err != nil {
		return time.Time{}, "nonexistent"
	}
	return instant, ""
}

// publishedResultDate parses a sale's result_date. It returns nil and no
// reason when the page states none, and nil with the reason when the date is
// stated but cannot be kept: on a first-come sale, unparsable, or before the
// day of the end time in the sale's zone.
func publishedResultDate(raw string, method entity.TicketSaleMethod, end time.Time, loc *time.Location) (*time.Time, string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, ""
	}
	if method != entity.TicketSaleMethodLottery {
		return nil, "result_date on a first-come sale"
	}
	d, err := time.Parse(time.DateOnly, raw)
	if err != nil {
		return nil, "unparseable result_date"
	}
	if !entity.IsResultDateOnOrAfterEnd(d, end, loc) {
		return nil, "result_date before the end date"
	}
	return &d, ""
}

// candidateFields returns the log attributes that describe a response's first
// candidate: its finish reason, URL-context counts, source URLs and retrieved
// URLs.
func candidateFields(c *genai.Candidate) []slog.Attr {
	fields := []slog.Attr{
		slog.String("finish_reason", string(c.FinishReason)),
		slog.String("finish_message", c.FinishMessage),
	}
	if c.Content != nil {
		// url_context_retrieved can omit fetches that only failed; these
		// count from the server-side tool parts.
		calls, succeeded := urlContextCounts(c.Content.Parts)
		fields = append(fields,
			slog.Int("url_context_calls", calls),
			slog.Int("url_context_succeeded", succeeded),
		)
	}
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
	return fields
}

// logResponseMetadata logs the metadata of a Gemini response for tuning and
// cost tracking: token usage, finish reason, the search queries the call
// issued (each one is billed), grounding sources and URL-context retrieval.
// One line is emitted per call, including empty responses.
func (s *TicketSaleSearcher) logResponseMetadata(
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
		s.logger.Info(ctx, "TicketSaleSearcher: gemini response metadata", fields...)
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
		s.logger.Info(ctx, "TicketSaleSearcher: gemini response metadata", fields...)
		return
	}
	fields = append(fields, candidateFields(resp.Candidates[0])...)
	s.logger.Info(ctx, "TicketSaleSearcher: gemini response metadata", fields...)
}
