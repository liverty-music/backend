//go:build integration

package gemini

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/analytics/posthog"
	"github.com/pannpers/go-logging/logging"
)

// Published ticket sale search evaluation harness. It runs the production
// TicketSaleSearcher on fixtures checked against the official pages and scores
// the kept sales: found / missed / extra, and wrong values on found sales.
// Every call is a billed grounded call, so it runs only when GEMINI_SP_EVAL=1.
//
// Env:
//
//	GEMINI_SP_EVAL=1             enable
//	GCP_GEMINI_SEARCH_API_KEY    Gemini API key (dev project)
//	GEMINI_SP_EVAL_MODEL         model (default gemini-4-hydrogen)
//	GEMINI_SP_EVAL_THINKING      thinking level (default low)
//	GEMINI_SP_EVAL_CASES         comma list of case names (default: all)
//	GEMINI_SP_EVAL_REPS          repetitions per case (default 3)
//
// Results go to testdata/ab_results/ticket_sale_<UTC time>.csv and .json,
// one row per search (see spEvalCall). A full run is 27 serial calls of up
// to a minute each, longer than go test's default 10-minute timeout:
//
//	GEMINI_SP_EVAL=1 GCP_GEMINI_SEARCH_API_KEY=<key> \
//	  go test -tags=integration -count=1 -timeout=1h -v \
//	  -run TestTicketSaleSearchEval ./internal/infrastructure/gcp/gemini/
//
// The fixture was captured on 2026-11-21 from public pages, with the current
// time frozen to 2026-11-06 12:00 JST, before every expected sale opened: on
// the capture day few announced sales had not opened yet, while the sales
// opened in November were still listed with their dates (OpenSpec change
// move-ticket-sale-search-to-gemini-4-hydrogen, design D1). Google Search is
// limited to the 30 days before the frozen time, so a later announcement is
// reachable only through URL context. Artists and series are prod's, with
// prod's official site URLs:
//
//   - King Gnu, "KICKOFF": CLUB GNU 2次受付 (a later round with a result date).
//   - ずっと真夜中でいいのに。, Hall Tour「アピクラ電機」: three sales of one
//     series; the page writes 当落発表 for each, not 抽選.
//   - Mrs. GREEN APPLE, "on Harmony2": 全会員先行.
//   - Suchmos, BAY SIDE TOUR 2027: オフィシャル先行 (ぴあ 5次, 抽選).
//   - 10-FEET, THE ORCHESTRA and the Budokan show (two series, sales only on
//     the first; its two lotteries share a start day and count as one).
//   - SUPER BEAVER, TOUR 2027-2028 and the Budokan shows (two series, sales
//     only on the first).
//   - Creepy Nuts, ONEMAN TOUR 2027: オフィシャル4次先行 and アップグレード1次受付.
//   - WurtS, SPARK NIGHT PROM +ADDITIONAL: ぴあプレリザーブ and e+ △プレオーダー.
//   - Novelbright, 2027 ARENA TOUR: NOVELCITY and ローチケ lotteries.

// spEvalNow freezes "current time" for the prompt and the checks so every run
// is scored against the same ground truth.
var spEvalNow = time.Date(2026, 11, 6, 12, 0, 0, 0, jst)

type spEvalExpected struct {
	// Name identifies the sale in the results: the artist and the sale's
	// name on its page.
	Name           string
	SeriesID       string
	Method         entity.TicketSaleMethod
	ApplyStartTime string
	ApplyEndTime   string
	// ResultDate is the expected result date (YYYY-MM-DD); empty means the
	// sale must have none.
	ResultDate string
}

type spEvalCase struct {
	Name     string
	Input    *entity.PublishedSaleSearchInput
	Expected []spEvalExpected
}

func spEvalDates(dates ...string) []time.Time {
	out := make([]time.Time, 0, len(dates))
	for _, d := range dates {
		t, err := time.ParseInLocation(time.DateOnly, d, jst)
		if err != nil {
			panic(err)
		}
		out = append(out, t)
	}
	return out
}

func spEvalSeries(id, title string, first, last string) *entity.SalesSeriesRef {
	return &entity.SalesSeriesRef{SeriesID: id, Title: title, EventDates: spEvalDates(first, last)}
}

