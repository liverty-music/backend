// Command replay-ab-log reconstructs cell scores from a partial A/B run log.
// Used to compare 3-flash-preview vs 3.1-flash-lite when the in-progress.json
// snapshot was lost. The log file is the stdout/stderr of a `go test` run.
//
// Usage:
//
//	go run ./cmd/replay-ab-log <path-to-log>
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/gcp/gemini"
)

type rawConcert struct {
	Title           string `json:"title"`
	ListedVenueName string `json:"listed_venue_name"`
	AdminArea       any    `json:"admin_area"`
	LocalDate       string `json:"local_date"`
	StartTime       string `json:"start_time"`
	OpenTime        string `json:"open_time"`
	SourceURL       string `json:"source_url"`
}

type parsedLog struct {
	Msg      string       `json:"msg"`
	Concerts []rawConcert `json:"concerts"`
}

type cellCoord struct {
	Model       string
	Temperature float32
	Thinking    string
	Artist      string
	Rep         int
}

type cellResult struct {
	Coord        cellCoord
	Returned     int
	Matched      int
	Precision    float64
	RecallPublic float64
	RecallAll    float64
	VenueAcc     float64
	AdminAreaAcc float64
	StartTimeAcc float64
	OpenTimeAcc  float64
	SourceURLAcc float64
}

var cellRe = regexp.MustCompile(`\[(\d+)/\d+\] model=(\S+) temp=([\d.]+) thinking=(\S+) artist=(.+?) rep=(\d+)$`)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: replay-ab-log <log-path>")
		os.Exit(2)
	}
	gt, err := gemini.LoadGroundTruth()
	if err != nil {
		fail("load ground truth: %v", err)
	}
	artistByName := map[string][]gemini.GroundTruthEvent{}
	for _, a := range gt.Artists {
		artistByName[a.Name] = a.Events
	}

	f, err := os.Open(os.Args[1])
	if err != nil {
		fail("open log: %v", err)
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1<<20), 1<<22)

	var current cellCoord
	var have bool
	var results []cellResult

	for scanner.Scan() {
		line := scanner.Text()
		if m := cellRe.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
			t, _ := strconv.ParseFloat(m[3], 32)
			r, _ := strconv.Atoi(m[6])
			current = cellCoord{
				Model:       m[2],
				Temperature: float32(t),
				Thinking:    m[4],
				Artist:      strings.TrimSpace(m[5]),
				Rep:         r,
			}
			have = true
			continue
		}
		idx := strings.Index(line, `{"time":`)
		if idx < 0 || !have {
			continue
		}
		var pl parsedLog
		if err := json.Unmarshal([]byte(line[idx:]), &pl); err != nil {
			continue
		}
		if pl.Msg != "successfully parsed new concerts" {
			continue
		}
		fixture, ok := artistByName[current.Artist]
		if !ok {
			fmt.Fprintf(os.Stderr, "no fixture for %q\n", current.Artist)
			continue
		}
		results = append(results, scoreCell(current, pl.Concerts, fixture))
		have = false
	}
	if err := scanner.Err(); err != nil {
		fail("scan: %v", err)
	}

	emitSummary(results)
}

