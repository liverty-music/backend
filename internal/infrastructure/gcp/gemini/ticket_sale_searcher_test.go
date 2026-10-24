package gemini

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/analytics/posthog"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genai"
)

const (
	spTourA = "01a05c6a-9e24-701e-88c6-158d7639b711"
	spTourB = "01a06ba9-f4c6-7e6e-ba1f-28cac928dcde"
)

// spNow is the frozen current time of these tests: 2026-10-05 15:00 JST.
var spNow = time.Date(2026, 10, 5, 15, 0, 0, 0, jst)

// spInput has two series: tour A runs from February to March 2027, tour B is
// a single show on 20 December 2026.
func spInput() *entity.PublishedSaleSearchInput {
	return &entity.PublishedSaleSearchInput{
		ArtistName:      "King Gnu",
		OfficialSiteURL: "https://kinggnu.jp/",
		Series: []*entity.SalesSeriesRef{
			{
				SeriesID: spTourA,
				Title:    "KICKOFF",
				EventDates: []time.Time{
					time.Date(2027, 3, 14, 0, 0, 0, 0, jst),
					time.Date(2027, 2, 17, 0, 0, 0, 0, jst),
				},
			},
			{
				SeriesID:   spTourB,
				Title:      "星降る晩餐会",
				EventDates: []time.Time{time.Date(2026, 12, 20, 0, 0, 0, 0, jst)},
			},
		},
	}
}

// fakeGemini records the request and answers with a canned response, or with
// respond when it is set.
type fakeGemini struct {
	resp    *genai.GenerateContentResponse
	err     error
	respond func(call int) (*genai.GenerateContentResponse, error)
	calls   int
	model   string
	text    string
	cfg     *genai.GenerateContentConfig
	cfgs    []*genai.GenerateContentConfig
}

func (f *fakeGemini) generate(
	_ context.Context, model string, contents []*genai.Content, cfg *genai.GenerateContentConfig,
) (*genai.GenerateContentResponse, error) {
	f.calls++
	f.model = model
	f.cfg = cfg
	f.cfgs = append(f.cfgs, cfg)
	f.text = ""
	for _, c := range contents {
		for _, p := range c.Parts {
			f.text += p.Text
		}
	}
	if f.respond != nil {
		return f.respond(f.calls)
	}
	return f.resp, f.err
}

func newTestTicketSaleSearcher(t *testing.T, f *fakeGemini, logs *bytes.Buffer) *TicketSaleSearcher {
	t.Helper()
	opts := []logging.Option{logging.WithFormat(logging.FormatJSON)}
	if logs != nil {
		opts = append(opts, logging.WithWriter(logs))
	}
	logger, err := logging.New(opts...)
	require.NoError(t, err)
	return &TicketSaleSearcher{
		generate: f.generate,
		now:      func() time.Time { return spNow },
		config:   TicketSaleSearcherConfig{APIKey: "k", Model: "gemini-3.8-flash", Thinking: "low"},
		logger:   logger,
	}
}

// jsonResponse wraps phases as a finished model response.
func jsonResponse(t *testing.T, phases ...map[string]any) *genai.GenerateContentResponse {
	t.Helper()
	if phases == nil {
		phases = []map[string]any{}
	}
	b, err := json.Marshal(map[string]any{"phases": phases})
	require.NoError(t, err)
	return textResponse(string(b), genai.FinishReasonStop)
}

func textResponse(text string, finish genai.FinishReason, extra ...*genai.Part) *genai.GenerateContentResponse {
	parts := append(extra, &genai.Part{Text: text})
	return &genai.GenerateContentResponse{
		Candidates: []*genai.Candidate{{
			Content:      &genai.Content{Parts: parts},
			FinishReason: finish,
		}},
	}
}

func phase(series, method, start string, end, result any) map[string]any {
	return map[string]any{
		"series_id":           series,
		"method":              method,
		"apply_start_time":    start,
		"apply_end_time":      end,
		"lottery_result_time": result,
	}
}

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	tm, err := time.Parse(time.RFC3339, s)
	require.NoError(t, err)
	return tm
}