const (
	spKingGnuKickoff  = "01a05c6a-9e24-701e-88c6-158d7639b711"
	spZutomayoApicula = "01a15368-3dbe-7f95-b7eb-2b29e01c27fd"
	spMGAHarmony2     = "01a10f31-6468-7140-afae-a3cd25a1c3ad"
	spSuchmosBaySide  = "019f8cd5-e9b2-7019-ae6a-a0358d768a42"
	sp10FeetOrchestra = "01a1f845-eb24-7f1c-9966-0a2d23659504"
	sp10FeetBudokan   = "01a1966a-7b9e-781e-a07e-c98599a5a032"
	spBeaverTour2728  = "01a10f3b-0408-7539-b206-bdecbc08f98c"
	spBeaverBudokan   = "01a1c4c0-1c64-799f-a490-1170e52a556f"
	spCreepyTour2027  = "01a05c49-6a32-7795-bc34-7a9b8be922c3"
	spWurtSAdditional = "01a05c7a-ca1e-7174-bfea-cfda7dac33be"
	spNovelArena2027  = "01a15369-33f2-7ddc-9ccc-777f00b39524"
)

// spEvalCases were checked on the pages named in each case on 2026-11-21.
var spEvalCases = []spEvalCase{
	{
		Name: "kinggnu",
		Input: &entity.PublishedSaleSearchInput{
			ArtistName: "King Gnu", OfficialSiteURL: "https://kinggnu.jp/",
			Series: []*entity.SalesSeriesRef{spEvalSeries(spKingGnuKickoff,
				"King Gnu 10th Anniversary Opening Live “KICKOFF”", "2027-02-17", "2027-03-14")},
		},
		// clubgnu.com/10th_KICKOFF/: 「CLUB GNU」チケット2次受付 (抽選制),
		// 抽選結果確認／入金期間 12月2日(水)15:00～. The 最速先行 opened on
		// 10月5日 and must not be returned.
		Expected: []spEvalExpected{
			{Name: "King Gnu CLUB GNU 2次受付", SeriesID: spKingGnuKickoff, Method: entity.TicketSaleMethodLottery,
				ApplyStartTime: "2026-11-10T18:00:00+09:00", ApplyEndTime: "2026-11-19T23:59:00+09:00", ResultDate: "2026-12-02"},
		},
	},
	{
		Name: "zutomayo",
		Input: &entity.PublishedSaleSearchInput{
			ArtistName: "ずっと真夜中でいいのに。", OfficialSiteURL: "https://zutomayo.net/",
			Series: []*entity.SalesSeriesRef{spEvalSeries(spZutomayoApicula,
				"Hall Tour「アピクラ電機」LÚ.NIM.LÀL", "2027-01-15", "2027-05-23")},
		},
		// zutomayo.net/apicula/ (受付期間 and 当落発表・入金期間 of each sale);
		// w.pia.jp/v/zutomayo27hall-is/ for 一般最速先行 (結果発表 2026年12月05日).
		Expected: []spEvalExpected{
			{Name: "ずとまよ PREMIUM会員二次先行", SeriesID: spZutomayoApicula, Method: entity.TicketSaleMethodLottery,
				ApplyStartTime: "2026-11-07T20:00:00+09:00", ApplyEndTime: "2026-11-09T23:59:00+09:00", ResultDate: "2026-11-17"},
			{Name: "ずとまよ 一般最速先行", SeriesID: spZutomayoApicula, Method: entity.TicketSaleMethodLottery,
				ApplyStartTime: "2026-11-17T20:00:00+09:00", ApplyEndTime: "2026-11-29T23:59:00+09:00", ResultDate: "2026-12-05"},
			{Name: "ずとまよ プレミアム席 Upgrade", SeriesID: spZutomayoApicula, Method: entity.TicketSaleMethodLottery,
				ApplyStartTime: "2026-12-01T20:00:00+09:00", ApplyEndTime: "2026-12-06T23:59:00+09:00", ResultDate: "2026-12-12"},
		},
	},
	{
		Name: "mrsgreenapple",
		Input: &entity.PublishedSaleSearchInput{
			ArtistName: "Mrs. GREEN APPLE", OfficialSiteURL: "http://mrsgreenapple.com/",
			Series: []*entity.SalesSeriesRef{spEvalSeries(spMGAHarmony2,
				"Mrs. GREEN APPLE on Harmony2", "2027-02-20", "2027-06-02")},
		},
		// mrsgreenapple.com/feature/harmony_2: 「Ringo Jam」全会員先行,
		// 当落発表・入金期間 2026年12月5日(土)20:00～. The 全会員＋CD購入者限定先行
		// opened on 2026年10月27日.
		Expected: []spEvalExpected{
			{Name: "Mrs. GREEN APPLE Ringo Jam 全会員先行", SeriesID: spMGAHarmony2, Method: entity.TicketSaleMethodLottery,
				ApplyStartTime: "2026-11-11T20:00:00+09:00", ApplyEndTime: "2026-11-23T23:59:00+09:00", ResultDate: "2026-12-05"},
		},
	},
	{
		Name: "suchmos",
		Input: &entity.PublishedSaleSearchInput{
			ArtistName: "Suchmos", OfficialSiteURL: "http://www.suchmos.com/",
			Series: []*entity.SalesSeriesRef{spEvalSeries(spSuchmosBaySide,
				"Suchmos BAY SIDE TOUR 2027", "2027-03-13", "2027-05-02")},
		},
		// suchmos.com/live: チケットオフィシャル先行 2026/11/19(木) 正午12:00～
		// 2026/11/30(月) 23:59; w.pia.jp/t/suchmos-tour2027/ lists it as 抽選.
		Expected: []spEvalExpected{
			{Name: "Suchmos チケットオフィシャル先行", SeriesID: spSuchmosBaySide, Method: entity.TicketSaleMethodLottery,
				ApplyStartTime: "2026-11-19T12:00:00+09:00", ApplyEndTime: "2026-11-30T23:59:00+09:00"},
		},
	},
	{
		Name: "10feet",
		Input: &entity.PublishedSaleSearchInput{
			ArtistName: "10-FEET", OfficialSiteURL: "https://10-feet.kyoto/",
			Series: []*entity.SalesSeriesRef{
				spEvalSeries(sp10FeetOrchestra, "X-ORCHESTRA PRESENTS 10-FEET THE ORCHESTRA", "2027-03-05", "2027-03-16"),
				spEvalSeries(sp10FeetBudokan, "10-FEET 30th Anniversary ONE-MAN LIVE『10-FEET HISTORY』〜日本武道館〜", "2027-04-27", "2027-04-27"),
			},
		},
		// 10-feet.kyoto/pages/orchestra: 10-FEET MOBILE会員先行(抽選) until
		// 11.23 and オフィシャル先行(抽選) until 11.30, both from 11.16(月)
		// 20:00, one sale as prod stores them. The Budokan show's sales
		// (10-feet.kyoto/pages/budokan) opened on 10.29 and 11.4.
		Expected: []spEvalExpected{
			{Name: "10-FEET ORCHESTRA オフィシャル先行", SeriesID: sp10FeetOrchestra, Method: entity.TicketSaleMethodLottery,
				ApplyStartTime: "2026-11-16T20:00:00+09:00", ApplyEndTime: "2026-11-30T23:59:00+09:00"},
		},
	},
	{
		Name: "superbeaver",
		Input: &entity.PublishedSaleSearchInput{
			ArtistName: "SUPER BEAVER", OfficialSiteURL: "http://www.super-beaver.com/",
			Series: []*entity.SalesSeriesRef{
				spEvalSeries(spBeaverBudokan, "都会のラクダSP at 日本武道館 〜ラクダフロムトウキョウジャパン〜", "2027-03-31", "2027-04-01"),
				spEvalSeries(spBeaverTour2728, "都会のラクダ TOUR 2027-2028 〜 ラクダムーンと、サンサンサンライズ 〜", "2027-09-24", "2028-03-19"),
			},
		},
		// eplus.jp/sb27-28/arena/: 抽選 ◆<日付公演>2次プレオーダー先行
		// 2026/11/21(土)13:00～2026/11/29(日)23:59, one row per show. The
		// ファミリーマート先行 opened on 2026/11/3; the Budokan shows' 友の会
		// sales (sp.super-beaver.com/feature/budokan2027) ended on 11月1日.
		Expected: []spEvalExpected{
			{Name: "SUPER BEAVER 2次プレオーダー先行", SeriesID: spBeaverTour2728, Method: entity.TicketSaleMethodLottery,
				ApplyStartTime: "2026-11-21T13:00:00+09:00", ApplyEndTime: "2026-11-29T23:59:00+09:00"},
		},
	},
	{
		Name: "creepynuts",
		Input: &entity.PublishedSaleSearchInput{
			ArtistName: "Creepy Nuts", OfficialSiteURL: "https://creepynuts.com/",
			Series: []*entity.SalesSeriesRef{spEvalSeries(spCreepyTour2027,
				"Creepy Nuts ONEMAN TOUR 2027", "2027-02-27", "2027-05-30")},
		},
		// creepynuts.com/onemantour2027/: オフィシャル4次先行 11.13(金)12:00 ～
		// 11.29(日)23:59 (抽選 on t.pia.jp eventCd=2629575) and アップグレード1次受付
		// (抽選) 12.11(金)18:00 ～ 12.20(日)23:59. The 3次先行 opened on 10.23.
		Expected: []spEvalExpected{
			{Name: "Creepy Nuts オフィシャル4次先行", SeriesID: spCreepyTour2027, Method: entity.TicketSaleMethodLottery,
				ApplyStartTime: "2026-11-13T12:00:00+09:00", ApplyEndTime: "2026-11-29T23:59:00+09:00"},
			{Name: "Creepy Nuts アップグレード1次受付", SeriesID: spCreepyTour2027, Method: entity.TicketSaleMethodLottery,
				ApplyStartTime: "2026-12-11T18:00:00+09:00", ApplyEndTime: "2026-12-20T23:59:00+09:00"},
		},
	},
	{
		Name: "wurts",
		Input: &entity.PublishedSaleSearchInput{
			ArtistName: "WurtS", OfficialSiteURL: "https://www.universal-music.co.jp/wurts/",
			Series: []*entity.SalesSeriesRef{spEvalSeries(spWurtSAdditional,
				"WurtS LIVE TOUR – SPARK NIGHT PROM – +ADDITIONAL", "2027-03-05", "2027-03-16")},
		},
		// wurts.jp/3238/, published 2026-11-09 12:00: ぴあプレリザーブ(抽選)
		// opens 「只今より」 until 11/15(日)23:59. eplus.jp/wurts/: 抽選
		// ◆△プレオーダー受付 2026/11/18(水)12:00～2026/11/29(日)23:59 (also
		// wurts.jp/3240/). The 〇プレオーダー opened on 2026/10/20.
		Expected: []spEvalExpected{
			{Name: "WurtS ぴあプレリザーブ", SeriesID: spWurtSAdditional, Method: entity.TicketSaleMethodLottery,
				ApplyStartTime: "2026-11-09T12:00:00+09:00", ApplyEndTime: "2026-11-15T23:59:00+09:00"},
			{Name: "WurtS △プレオーダー", SeriesID: spWurtSAdditional, Method: entity.TicketSaleMethodLottery,
				ApplyStartTime: "2026-11-18T12:00:00+09:00", ApplyEndTime: "2026-11-29T23:59:00+09:00"},
		},
	},
	{
		Name: "novelbright",
		Input: &entity.PublishedSaleSearchInput{
			ArtistName: "Novelbright", OfficialSiteURL: "https://novelbright.jp/",
			Series: []*entity.SalesSeriesRef{spEvalSeries(spNovelArena2027,
				"Novelbright 2027 ARENA TOUR", "2027-04-17", "2027-10-17")},
		},
		// novelbright.jp/news/detail/2551 (「NOVELCITY」抽選先行) and
		// novelbright.jp/news/detail/2555 (ローソンチケット抽選先行); neither
		// states a result day.
		Expected: []spEvalExpected{
			{Name: "Novelbright NOVELCITY 抽選先行", SeriesID: spNovelArena2027, Method: entity.TicketSaleMethodLottery,
				ApplyStartTime: "2026-11-17T18:00:00+09:00", ApplyEndTime: "2026-11-21T23:59:00+09:00"},
			{Name: "Novelbright ローソンチケット抽選先行", SeriesID: spNovelArena2027, Method: entity.TicketSaleMethodLottery,
				ApplyStartTime: "2026-11-22T13:00:00+09:00", ApplyEndTime: "2026-12-03T23:59:00+09:00"},
		},
	},
}