func scoreCell(coord cellCoord, raw []rawConcert, fixture []gemini.GroundTruthEvent) cellResult {
	fixByKey := make(map[gemini.MatchKey]gemini.GroundTruthEvent, len(fixture))
	for _, f := range fixture {
		fixByKey[f.Key()] = f
	}

	var matched, publicMatched int
	var venueOK, adminOK, startOK, openOK, urlOK int

	for _, rc := range raw {
		sc := toScraped(rc)
		key := gemini.KeyForScraped(sc)
		fx, ok := fixByKey[key]
		if !ok {
			continue
		}
		matched++
		if fx.Visibility != "members-only" {
			publicMatched++
		}
		acc := gemini.CompareEvent(sc, fx)
		if acc.Venue {
			venueOK++
		}
		if acc.AdminArea {
			adminOK++
		}
		if acc.StartTime {
			startOK++
		}
		if acc.OpenTime {
			openOK++
		}
		if acc.SourceURL {
			urlOK++
		}
	}

	publicTotal := 0
	for _, f := range fixture {
		if f.Visibility != "members-only" {
			publicTotal++
		}
	}

	res := cellResult{Coord: coord, Returned: len(raw), Matched: matched}
	if len(raw) > 0 {
		res.Precision = float64(matched) / float64(len(raw))
	}
	if len(fixture) > 0 {
		res.RecallAll = float64(matched) / float64(len(fixture))
	}
	if publicTotal > 0 {
		res.RecallPublic = float64(publicMatched) / float64(publicTotal)
	}
	if matched > 0 {
		d := float64(matched)
		res.VenueAcc = float64(venueOK) / d
		res.AdminAreaAcc = float64(adminOK) / d
		res.StartTimeAcc = float64(startOK) / d
		res.OpenTimeAcc = float64(openOK) / d
		res.SourceURLAcc = float64(urlOK) / d
	}
	return res
}

func toScraped(rc rawConcert) *entity.ScrapedConcert {
	sc := &entity.ScrapedConcert{
		Title:           rc.Title,
		ListedVenueName: rc.ListedVenueName,
		SourceURL:       rc.SourceURL,
	}
	if rc.LocalDate != "" {
		if t, err := time.Parse(time.RFC3339, rc.LocalDate); err == nil {
			sc.LocalDate = t
		} else if t, err := time.Parse("2006-01-02", rc.LocalDate); err == nil {
			sc.LocalDate = t
		}
	}
	if rc.StartTime != "" {
		if t, err := time.Parse(time.RFC3339, rc.StartTime); err == nil {
			sc.StartTime = t
		}
	}
	if rc.OpenTime != "" {
		if t, err := time.Parse(time.RFC3339, rc.OpenTime); err == nil {
			sc.OpenTime = t
		}
	}
	// AdminArea in log is JSON-encoded as either string or null. Coerce.
	if s, ok := rc.AdminArea.(string); ok && s != "" {
		sc.AdminArea = &s
	}
	return sc
}