func TestTicketSaleSearcher_BuildsRequest(t *testing.T) {
	t.Parallel()

	f := &fakeGemini{resp: jsonResponse(t)}
	s := newTestTicketSaleSearcher(t, f, nil)

	_, err := s.SearchPublishedSales(context.Background(), spInput())
	require.NoError(t, err)
	require.Equal(t, 1, f.calls, "one call per search")
	assert.Equal(t, "gemini-3.8-flash", f.model)

	cfg := f.cfg
	require.Len(t, cfg.Tools, 2)
	search := cfg.Tools[0].GoogleSearch
	require.NotNil(t, search)
	require.NotNil(t, search.TimeRangeFilter)
	assert.Equal(t, spNow.UTC(), search.TimeRangeFilter.EndTime)
	assert.Equal(t, spNow.UTC().Add(-30*24*time.Hour), search.TimeRangeFilter.StartTime)
	assert.NotNil(t, cfg.Tools[1].URLContext)

	require.NotNil(t, cfg.ToolConfig)
	require.NotNil(t, cfg.ToolConfig.IncludeServerSideToolInvocations)
	assert.True(t, *cfg.ToolConfig.IncludeServerSideToolInvocations)

	assert.Equal(t, "application/json", cfg.ResponseMIMEType)
	assert.Nil(t, cfg.Temperature, "temperature is not sent")
	require.NotNil(t, cfg.ThinkingConfig)
	assert.Equal(t, genai.ThinkingLevelLow, cfg.ThinkingConfig.ThinkingLevel)
	assert.Equal(t, systemInstructionPublishedSales, cfg.SystemInstruction.Parts[0].Text)

	schema, ok := cfg.ResponseJsonSchema.(map[string]any)
	require.True(t, ok)
	items := schema["properties"].(map[string]any)["phases"].(map[string]any)["items"].(map[string]any)
	assert.ElementsMatch(t,
		[]string{"series_id", "method", "apply_start_time", "apply_end_time", "lottery_result_time"},
		items["required"])
	seriesProp := items["properties"].(map[string]any)["series_id"].(map[string]any)
	assert.Equal(t, []string{spTourA, spTourB}, seriesProp["enum"])

	assert.Equal(t, "Current time: 2026-10-05T15:00:00+09:00\n"+
		"Artist: King Gnu\n"+
		"Official site: https://kinggnu.jp/\n\n"+
		"Series:\n"+
		"- id: "+spTourA+"\n  title: KICKOFF\n  event_period: 2027-02-17 to 2027-03-14\n"+
		"- id: "+spTourB+"\n  title: 星降る晩餐会\n  event_period: 2026-12-20 to 2026-12-20\n",
		f.text)
}