// spEvalSame reports whether a returned time equals the expected RFC 3339
// value; an empty expected value means the time must be unknown.
func spEvalSame(want string, got time.Time) bool {
	if want == "" {
		return got.IsZero()
	}
	w, err := time.Parse(time.RFC3339, want)
	return err == nil && w.Equal(got)
}

// spEvalScore is the score of one search against a case's expected sales.
type spEvalScore struct {
	// Found and Missed name the expected sales (spEvalExpected.Name) the
	// search returned and did not return, so runs compare sale by sale.
	Found  []string `json:"found"`
	Missed []string `json:"missed"`
	// Extra describes each returned sale that matches no expected one.
	Extra []string `json:"extra"`
	// Wrong describes each wrong end time or result date on a found sale.
	Wrong []string `json:"wrong"`
}

// spEvalDescribe returns a one-line description of a returned sale.
func spEvalDescribe(c *entity.DiscoveredTicketSale) string {
	result := ""
	if c.ResultDate != nil {
		result = " result=" + c.ResultDate.Format(time.DateOnly)
	}
	return fmt.Sprintf("%s %s method=%d %s..%s%s", c.SeriesID, c.Name, c.Method,
		c.SaleStartTime.Format(time.RFC3339), c.SaleEndTime.Format(time.RFC3339), result)
}