func emitSummary(results []cellResult) {
	fmt.Printf("=== Replay summary: %d cells parsed ===\n\n", len(results))

	var allP, allRP, allRA float64
	for _, r := range results {
		allP += r.Precision
		allRP += r.RecallPublic
		allRA += r.RecallAll
	}
	n := float64(len(results))
	if n > 0 {
		fmt.Printf("Overall: avg precision=%.3f recall_public=%.3f recall_all=%.3f\n\n",
			allP/n, allRP/n, allRA/n)
	}

	// Group by thinking
	byThink := map[string][]cellResult{}
	for _, r := range results {
		byThink[r.Coord.Thinking] = append(byThink[r.Coord.Thinking], r)
	}
	fmt.Println("By thinking_level:")
	for _, k := range []string{"medium", "high"} {
		group, ok := byThink[k]
		if !ok || len(group) == 0 {
			continue
		}
		var p, rp, ra float64
		for _, r := range group {
			p += r.Precision
			rp += r.RecallPublic
			ra += r.RecallAll
		}
		gn := float64(len(group))
		fmt.Printf("  %-6s  cells=%2d  precision=%.3f  recall_public=%.3f  recall_all=%.3f\n",
			k, len(group), p/gn, rp/gn, ra/gn)
	}
	fmt.Println()

	// Group by temperature
	byTemp := map[float32][]cellResult{}
	for _, r := range results {
		byTemp[r.Coord.Temperature] = append(byTemp[r.Coord.Temperature], r)
	}
	fmt.Println("By temperature:")
	for _, t := range []float32{0.2, 0.5, 1.0} {
		group, ok := byTemp[t]
		if !ok || len(group) == 0 {
			continue
		}
		var p, rp float64
		for _, r := range group {
			p += r.Precision
			rp += r.RecallPublic
		}
		gn := float64(len(group))
		fmt.Printf("  T=%.1f  cells=%2d  precision=%.3f  recall_public=%.3f\n",
			t, len(group), p/gn, rp/gn)
	}
	fmt.Println()

	// Group by artist
	byArtist := map[string][]cellResult{}
	for _, r := range results {
		byArtist[r.Coord.Artist] = append(byArtist[r.Coord.Artist], r)
	}
	fmt.Println("By artist:")
	for _, k := range []string{"UVERworld", "Vaundy", "SUPER BEAVER"} {
		group, ok := byArtist[k]
		if !ok || len(group) == 0 {
			continue
		}
		var p, rp, vAcc, aAcc, sAcc, oAcc, uAcc float64
		var ret, matched float64
		for _, r := range group {
			p += r.Precision
			rp += r.RecallPublic
			ret += float64(r.Returned)
			matched += float64(r.Matched)
			vAcc += r.VenueAcc
			aAcc += r.AdminAreaAcc
			sAcc += r.StartTimeAcc
			oAcc += r.OpenTimeAcc
			uAcc += r.SourceURLAcc
		}
		gn := float64(len(group))
		fmt.Printf("  %-13s cells=%2d  precision=%.3f  recall_public=%.3f  returned=%.1f matched=%.1f | venue=%.2f date=N/A admin=%.2f start=%.2f open=%.2f url=%.2f\n",
			k, len(group), p/gn, rp/gn, ret/gn, matched/gn, vAcc/gn, aAcc/gn, sAcc/gn, oAcc/gn, uAcc/gn)
	}

	// Group by model
	byModel := map[string][]cellResult{}
	for _, r := range results {
		byModel[r.Coord.Model] = append(byModel[r.Coord.Model], r)
	}
	fmt.Println("\nBy model:")
	for _, k := range []string{"gemini-3-flash-preview", "gemini-3.1-flash-lite", "gemini-3.5-flash"} {
		group, ok := byModel[k]
		if !ok || len(group) == 0 {
			continue
		}
		var p, rp, ra, vAcc, aAcc, sAcc, oAcc, uAcc float64
		var ret, matched float64
		for _, r := range group {
			p += r.Precision
			rp += r.RecallPublic
			ra += r.RecallAll
			ret += float64(r.Returned)
			matched += float64(r.Matched)
			vAcc += r.VenueAcc
			aAcc += r.AdminAreaAcc
			sAcc += r.StartTimeAcc
			oAcc += r.OpenTimeAcc
			uAcc += r.SourceURLAcc
		}
		gn := float64(len(group))
		fmt.Printf("  %-25s cells=%2d  P=%.3f Rp=%.3f Ra=%.3f returned=%.1f matched=%.1f | venue=%.2f admin=%.2f start=%.2f open=%.2f url=%.2f\n",
			k, len(group), p/gn, rp/gn, ra/gn, ret/gn, matched/gn, vAcc/gn, aAcc/gn, sAcc/gn, oAcc/gn, uAcc/gn)
	}

	// Group by model × artist
	type maKey struct {
		Model  string
		Artist string
	}
	byMA := map[maKey][]cellResult{}
	for _, r := range results {
		byMA[maKey{r.Coord.Model, r.Coord.Artist}] = append(byMA[maKey{r.Coord.Model, r.Coord.Artist}], r)
	}
	fmt.Println("\nBy model × artist:")
	for _, m := range []string{"gemini-3-flash-preview", "gemini-3.1-flash-lite", "gemini-3.5-flash"} {
		for _, a := range []string{"UVERworld", "Vaundy", "SUPER BEAVER"} {
			group, ok := byMA[maKey{m, a}]
			if !ok || len(group) == 0 {
				continue
			}
			var p, rp, ra float64
			var ret, matched float64
			for _, r := range group {
				p += r.Precision
				rp += r.RecallPublic
				ra += r.RecallAll
				ret += float64(r.Returned)
				matched += float64(r.Matched)
			}
			gn := float64(len(group))
			fmt.Printf("  %-25s %-13s cells=%2d  P=%.3f Rp=%.3f returned=%.1f matched=%.1f\n",
				m, a, len(group), p/gn, rp/gn, ret/gn, matched/gn)
		}
	}
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "ERROR: "+format+"\n", args...)
	os.Exit(1)
}