func TestTicketSaleSearcher_Checks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		phases []map[string]any
		want   []*entity.DiscoveredTicketSale
	}{
		{
			// @spec components/entity/ticket-sale/search-published-sales "Two rounds announced ahead"
			// @spec components/entity/ticket-sale/search-published-sales "Artist with two tours"
			// The input names both tours; the sales are attributed to tour A only.
			name: "Two rounds announced ahead",
			phases: []map[string]any{
				phase(spTourA, "lottery", "2026-10-06T18:00:00+09:00", "2026-10-22T23:59:00+09:00", nil),
				phase(spTourA, "lottery", "2026-11-10T18:00:00+09:00", "2026-11-19T23:59:00+09:00", nil),
			},
			want: []*entity.DiscoveredTicketSale{
				{SeriesID: spTourA, Method: entity.TicketSaleMethodLottery,
					SaleStartTime: time.Date(2026, 10, 6, 18, 0, 0, 0, jst), SaleEndTime: time.Date(2026, 10, 22, 23, 59, 0, 0, jst)},
				{SeriesID: spTourA, Method: entity.TicketSaleMethodLottery,
					SaleStartTime: time.Date(2026, 11, 10, 18, 0, 0, 0, jst), SaleEndTime: time.Date(2026, 11, 19, 23, 59, 0, 0, jst)},
			},
		},
		{
			// @spec components/entity/ticket-sale/search-published-sales "Sale already open"
			name: "Sale already open",
			phases: []map[string]any{
				phase(spTourA, "lottery", "2026-10-04T18:00:00+09:00", "2026-10-12T23:59:00+09:00", nil),
			},
		},
		{
			// @spec components/entity/ticket-sale/search-published-sales "Sale abroad"
			name: "Sale abroad",
			phases: []map[string]any{
				phase(spTourB, "first_come", "2026-11-01T12:00:00+08:00", nil, nil),
			},
			want: []*entity.DiscoveredTicketSale{
				{SeriesID: spTourB, Method: entity.TicketSaleMethodFirstCome, SaleStartTime: time.Date(2026, 11, 1, 13, 0, 0, 0, jst)},
			},
		},
		{
			// @spec components/entity/ticket-sale/search-published-sales "Year omitted"
			name: "Year omitted",
			phases: []map[string]any{
				phase(spTourA, "lottery", "2026-11-10T18:00:00+09:00", "2026-11-19T23:59:00+09:00", nil),
			},
			want: []*entity.DiscoveredTicketSale{
				{SeriesID: spTourA, Method: entity.TicketSaleMethodLottery,
					SaleStartTime: time.Date(2026, 11, 10, 18, 0, 0, 0, jst), SaleEndTime: time.Date(2026, 11, 19, 23, 59, 0, 0, jst)},
			},
		},
		{
			name: "year mistaken into the past is dropped",
			phases: []map[string]any{
				phase(spTourA, "lottery", "2025-11-10T18:00:00+09:00", "2025-11-19T23:59:00+09:00", nil),
			},
		},
		{
			// @spec components/entity/ticket-sale/search-published-sales "Lottery with a published result date"
			name: "Lottery with a published result date",
			phases: []map[string]any{
				phase(spTourA, "lottery", "2026-10-06T18:00:00+09:00", "2026-10-22T23:59:00+09:00", "2026-11-03T15:00:00+09:00"),
			},
			want: []*entity.DiscoveredTicketSale{
				{SeriesID: spTourA, Method: entity.TicketSaleMethodLottery,
					SaleStartTime:     time.Date(2026, 10, 6, 18, 0, 0, 0, jst),
					SaleEndTime:       time.Date(2026, 10, 22, 23, 59, 0, 0, jst),
					LotteryResultTime: time.Date(2026, 11, 3, 15, 0, 0, 0, jst)},
			},
		},
		{
			// @spec components/entity/ticket-sale/search-published-sales "Lottery without a published result date"
			name: "Lottery without a published result date",
			phases: []map[string]any{
				phase(spTourA, "lottery", "2026-10-06T18:00:00+09:00", "2026-10-22T23:59:00+09:00", nil),
			},
			want: []*entity.DiscoveredTicketSale{
				{SeriesID: spTourA, Method: entity.TicketSaleMethodLottery,
					SaleStartTime: time.Date(2026, 10, 6, 18, 0, 0, 0, jst), SaleEndTime: time.Date(2026, 10, 22, 23, 59, 0, 0, jst)},
			},
		},
		{
			// @spec components/entity/ticket-sale/search-published-sales "First-come sale until sold out"
			name: "First-come sale until sold out",
			phases: []map[string]any{
				phase(spTourA, "first_come", "2026-10-06T18:30:00+09:00", nil, nil),
			},
			want: []*entity.DiscoveredTicketSale{
				{SeriesID: spTourA, Method: entity.TicketSaleMethodFirstCome, SaleStartTime: time.Date(2026, 10, 6, 18, 30, 0, 0, jst)},
			},
		},
		{
			// @spec components/entity/ticket-sale/search-published-sales "Lottery without a stated close"
			name: "Lottery without a stated close",
			phases: []map[string]any{
				phase(spTourA, "lottery", "2026-10-06T18:00:00+09:00", nil, nil),
			},
		},
		{
			// @spec components/entity/ticket-sale/search-published-sales "Method not stated"
			name: "Method not stated",
			phases: []map[string]any{
				phase(spTourA, "", "2026-10-06T18:00:00+09:00", "2026-10-22T23:59:00+09:00", nil),
			},
		},
		{
			// @spec components/entity/ticket-sale/search-published-sales "Nothing usable found"
			name: "Nothing usable found",
		},
		{
			// @spec components/entity/ticket-sale/search-published-sales "Close before open"
			name: "Close before open",
			phases: []map[string]any{
				phase(spTourA, "lottery", "2026-11-10T10:00:00+09:00", "2026-11-05T10:00:00+09:00", nil),
			},
		},
		{
			name: "result before close is dropped",
			phases: []map[string]any{
				phase(spTourA, "lottery", "2026-10-06T18:00:00+09:00", "2026-10-22T23:59:00+09:00", "2026-10-20T15:00:00+09:00"),
			},
		},
		{
			name: "result on a first-come sale is dropped",
			phases: []map[string]any{
				phase(spTourA, "first_come", "2026-10-06T18:00:00+09:00", nil, "2026-10-20T15:00:00+09:00"),
			},
		},
		{
			// @spec components/entity/ticket-sale/search-published-sales "Opening after the last show"
			name: "Opening after the last show",
			phases: []map[string]any{
				phase(spTourB, "first_come", "2027-01-10T10:00:00+09:00", nil, nil),
			},
		},
		{
			name: "opening on the last show day is kept",
			phases: []map[string]any{
				phase(spTourB, "first_come", "2026-12-20T10:00:00+09:00", nil, nil),
			},
			want: []*entity.DiscoveredTicketSale{
				{SeriesID: spTourB, Method: entity.TicketSaleMethodFirstCome, SaleStartTime: time.Date(2026, 12, 20, 10, 0, 0, 0, jst)},
			},
		},
		{
			// @spec components/entity/ticket-sale/search-published-sales "Unattributable sale"
			name: "Unattributable sale",
			phases: []map[string]any{
				phase("01a06ba9-ef6a-7d7d-81de-d1b11b945026", "lottery", "2026-10-06T18:00:00+09:00", "2026-10-22T23:59:00+09:00", nil),
			},
		},
		{
			name: "unparseable start is dropped",
			phases: []map[string]any{
				phase(spTourA, "first_come", "11月10日 18:00", nil, nil),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := &fakeGemini{resp: jsonResponse(t, tt.phases...)}
			s := newTestTicketSaleSearcher(t, f, nil)

			got, err := s.SearchPublishedSales(context.Background(), spInput())
			require.NoError(t, err)
			require.Len(t, got, len(tt.want))
			for i, w := range tt.want {
				assert.Equal(t, w.SeriesID, got[i].SeriesID)
				assert.Equal(t, w.Method, got[i].Method)
				assert.True(t, w.SaleStartTime.Equal(got[i].SaleStartTime), "apply start: got %s", got[i].SaleStartTime)
				assert.True(t, w.SaleEndTime.Equal(got[i].SaleEndTime), "apply end: got %s", got[i].SaleEndTime)
				assert.True(t, w.LotteryResultTime.Equal(got[i].LotteryResultTime), "lottery result: got %s", got[i].LotteryResultTime)
			}
		})
	}
}