// spEvalStartDay is the Japan-time day a sale opens: prod stores one sale per
// series, method and that day (uq_ticket_sales_discovered_convergence).
func spEvalStartDay(t time.Time) string { return t.In(jst).Format(time.DateOnly) }

// scoreSales matches the returned sales to the expected ones as prod stores
// them: an expected sale is found when a returned sale has its series, method
// and apply start time, and the other returned sales of the same series,
// method and start day are the same stored sale, not extras. The end time and
// result date are right when one sale of that group carries them.
func scoreSales(got []*entity.DiscoveredTicketSale, expected []spEvalExpected) spEvalScore {
	sc := spEvalScore{Found: []string{}, Missed: []string{}, Extra: []string{}, Wrong: []string{}}
	used := make([]bool, len(got))
	for _, e := range expected {
		var group []*entity.DiscoveredTicketSale
		matched := false
		for i, c := range got {
			if c.SeriesID != e.SeriesID || c.Method != e.Method {
				continue
			}
			if spEvalSame(e.ApplyStartTime, c.SaleStartTime) {
				matched = true
			}
			if want, err := time.Parse(time.RFC3339, e.ApplyStartTime); err == nil &&
				spEvalStartDay(want) == spEvalStartDay(c.SaleStartTime) {
				group = append(group, c)
				used[i] = true
			}
		}
		if !matched {
			sc.Missed = append(sc.Missed, e.Name)
			continue
		}
		sc.Found = append(sc.Found, e.Name)
		endOK, resultOK := false, false
		var ends, results []string
		for _, c := range group {
			gotResult := ""
			if c.ResultDate != nil {
				gotResult = c.ResultDate.Format(time.DateOnly)
			}
			endOK = endOK || spEvalSame(e.ApplyEndTime, c.SaleEndTime)
			resultOK = resultOK || gotResult == e.ResultDate
			ends = append(ends, c.SaleEndTime.Format(time.RFC3339))
			results = append(results, gotResult)
		}
		if !endOK {
			sc.Wrong = append(sc.Wrong, e.Name+" apply_end="+strings.Join(ends, ",")+" want "+e.ApplyEndTime)
		}
		if !resultOK {
			sc.Wrong = append(sc.Wrong, e.Name+" result_date="+strings.Join(results, ",")+" want "+e.ResultDate)
		}
	}
	for i, u := range used {
		if !u {
			sc.Extra = append(sc.Extra, spEvalDescribe(got[i]))
		}
	}
	return sc
}

