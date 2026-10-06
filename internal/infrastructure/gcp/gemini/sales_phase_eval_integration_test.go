//go:build integration

package gemini

import (
	"context"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-logging/logging"
)

// Sales-phase search evaluation harness. It runs the production
// SalesPhaseSearcher on fixtures checked against the official pages and scores
// the kept sales: found / missed / extra, and wrong values on found sales.
// Every call is a billed grounded call, so it runs only when GEMINI_SP_EVAL=1.
//
// Env:
//
//	GEMINI_SP_EVAL=1             enable
//	GCP_GEMINI_SEARCH_API_KEY    Gemini API key (dev project)
//	GEMINI_SP_EVAL_MODEL         model (default gemini-3.8-flash)
//	GEMINI_SP_EVAL_THINKING      thinking level (default low)
//	GEMINI_SP_EVAL_CASES         comma list of case names (default: all)
//	GEMINI_SP_EVAL_REPS          repetitions per case (default 3; 5 sales x 3 = 15)
//
// The search queries of each call are in the searcher's
// "gemini response metadata" log line.

// spEvalNow freezes "current time" for the prompt and the checks so every run
// is scored against the same ground truth (captured 2026-10-05 15:00 JST).
var spEvalNow = time.Date(2026, 10, 5, 15, 0, 0, 0, jst)

type spEvalExpected struct {
	SeriesID          string
	Method            entity.SalesMethod
	ApplyStartTime    string
	ApplyEndTime      string // "" when the sale has no stated end
	LotteryResultTime string
}

