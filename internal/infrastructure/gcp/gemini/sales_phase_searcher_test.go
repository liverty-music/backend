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
func spInput() *entity.SalesPhaseSearchInput {
	return &entity.SalesPhaseSearchInput{
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

// fakeGemini records the request and answers with a canned response.
type fakeGemini struct {
	resp  *genai.GenerateContentResponse
	err   error
	calls int
	model string
	text  string
	cfg   *genai.GenerateContentConfig
}

func (f *fakeGemini) generate(
	_ context.Context, model string, contents []*genai.Content, cfg *genai.GenerateContentConfig,
) (*genai.GenerateContentResponse, error) {
	f.calls++
	f.model = model
	f.cfg = cfg
	for _, c := range contents {
		for _, p := range c.Parts {
			f.text += p.Text
		}
	}
	return f.resp, f.err
}

func newTestSalesPhaseSearcher(t *testing.T, f *fakeGemini, logs *bytes.Buffer) *SalesPhaseSearcher {
	t.Helper()
	opts := []logging.Option{logging.WithFormat(logging.FormatJSON)}
	if logs != nil {
		opts = append(opts, logging.WithWriter(logs))
	}
	logger, err := logging.New(opts...)
	require.NoError(t, err)
	return &SalesPhaseSearcher{
		generate: f.generate,
		now:      func() time.Time { return spNow },
		config:   SalesPhaseConfig{APIKey: "k", Model: "gemini-3.8-flash", Thinking: "low"},
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

func TestSalesPhaseSearcher_BuildsRequest(t *testing.T) {
	t.Parallel()

	f := &fakeGemini{resp: jsonResponse(t)}
	s := newTestSalesPhaseSearcher(t, f, nil)

	_, err := s.SearchSalesPhases(context.Background(), spInput())
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
	assert.Equal(t, systemInstructionSalesPhase, cfg.SystemInstruction.Parts[0].Text)

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

func TestSalesPhaseSearcher_Checks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		phases []map[string]any
		want   []*entity.SalesPhaseCandidate
	}{
		{
			// @spec components/entity/sales-phase/search-sales-phases "Two rounds announced ahead"
			name: "Two rounds announced ahead",
			phases: []map[string]any{
				phase(spTourA, "lottery", "2026-10-06T18:00:00+09:00", "2026-10-22T23:59:00+09:00", nil),
				phase(spTourA, "lottery", "2026-11-10T18:00:00+09:00", "2026-11-19T23:59:00+09:00", nil),
			},
			want: []*entity.SalesPhaseCandidate{
				{SeriesID: spTourA, Method: entity.SalesMethodLottery,
					ApplyStartTime: time.Date(2026, 10, 6, 18, 0, 0, 0, jst), ApplyEndTime: time.Date(2026, 10, 22, 23, 59, 0, 0, jst)},
				{SeriesID: spTourA, Method: entity.SalesMethodLottery,
					ApplyStartTime: time.Date(2026, 11, 10, 18, 0, 0, 0, jst), ApplyEndTime: time.Date(2026, 11, 19, 23, 59, 0, 0, jst)},
			},
		},
		{
			// @spec components/entity/sales-phase/search-sales-phases "Sale already open"
			name: "Sale already open",
			phases: []map[string]any{
				phase(spTourA, "lottery", "2026-10-04T18:00:00+09:00", "2026-10-12T23:59:00+09:00", nil),
			},
		},
		{
			// @spec components/entity/sales-phase/search-sales-phases "Sale abroad"
			name: "Sale abroad",
			phases: []map[string]any{
				phase(spTourB, "first_come", "2026-11-01T12:00:00+08:00", nil, nil),
			},
			want: []*entity.SalesPhaseCandidate{
				{SeriesID: spTourB, Method: entity.SalesMethodFirstCome, ApplyStartTime: time.Date(2026, 11, 1, 13, 0, 0, 0, jst)},
			},
		},
		{
			// @spec components/entity/sales-phase/search-sales-phases "Year omitted"
			name: "Year omitted",
			phases: []map[string]any{
				phase(spTourA, "lottery", "2026-11-10T18:00:00+09:00", "2026-11-19T23:59:00+09:00", nil),
			},
			want: []*entity.SalesPhaseCandidate{
				{SeriesID: spTourA, Method: entity.SalesMethodLottery,
					ApplyStartTime: time.Date(2026, 11, 10, 18, 0, 0, 0, jst), ApplyEndTime: time.Date(2026, 11, 19, 23, 59, 0, 0, jst)},
			},
		},
		{
			name: "year mistaken into the past is dropped",
			phases: []map[string]any{
				phase(spTourA, "lottery", "2025-11-10T18:00:00+09:00", "2025-11-19T23:59:00+09:00", nil),
			},
		},
		{
			// @spec components/entity/sales-phase/search-sales-phases "Lottery with a published result date"
			name: "Lottery with a published result date",
			phases: []map[string]any{
				phase(spTourA, "lottery", "2026-10-06T18:00:00+09:00", "2026-10-22T23:59:00+09:00", "2026-11-03T15:00:00+09:00"),
			},
			want: []*entity.SalesPhaseCandidate{
				{SeriesID: spTourA, Method: entity.SalesMethodLottery,
					ApplyStartTime:    time.Date(2026, 10, 6, 18, 0, 0, 0, jst),
					ApplyEndTime:      time.Date(2026, 10, 22, 23, 59, 0, 0, jst),
					LotteryResultTime: time.Date(2026, 11, 3, 15, 0, 0, 0, jst)},
			},
		},
		{
			// @spec components/entity/sales-phase/search-sales-phases "Lottery without a published result date"
			name: "Lottery without a published result date",
			phases: []map[string]any{
				phase(spTourA, "lottery", "2026-10-06T18:00:00+09:00", "2026-10-22T23:59:00+09:00", nil),
			},
			want: []*entity.SalesPhaseCandidate{
				{SeriesID: spTourA, Method: entity.SalesMethodLottery,
					ApplyStartTime: time.Date(2026, 10, 6, 18, 0, 0, 0, jst), ApplyEndTime: time.Date(2026, 10, 22, 23, 59, 0, 0, jst)},
			},
		},
		{
			// @spec components/entity/sales-phase/search-sales-phases "First-come sale until sold out"
			name: "First-come sale until sold out",
			phases: []map[string]any{
				phase(spTourA, "first_come", "2026-10-06T18:30:00+09:00", nil, nil),
			},
			want: []*entity.SalesPhaseCandidate{
				{SeriesID: spTourA, Method: entity.SalesMethodFirstCome, ApplyStartTime: time.Date(2026, 10, 6, 18, 30, 0, 0, jst)},
			},
		},
		{
			// @spec components/entity/sales-phase/search-sales-phases "Lottery without a stated close"
			name: "Lottery without a stated close",
			phases: []map[string]any{
				phase(spTourA, "lottery", "2026-10-06T18:00:00+09:00", nil, nil),
			},
		},
		{
			// @spec components/entity/sales-phase/search-sales-phases "Method not stated"
			name: "Method not stated",
			phases: []map[string]any{
				phase(spTourA, "", "2026-10-06T18:00:00+09:00", "2026-10-22T23:59:00+09:00", nil),
			},
		},
		{
			// @spec components/entity/sales-phase/search-sales-phases "Nothing usable found"
			name: "Nothing usable found",
		},
		{
			// @spec components/entity/sales-phase/search-sales-phases "Close before open"
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
			// @spec components/entity/sales-phase/search-sales-phases "Opening after the last show"
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
			want: []*entity.SalesPhaseCandidate{
				{SeriesID: spTourB, Method: entity.SalesMethodFirstCome, ApplyStartTime: time.Date(2026, 12, 20, 10, 0, 0, 0, jst)},
			},
		},
		{
			// @spec components/entity/sales-phase/search-sales-phases "Unattributable sale"
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
			s := newTestSalesPhaseSearcher(t, f, nil)

			got, err := s.SearchSalesPhases(context.Background(), spInput())
			require.NoError(t, err)
			require.Len(t, got, len(tt.want))
			for i, w := range tt.want {
				assert.Equal(t, w.SeriesID, got[i].SeriesID)
				assert.Equal(t, w.Method, got[i].Method)
				assert.True(t, w.ApplyStartTime.Equal(got[i].ApplyStartTime), "apply start: got %s", got[i].ApplyStartTime)
				assert.True(t, w.ApplyEndTime.Equal(got[i].ApplyEndTime), "apply end: got %s", got[i].ApplyEndTime)
				assert.True(t, w.LotteryResultTime.Equal(got[i].LotteryResultTime), "lottery result: got %s", got[i].LotteryResultTime)
			}
		})
	}
}

// @spec components/entity/sales-phase/search-sales-phases "No series given"
func TestSalesPhaseSearcher_NoSeriesGiven(t *testing.T) {
	t.Parallel()

	f := &fakeGemini{resp: jsonResponse(t)}
	s := newTestSalesPhaseSearcher(t, f, nil)

	got, err := s.SearchSalesPhases(context.Background(), &entity.SalesPhaseSearchInput{ArtistName: "King Gnu"})
	require.NoError(t, err)
	assert.Empty(t, got)
	assert.Zero(t, f.calls, "no search without a series")
}

func TestSalesPhaseSearcher_Failures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		resp     *genai.GenerateContentResponse
		err      error
		wantCode codes.Code
	}{
		{
			// @spec components/entity/sales-phase/search-sales-phases "Spend cap reached"
			name:     "Spend cap reached",
			err:      genai.APIError{Code: http.StatusTooManyRequests, Status: "RESOURCE_EXHAUSTED", Message: "spend cap reached"},
			wantCode: codes.ResourceExhausted,
		},
		{
			// @spec components/entity/sales-phase/search-sales-phases "No result"
			name:     "No result",
			resp:     &genai.GenerateContentResponse{},
			wantCode: codes.Internal,
		},
		{
			// @spec components/entity/sales-phase/search-sales-phases "Service unreachable"
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
			name:     "rejected request",
			err:      genai.APIError{Code: http.StatusBadRequest, Message: "bad request"},
			wantCode: codes.InvalidArgument,
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
			s := newTestSalesPhaseSearcher(t, f, nil)

			got, err := s.SearchSalesPhases(context.Background(), spInput())
			require.Error(t, err)
			assert.Nil(t, got)
			assert.Equal(t, 1, f.calls, "single attempt")
			var ae *apperr.AppErr
			require.ErrorAs(t, err, &ae)
			assert.Equal(t, tt.wantCode, ae.Code, "got error: %v", err)
		})
	}
}

func TestSalesPhaseSearcher_LogsSearchQueries(t *testing.T) {
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
	s := newTestSalesPhaseSearcher(t, f, &logs)

	_, err := s.SearchSalesPhases(context.Background(), spInput())
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

func TestSalesPhaseSearcher_ParseCandidateTimes(t *testing.T) {
	t.Parallel()

	start := "2026-10-06T18:30:00+09:00"
	f := &fakeGemini{resp: jsonResponse(t, phase(spTourA, "first_come", start, nil, nil))}
	s := newTestSalesPhaseSearcher(t, f, nil)

	got, err := s.SearchSalesPhases(context.Background(), spInput())
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.True(t, mustTime(t, start).Equal(got[0].ApplyStartTime))
	assert.True(t, got[0].ApplyEndTime.IsZero())
}