// @spec components/entity/ticket-sale/search-published-sales "No series given"
func TestTicketSaleSearcher_NoSeriesGiven(t *testing.T) {
	t.Parallel()

	f := &fakeGemini{resp: jsonResponse(t)}
	s := newTestTicketSaleSearcher(t, f, nil)

	got, err := s.SearchPublishedSales(context.Background(), &entity.PublishedSaleSearchInput{ArtistName: "King Gnu"})
	require.NoError(t, err)
	assert.Empty(t, got)
	assert.Zero(t, f.calls, "no search without a series")
}

func TestTicketSaleSearcher_Failures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		resp      *genai.GenerateContentResponse
		err       error
		wantCode  codes.Code
		wantCalls int
	}{
		{
			// @spec components/entity/ticket-sale/search-published-sales "Spend cap reached"
			name:     "Spend cap reached",
			err:      genai.APIError{Code: http.StatusTooManyRequests, Status: "RESOURCE_EXHAUSTED", Message: "spend cap reached"},
			wantCode: codes.ResourceExhausted,
		},
		{
			// @spec components/entity/ticket-sale/search-published-sales "No result"
			name:     "No result",
			resp:     &genai.GenerateContentResponse{},
			wantCode: codes.Internal,
		},
		{
			// @spec components/entity/ticket-sale/search-published-sales "Service unreachable"
			name:     "Service unreachable",
			err:      errors.New("dial tcp: connection refused"),
			wantCode: codes.Unavailable,
		},
		{
			name:     "server error",
			err:      genai.APIError{Code: http.StatusServiceUnavailable, Message: "overloaded"},
			wantCode: codes.Unavailable,
		},
		{
			// @spec components/entity/ticket-sale/search-published-sales "Invalid request rejected twice"
			name:      "Invalid request rejected twice",
			err:       genai.APIError{Code: http.StatusBadRequest, Message: "bad request"},
			wantCode:  codes.InvalidArgument,
			wantCalls: 2,
		},
		{
			name:     "deadline",
			err:      context.DeadlineExceeded,
			wantCode: codes.DeadlineExceeded,
		},
		{
			name:     "cut-off response",
			resp:     textResponse(`{"phases":[{"series_id":`, genai.FinishReasonMaxTokens),
			wantCode: codes.Internal,
		},
		{
			name:     "invalid JSON",
			resp:     textResponse(`not json`, genai.FinishReasonStop),
			wantCode: codes.Internal,
		},
		{
			name:     "empty text",
			resp:     textResponse("", genai.FinishReasonStop),
			wantCode: codes.Internal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := &fakeGemini{resp: tt.resp, err: tt.err}
			s := newTestTicketSaleSearcher(t, f, nil)

			got, err := s.SearchPublishedSales(context.Background(), spInput())
			require.Error(t, err)
			assert.Nil(t, got)
			wantCalls := tt.wantCalls
			if wantCalls == 0 {
				wantCalls = 1
			}
			assert.Equal(t, wantCalls, f.calls)
			var ae *apperr.AppErr
			require.ErrorAs(t, err, &ae)
			assert.Equal(t, tt.wantCode, ae.Code, "got error: %v", err)
		})
	}
}