// spEvalCall is one search of the evaluation: its score and what it cost.
// The token counts, search queries and cost add up every request the search
// sent (a request rejected as invalid is sent once more).
type spEvalCall struct {
	Model    string `json:"model"`
	Thinking string `json:"thinking"`
	Case     string `json:"case"`
	Rep      int    `json:"rep"`
	spEvalScore
	// Returned is every sale the search kept, for checking a miss or an
	// extra against the pages.
	Returned         []string `json:"returned"`
	Requests         int      `json:"requests"`
	SearchQueries    []string `json:"search_queries"`
	PromptTokens     int      `json:"prompt_tokens"`
	CandidatesTokens int      `json:"candidates_tokens"`
	ThinkingTokens   int      `json:"thinking_tokens"`
	ToolUseTokens    int      `json:"tool_use_tokens"`
	// CostUSD2027 is the cost at the price from 2027-01-01 (Pricing2027).
	CostUSD2027 float64 `json:"cost_usd_2027"`
	LatencyMS   int64   `json:"latency_ms"`
	// Empty is true when the model answered with no candidate, no text or an
	// unfinished response; ErrorKind is the generation error kind of a
	// failed search, Error its message.
	Empty     bool   `json:"empty"`
	ErrorKind string `json:"error_kind,omitempty"`
	Error     string `json:"error,omitempty"`
}