type spEvalCase struct {
	Name     string
	Input    *entity.SalesPhaseSearchInput
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

// spEvalCases are verified against official pages on 2026-10-05.
var spEvalCases = []spEvalCase{
	{
		Name: "kinggnu",
		Input: &entity.SalesPhaseSearchInput{
			ArtistName:      "King Gnu",
			OfficialSiteURL: "https://kinggnu.jp/",
			Series: []*entity.SalesSeriesRef{{
				SeriesID:   "01a05c6a-9e24-701e-88c6-158d7639b711",
				Title:      "King Gnu 10th Anniversary Opening Live “KICKOFF”",
				EventDates: spEvalDates("2027-02-17", "2027-02-18", "2027-03-13", "2027-03-14"),
			}},
		},
		// clubgnu.com/10th_KICKOFF/: CLUB GNU 最速先行 and 2次受付 (lottery).
		Expected: []spEvalExpected{
			{SeriesID: "01a05c6a-9e24-701e-88c6-158d7639b711", Method: entity.SalesMethodLottery,
				ApplyStartTime: "2026-10-05T18:00:00+09:00", ApplyEndTime: "2026-10-22T23:59:00+09:00",
				LotteryResultTime: "2026-11-03T15:00:00+09:00"},
			{SeriesID: "01a05c6a-9e24-701e-88c6-158d7639b711", Method: entity.SalesMethodLottery,
				ApplyStartTime: "2026-11-10T18:00:00+09:00", ApplyEndTime: "2026-11-19T23:59:00+09:00",
				LotteryResultTime: "2026-12-02T15:00:00+09:00"},
		},
	},
	{
		Name: "tuki",
		Input: &entity.SalesPhaseSearchInput{
			ArtistName:      "tuki.",
			OfficialSiteURL: "https://tuki-official.net/",
			Series: []*entity.SalesSeriesRef{
				{
					SeriesID:   "01a06ba9-ef6a-7d7d-81de-d1b11b945026",
					Title:      "初の5都市ホールツアー『秋の修学旅行〜天体観測〜』",
					EventDates: spEvalDates("2026-10-11", "2026-10-12", "2026-11-03", "2026-11-22", "2026-11-26", "2026-12-02"),
				},
				{
					SeriesID:   "01a06ba9-f4c6-7e6e-ba1f-28cac928dcde",
					Title:      "''tuki.''と''星街すいせい''の 一夜限りの対バンライブ『星降る晩餐会』",
					EventDates: spEvalDates("2026-12-20"),
				},
			},
		},
		// eplus.jp/sf/detail/4594420001-P0030001P021001: オフィシャル先着先行 on e+.
		Expected: []spEvalExpected{
			{SeriesID: "01a06ba9-f4c6-7e6e-ba1f-28cac928dcde", Method: entity.SalesMethodFirstCome,
				ApplyStartTime: "2026-10-06T19:00:00+09:00", ApplyEndTime: "2026-10-25T23:59:00+09:00"},
		},
	},
	{
		Name: "mrsgreenapple",
		Input: &entity.SalesPhaseSearchInput{
			ArtistName:      "Mrs. GREEN APPLE",
			OfficialSiteURL: "http://mrsgreenapple.com/",
			Series: []*entity.SalesSeriesRef{{
				SeriesID: "019f8cd8-774e-77ea-9047-973899a4b6a6",
				Title:    "Mrs. GREEN APPLE Ringo Jam Tour “SHADOWS”",
				EventDates: spEvalDates("2026-10-06", "2026-10-07", "2026-10-14", "2026-10-15", "2026-10-23", "2026-10-24",
					"2026-10-28", "2026-10-29", "2026-11-04", "2026-11-05", "2026-11-13", "2026-11-14", "2026-11-18",
					"2026-11-19", "2026-11-28", "2026-11-29", "2026-12-02", "2026-12-03", "2026-12-09", "2026-12-10",
					"2026-12-15", "2026-12-16", "2026-12-19", "2026-12-20", "2026-12-27", "2026-12-28"),
			}},
		},
		// Both upcoming sales are first-come "until sold out" with no stated end.
		Expected: []spEvalExpected{
			{SeriesID: "019f8cd8-774e-77ea-9047-973899a4b6a6", Method: entity.SalesMethodFirstCome,
				ApplyStartTime: "2026-10-05T18:30:00+09:00"},
			{SeriesID: "019f8cd8-774e-77ea-9047-973899a4b6a6", Method: entity.SalesMethodFirstCome,
				ApplyStartTime: "2026-10-06T14:00:00+09:00"},
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

// spEvalScore matches the returned sales to the expected ones on series,
// method and apply start time, and returns found, missed, extra and the
// wrong values on found sales.
func spEvalScore(got []*entity.SalesPhaseCandidate, expected []spEvalExpected) (found, missed, extra int, wrong []string) {
	used := make([]bool, len(got))
	for _, e := range expected {
		idx := -1
		for i, c := range got {
			if !used[i] && c.SeriesID == e.SeriesID && c.Method == e.Method && spEvalSame(e.ApplyStartTime, c.ApplyStartTime) {
				idx = i
				break
			}
		}
		if idx < 0 {
			missed++
			continue
		}
		used[idx] = true
		found++
		c := got[idx]
		if !spEvalSame(e.ApplyEndTime, c.ApplyEndTime) {
			wrong = append(wrong, e.ApplyStartTime+" apply_end="+c.ApplyEndTime.Format(time.RFC3339)+" want "+e.ApplyEndTime)
		}
		if e.LotteryResultTime != "" && !spEvalSame(e.LotteryResultTime, c.LotteryResultTime) {
			wrong = append(wrong, e.ApplyStartTime+" lottery_result="+c.LotteryResultTime.Format(time.RFC3339)+" want "+e.LotteryResultTime)
		}
	}
	for _, u := range used {
		if !u {
			extra++
		}
	}
	return found, missed, extra, wrong
}

// TestSalesPhaseSearchEval runs the production searcher on the fixtures.
// The tuki. case has two tours, and only one of them has an upcoming sale.
// Zero extra sales across the fixtures also shows that no ticket trade or
// resale on the artists' pages was returned.
//
// @spec components/entity/sales-phase/search-sales-phases "Artist with two tours"
// @spec components/entity/sales-phase/search-sales-phases "Official ticket trade"
func TestSalesPhaseSearchEval(t *testing.T) {
	if os.Getenv("GEMINI_SP_EVAL") != "1" {
		t.Skip("set GEMINI_SP_EVAL=1 to run")
	}
	apiKey := os.Getenv("GCP_GEMINI_SEARCH_API_KEY")
	if apiKey == "" {
		t.Fatal("GCP_GEMINI_SEARCH_API_KEY is required")
	}
	model := envOr("GEMINI_SP_EVAL_MODEL", "gemini-3.8-flash")
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
	s, err := NewSalesPhaseSearcher(ctx, SalesPhaseConfig{APIKey: apiKey, Model: model, Thinking: thinking}, http.DefaultClient, logger)
	if err != nil {
		t.Fatalf("searcher: %v", err)
	}
	s.now = func() time.Time { return spEvalNow }

	var totalFound, totalExpected, totalWrong, failures int
	for _, c := range spEvalCases {
		if len(caseFilter) > 0 && !caseFilter[c.Name] {
			continue
		}
		for rep := 1; rep <= reps; rep++ {
			started := time.Now()
			got, err := s.SearchSalesPhases(ctx, c.Input)
			if err != nil {
				failures++
				t.Logf("%-14s #%d error=%v", c.Name, rep, err)
				continue
			}
			found, missed, extra, wrong := spEvalScore(got, c.Expected)
			totalFound += found
			totalExpected += len(c.Expected)
			totalWrong += len(wrong) + extra
			t.Logf("%-14s #%d found=%d missed=%d extra=%d wrong=%v %.0fs",
				c.Name, rep, found, missed, extra, wrong, time.Since(started).Seconds())
		}
	}
	t.Logf("SUMMARY model=%s thinking=%s found=%d/%d wrong_or_extra=%d failures=%d",
		model, thinking, totalFound, totalExpected, totalWrong, failures)
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