func TestTicketSaleSearcher_LogsSearchQueries(t *testing.T) {
	t.Parallel()

	var logs bytes.Buffer
	resp := textResponse(`{"phases":[]}`, genai.FinishReasonStop,
		&genai.Part{ToolCall: &genai.ToolCall{
			ToolType: genai.ToolTypeGoogleSearchWeb,
			Args:     map[string]any{"queries": []any{"King Gnu KICKOFF 先行", "King Gnu チケット"}},
		}},
		&genai.Part{ToolCall: &genai.ToolCall{
			ToolType: genai.ToolTypeGoogleSearchWeb,
			Args:     map[string]any{"queries": []any{"clubgnu 2次受付"}},
		}},
	)
	f := &fakeGemini{resp: resp}
	s := newTestTicketSaleSearcher(t, f, &logs)

	_, err := s.SearchPublishedSales(context.Background(), spInput())
	require.NoError(t, err)

	var line map[string]any
	for l := range strings.SplitSeq(strings.TrimSpace(logs.String()), "\n") {
		if strings.Contains(l, "gemini response metadata") {
			require.NoError(t, json.Unmarshal([]byte(l), &line))
		}
	}
	require.NotNil(t, line, "metadata line is logged")
	assert.Equal(t, []any{"King Gnu KICKOFF 先行", "King Gnu チケット", "clubgnu 2次受付"}, line["search_queries"])
	assert.EqualValues(t, 3, line["search_query_count"])
}

func TestTicketSaleSearcher_ParseCandidateTimes(t *testing.T) {
	t.Parallel()

	start := "2026-10-06T18:30:00+09:00"
	f := &fakeGemini{resp: jsonResponse(t, phase(spTourA, "first_come", start, nil, nil))}
	s := newTestTicketSaleSearcher(t, f, nil)

	got, err := s.SearchPublishedSales(context.Background(), spInput())
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.True(t, mustTime(t, start).Equal(got[0].SaleStartTime))
	assert.True(t, got[0].SaleEndTime.IsZero())
}

// @spec components/entity/ticket-sale/search-published-sales "Invalid request recovered without the tool report"
func TestTicketSaleSearcher_InvalidRequestRetriedWithoutToolReport(t *testing.T) {
	t.Parallel()

	var logs bytes.Buffer
	f := &fakeGemini{respond: func(call int) (*genai.GenerateContentResponse, error) {
		if call == 1 {
			return nil, genai.APIError{Code: http.StatusBadRequest, Status: "INVALID_ARGUMENT", Message: "bad request"}
		}
		return jsonResponse(t, phase(spTourB, "lottery", "2026-10-10T12:00:00+09:00", "2026-10-15T23:59:00+09:00", nil)), nil
	}}
	s := newTestTicketSaleSearcher(t, f, &logs)

	got, err := s.SearchPublishedSales(context.Background(), spInput())

	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, spTourB, got[0].SeriesID)
	require.Len(t, f.cfgs, 2)
	assert.NotNil(t, f.cfgs[0].ToolConfig, "the first request carries the tool-invocation report")
	assert.Nil(t, f.cfgs[1].ToolConfig, "the second request drops it")
	assert.Equal(t, f.cfgs[0].Tools, f.cfgs[1].Tools, "the rest of the request is unchanged")
	assert.Contains(t, logs.String(), `"tool_report_dropped":true`)
}