// spEvalReporter collects the generations of the current search.
type spEvalReporter struct {
	gens []posthog.LLMGeneration
}

func (r *spEvalReporter) ReportGeneration(_ context.Context, g posthog.LLMGeneration) {
	r.gens = append(r.gens, g)
}

// fill adds the collected generations' usage, queries and cost to c.
func (r *spEvalReporter) fill(c *spEvalCall) {
	c.Requests = len(r.gens)
	c.SearchQueries = []string{}
	for _, g := range r.gens {
		c.SearchQueries = append(c.SearchQueries, g.WebSearchQueries...)
		c.PromptTokens += g.Usage.PromptTokens
		c.CandidatesTokens += g.Usage.CandidatesTokens
		c.ThinkingTokens += g.Usage.ThinkingTokens
		c.ToolUseTokens += g.Usage.ToolUseTokens
		c.CostUSD2027 += Pricing2027.CostUSD(c.Model,
			int32(g.Usage.PromptTokens), int32(g.Usage.CandidatesTokens), int32(g.Usage.ThinkingTokens),
			int32(g.Usage.ToolUseTokens), int32(len(g.WebSearchQueries)))
	}
	// Only the last request decides the search: a request rejected as invalid
	// is sent once more, and a retry that succeeds leaves no error.
	if n := len(r.gens); n > 0 {
		c.ErrorKind = r.gens[n-1].ErrorKind
	}
	switch c.ErrorKind {
	case errorKindNoCandidates, errorKindNoText, errorKindIncomplete:
		c.Empty = true
	}
}

// TestTicketSaleSearchEval runs the production searcher on the fixtures and
// writes one row per search to testdata/ab_results/ticket_sale_<time>.csv and
// .json. It fails on an extra sale or a wrong value: the artists with two
// series have upcoming sales on one only, the pages list ticket trades and
// sales that opened before the frozen time, and several lotteries state a
// result day. A missed sale does not fail it; recall is compared across runs.
//
// @spec components/entity/ticket-sale/search-published-sales "Artist with two tours"
// @spec components/entity/ticket-sale/search-published-sales "Official ticket trade"
// @spec components/entity/ticket-sale/search-published-sales "Lottery page states the result day"
func TestTicketSaleSearchEval(t *testing.T) {
	if os.Getenv("GEMINI_SP_EVAL") != "1" {
		t.Skip("set GEMINI_SP_EVAL=1 to run")
	}
	apiKey := os.Getenv("GCP_GEMINI_SEARCH_API_KEY")
	if apiKey == "" {
		t.Fatal("GCP_GEMINI_SEARCH_API_KEY is required")
	}
	model := envOr("GEMINI_SP_EVAL_MODEL", "gemini-4-hydrogen")
	thinking := envOr("GEMINI_SP_EVAL_THINKING", "low")
	reps := 3
	if v := os.Getenv("GEMINI_SP_EVAL_REPS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			t.Fatalf("GEMINI_SP_EVAL_REPS must be a positive integer, got %q", v)
		}
		reps = n
	}
	caseFilter := map[string]bool{}
	if v := os.Getenv("GEMINI_SP_EVAL_CASES"); v != "" {
		for n := range strings.SplitSeq(v, ",") {
			caseFilter[n] = true
		}
	}

	ctx := context.Background()
	logger, err := logging.New()
	if err != nil {
		t.Fatalf("logger: %v", err)
	}
	reporter := &spEvalReporter{}
	s, err := NewTicketSaleSearcher(ctx, TicketSaleSearcherConfig{
		APIKey: apiKey, Model: model, Thinking: thinking, Reporter: reporter,
	}, http.DefaultClient, logger)
	if err != nil {
		t.Fatalf("searcher: %v", err)
	}
	s.now = func() time.Time { return spEvalNow }

	// The files are rewritten after every search, so a run cut off by the
	// test timeout (which skips deferred calls) keeps the calls paid for.
	base := filepath.Join("testdata/ab_results", "ticket_sale_"+time.Now().UTC().Format("20060102T150405Z"))
	var calls []spEvalCall
	t.Logf("writing %s.json and .csv", base)
	for rep := 1; rep <= reps; rep++ {
		for _, c := range spEvalCases {
			if len(caseFilter) > 0 && !caseFilter[c.Name] {
				continue
			}
			reporter.gens = nil
			call := spEvalCall{Model: model, Thinking: thinking, Case: c.Name, Rep: rep}
			started := time.Now()
			got, err := s.SearchPublishedSales(ctx, c.Input)
			call.LatencyMS = time.Since(started).Milliseconds()
			reporter.fill(&call)
			if err != nil {
				call.Error = err.Error()
				call.spEvalScore = spEvalScore{Found: []string{}, Missed: []string{}, Extra: []string{}, Wrong: []string{}}
				for _, e := range c.Expected {
					call.Missed = append(call.Missed, e.Name)
				}
			} else {
				call.spEvalScore = scoreSales(got, c.Expected)
				for _, g := range got {
					call.Returned = append(call.Returned, spEvalDescribe(g))
				}
			}
			t.Logf("%-14s #%d found=%d missed=%v extra=%v wrong=%v queries=%d cost_2027=$%.4f %.0fs err=%s",
				c.Name, rep, len(call.Found), call.Missed, call.Extra, call.Wrong, len(call.SearchQueries),
				call.CostUSD2027, float64(call.LatencyMS)/1000, call.ErrorKind)
			calls = append(calls, call)
			if err := writeSPEvalResults(base, calls); err != nil {
				t.Fatalf("write results: %v", err)
			}
			// A miss is a recall result, compared across runs; an extra sale
			// (a trade, a resale, a sale of the other series, an opened one)
			// or a wrong end time or result date breaks the spec.
			for _, x := range call.Extra {
				t.Errorf("%s #%d: extra sale %s", c.Name, rep, x)
			}
			for _, w := range call.Wrong {
				t.Errorf("%s #%d: wrong value %s", c.Name, rep, w)
			}
		}
	}
}

// writeSPEvalResults writes the calls to base + ".json" and ".csv".
func writeSPEvalResults(base string, calls []spEvalCall) error {
	if err := os.MkdirAll(filepath.Dir(base), 0o755); err != nil {
		return err
	}
	jb, err := json.MarshalIndent(map[string]any{
		"now":   spEvalNow.Format(time.RFC3339),
		"calls": calls,
	}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(base+".json", jb, 0o644); err != nil {
		return err
	}

	f, err := os.Create(base + ".csv")
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	w := csv.NewWriter(f)
	rows := [][]string{{
		"model", "thinking", "case", "rep", "found", "missed", "extra", "wrong",
		"missed_sales", "requests", "search_queries", "search_query_count",
		"prompt_tokens", "candidates_tokens", "thinking_tokens", "tool_use_tokens",
		"cost_usd_2027", "latency_ms", "empty", "error_kind", "error",
	}}
	for _, c := range calls {
		rows = append(rows, []string{
			c.Model, c.Thinking, c.Case, strconv.Itoa(c.Rep),
			strconv.Itoa(len(c.Found)), strconv.Itoa(len(c.Missed)), strconv.Itoa(len(c.Extra)), strconv.Itoa(len(c.Wrong)),
			strings.Join(c.Missed, "; "), strconv.Itoa(c.Requests),
			strings.Join(c.SearchQueries, "; "), strconv.Itoa(len(c.SearchQueries)),
			strconv.Itoa(c.PromptTokens), strconv.Itoa(c.CandidatesTokens),
			strconv.Itoa(c.ThinkingTokens), strconv.Itoa(c.ToolUseTokens),
			strconv.FormatFloat(c.CostUSD2027, 'f', 6, 64), strconv.FormatInt(c.LatencyMS, 10),
			strconv.FormatBool(c.Empty), c.ErrorKind, c.Error,
		})
	}
	return w.WriteAll(rows)
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