// TestTicketSaleSearcher_LogsURLContextCounts feeds the URL context tool parts
// recorded from gemini-3.8-flash (2026-10-07): one call for two URLs, one
// retrieved and one not.
func TestTicketSaleSearcher_LogsURLContextCounts(t *testing.T) {
	t.Parallel()

	var logs bytes.Buffer
	resp := textResponse(`{"phases":[]}`, genai.FinishReasonStop,
		&genai.Part{ToolCall: &genai.ToolCall{
			ID:       "call_655975",
			ToolType: genai.ToolTypeURLContext,
			Args:     map[string]any{"urls": []any{"https://kinggnu.jp/live/", "https://kinggnu.jp/missing/"}},
		}},
		&genai.Part{ToolResponse: &genai.ToolResponse{
			ID:       "call_655975",
			ToolType: genai.ToolTypeURLContext,
			Response: map[string]any{"url_metadata": []any{
				map[string]any{"retrieved_url": "https://kinggnu.jp/live/", "url_retrieval_status": "URL_RETRIEVAL_STATUS_SUCCESS"},
				map[string]any{"retrieved_url": "https://kinggnu.jp/missing/", "url_retrieval_status": "URL_RETRIEVAL_STATUS_ERROR"},
			}},
		}},
	)
	s := newTestTicketSaleSearcher(t, &fakeGemini{resp: resp}, &logs)

	_, err := s.SearchPublishedSales(context.Background(), spInput())
	require.NoError(t, err)

	var line map[string]any
	for l := range strings.SplitSeq(strings.TrimSpace(logs.String()), "\n") {
		if strings.Contains(l, "gemini response metadata") {
			require.NoError(t, json.Unmarshal([]byte(l), &line))
		}
	}
	require.NotNil(t, line, "metadata line is logged")
	assert.EqualValues(t, 1, line["url_context_calls"])
	assert.EqualValues(t, 1, line["url_context_succeeded"])
}

// generationRecorder is a GenerationReporter that records each report.
type generationRecorder struct{ gens []posthog.LLMGeneration }

func (r *generationRecorder) ReportGeneration(_ context.Context, g posthog.LLMGeneration) {
	r.gens = append(r.gens, g)
}

// @spec components/entity/ticket-sale/search-published-sales "Discarded sale"
func TestTicketSaleSearcher_ReportsDiscardedSale(t *testing.T) {
	t.Parallel()

	resp := jsonResponse(t,
		phase(spTourA, salesMethodLottery, "2026-11-01T12:00:00+09:00", "2026-11-10T23:59:00+09:00", nil),
		phase(spTourB, salesMethodFirstCome, "2026-11-20T10:00:00+09:00", nil, nil),
		// Opened before spNow: discarded.
		phase(spTourB, salesMethodFirstCome, "2026-10-01T10:00:00+09:00", nil, nil),
	)
	resp.UsageMetadata = &genai.GenerateContentResponseUsageMetadata{PromptTokenCount: 50, ToolUsePromptTokenCount: 300}
	f := &fakeGemini{resp: resp}
	s := newTestTicketSaleSearcher(t, f, nil)
	rec := &generationRecorder{}
	s.config.Reporter = rec
	in := spInput()
	in.ArtistID = "artist-1"

	got, err := s.SearchPublishedSales(context.Background(), in)
	require.NoError(t, err)
	require.Len(t, got, 2)

	require.Len(t, rec.gens, 1)
	g := rec.gens[0]
	assert.Equal(t, &posthog.SaleParseResult{Returned: 3, Kept: 2, Dropped: map[string]int{"already started": 1}}, g.Sales)
	assert.Equal(t, posthog.LLMRequest{ArtistID: "artist-1", OfficialSiteURL: "https://kinggnu.jp/", SeriesIDs: []string{spTourA, spTourB}}, g.Request)
	assert.Equal(t, posthog.LLMUsage{PromptTokens: 50, ToolUseTokens: 300}, g.Usage)
	assert.Empty(t, g.ErrorKind)
}

// The request rejected as invalid and the request sent after it are both
// reported; a response that is not JSON marks the last one.
func TestTicketSaleSearcher_ReportsBothRequestsAfterAnInvalidOne(t *testing.T) {
	t.Parallel()

	f := &fakeGemini{respond: func(call int) (*genai.GenerateContentResponse, error) {
		if call == 1 {
			return nil, genai.APIError{Code: http.StatusBadRequest, Message: "invalid"}
		}
		return textResponse("not json", genai.FinishReasonStop), nil
	}}
	s := newTestTicketSaleSearcher(t, f, nil)
	rec := &generationRecorder{}
	s.config.Reporter = rec

	_, err := s.SearchPublishedSales(context.Background(), spInput())
	require.Error(t, err)

	require.Len(t, rec.gens, 2)
	assert.Equal(t, "invalid_request", rec.gens[0].ErrorKind)
	assert.Equal(t, "invalid_json", rec.gens[1].ErrorKind)
	assert.Nil(t, rec.gens[1].Sales)
}
