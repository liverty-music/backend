// Command analyze-missed-events digs into the "domestic public, fully populated"
// false-negative bucket from a run log to understand WHY those events are
// being missed — by tour vs festival, by date distance, by venue type.
//
// Usage:
//
//	go run ./cmd/analyze-missed-events <log-path> [temperature]
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/liverty-music/backend/internal/infrastructure/gcp/gemini"
)

type rawConcert struct {
	Title           string `json:"title"`
	ListedVenueName string `json:"listed_venue_name"`
	LocalDate       string `json:"local_date"`
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

var cellRe = regexp.MustCompile(`\[(\d+)/\d+\] model=(\S+) temp=([\d.]+) thinking=(\S+) artist=(.+?) rep=(\d+)$`)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: analyze-missed-events <log> [temperature] [model]")
		os.Exit(2)
	}
	var tempFilter *float32
	if len(os.Args) >= 3 && os.Args[2] != "" {
		v, err := strconv.ParseFloat(os.Args[2], 32)
		if err != nil {
			fmt.Fprintf(os.Stderr, "bad temperature %q: %v\n", os.Args[2], err)
			os.Exit(1)
		}
		t := float32(v)
		tempFilter = &t
	}
	var modelFilter string
	if len(os.Args) >= 4 {
		modelFilter = os.Args[3]
	}
	gt, err := gemini.LoadGroundTruth()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load: %v\n", err)
		os.Exit(1)
	}
	artistByName := map[string][]gemini.GroundTruthEvent{}
	for _, a := range gt.Artists {
		artistByName[a.Name] = a.Events
	}

	f, err := os.Open(os.Args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "open: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1<<20), 1<<22)

	var current cellCoord
	var have bool

	// (event key) → number of cells where it was missed
	missCounter := map[string]int{}
	// keep the event itself
	missEvent := map[string]gemini.GroundTruthEvent{}
	missArtist := map[string]string{}
	cellsByArtist := map[string]int{}

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
		if tempFilter != nil && current.Temperature != *tempFilter {
			have = false
			continue
		}
		if modelFilter != "" && current.Model != modelFilter {
			have = false
			continue
		}
		fixture, ok := artistByName[current.Artist]
		if !ok {
			have = false
			continue
		}
		matched := map[gemini.MatchKey]bool{}
		for _, rc := range pl.Concerts {
			scKey := gemini.MatchKey{
				LocalDate: trimDate(rc.LocalDate),
				Venue:     gemini.NormalizeVenue(rc.ListedVenueName),
			}
			matched[scKey] = true
		}
		for _, f := range fixture {
			// Only count fully-populated domestic public events
			if f.Visibility != "public" || f.AdminArea == "" || f.Confidence != "confirmed" || f.Venue == "" {
				continue
			}
			if !matched[f.Key()] {
				key := f.LocalDate + "|" + f.EventName + "|" + f.Venue
				missCounter[key]++
				missEvent[key] = f
				missArtist[key] = current.Artist
			}
		}
		cellsByArtist[current.Artist]++
		have = false
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "scan: %v\n", err)
		os.Exit(1)
	}

	type miss struct {
		Key    string
		Event  gemini.GroundTruthEvent
		Artist string
		Count  int
	}
	var all []miss
	for k, v := range missCounter {
		all = append(all, miss{k, missEvent[k], missArtist[k], v})
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].Count != all[j].Count {
			return all[i].Count > all[j].Count
		}
		return all[i].Event.LocalDate < all[j].Event.LocalDate
	})

	fmt.Printf("# Missed domestic-public-fully-populated events (T=%v)\n\n", argDesc(tempFilter))
	totalMiss := 0
	for _, m := range all {
		totalMiss += m.Count
	}
	fmt.Printf("%d distinct fixture events were missed at least once (%d miss-occurrences across cells)\n\n", len(all), totalMiss)

	// Pattern 1: tour vs single
	tourCnt := map[string]int{}
	for _, m := range all {
		tag := classifyTour(m.Event.EventName)
		tourCnt[tag] += m.Count
	}
	fmt.Println("## By event type:")
	for _, k := range sortKV(tourCnt) {
		fmt.Printf("  %-30s %4d miss-occurrences\n", k, tourCnt[k])
	}

	// Pattern 2: tour leg position (cluster)
	tourLeg := map[string]int{}
	for _, m := range all {
		if tag := tourLegFor(m.Event); tag != "" {
			tourLeg[tag] += m.Count
		}
	}
	if len(tourLeg) > 0 {
		fmt.Println("\n## By tour-leg position:")
		for _, k := range sortKV(tourLeg) {
			fmt.Printf("  %-40s %4d miss-occurrences\n", k, tourLeg[k])
		}
	}

	// Pattern 3: date distance from evaluation_from
	from, _ := time.Parse("2006-01-02", "2026-05-20")
	bucketCnt := map[string]int{}
	for _, m := range all {
		d, _ := time.Parse("2006-01-02", m.Event.LocalDate)
		if d.IsZero() {
			continue
		}
		days := int(d.Sub(from).Hours() / 24)
		bucket := dateBucket(days)
		bucketCnt[bucket] += m.Count
	}
	fmt.Println("\n## By days from evaluation_from (2026-05-20):")
	for _, k := range []string{"0-30d", "31-60d", "61-90d", "91-120d", "121-180d", "180d+"} {
		if v, ok := bucketCnt[k]; ok {
			fmt.Printf("  %-12s %4d miss-occurrences\n", k, v)
		}
	}

	// Pattern 4: artist breakdown
	artistCnt := map[string]int{}
	artistDistinct := map[string]int{}
	for _, m := range all {
		artistCnt[m.Artist] += m.Count
		artistDistinct[m.Artist]++
	}
	fmt.Println("\n## By artist:")
	for _, k := range []string{"UVERworld", "Vaundy", "SUPER BEAVER"} {
		fmt.Printf("  %-13s  distinct missed: %2d   total miss-occurrences: %3d\n",
			k, artistDistinct[k], artistCnt[k])
	}

	// Pattern 5: venue type
	venueCnt := map[string]int{}
	for _, m := range all {
		t := classifyVenue(m.Event.Venue)
		venueCnt[t] += m.Count
	}
	fmt.Println("\n## By venue type:")
	for _, k := range sortKV(venueCnt) {
		fmt.Printf("  %-40s %4d miss-occurrences\n", k, venueCnt[k])
	}

	// Always-miss list
	fmt.Println("\n## Distinct events missed in EVERY cell of their artist:")
	for _, m := range all {
		if m.Count >= cellsByArtist[m.Artist] && cellsByArtist[m.Artist] > 0 {
			fmt.Printf("  [%dx] %s — %s @ %s (%s) on %s\n",
				m.Count, m.Artist, m.Event.EventName, m.Event.Venue, m.Event.AdminArea, m.Event.LocalDate)
		}
	}
}

func argDesc(p *float32) string {
	if p == nil {
		return "all"
	}
	return fmt.Sprintf("%.1f", *p)
}

func trimDate(s string) string {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.Format("2006-01-02")
	}
	return s
}

func classifyTour(eventName string) string {
	low := strings.ToLower(eventName)
	switch {
	case strings.Contains(eventName, "DOME TOUR") || strings.Contains(eventName, "ドームツアー"):
		return "dome-tour"
	case strings.Contains(eventName, "ARENA TOUR") || strings.Contains(eventName, "アリーナツアー"):
		return "arena-tour"
	case strings.Contains(eventName, "TOUR") || strings.Contains(eventName, "ツアー"):
		return "hall-tour"
	case strings.Contains(eventName, "FESTIVAL") || strings.Contains(eventName, "フェス") ||
		strings.Contains(low, "fest"):
		return "festival"
	case strings.Contains(eventName, "ファンクラブ") || strings.Contains(eventName, "会員限定"):
		return "fanclub-only"
	default:
		return "other-single"
	}
}

func tourLegFor(ev gemini.GroundTruthEvent) string {
	// Tag by tour for SUPER BEAVER (largest set of missed events)
	if strings.Contains(ev.EventName, "ラクダの人生") {
		return "SB: 都会のラクダ TOUR 2026-2027 (hall)"
	}
	if strings.Contains(ev.EventName, "DOME TOUR") && strings.Contains(ev.EventName, "ラクダ") {
		return "SB: 都会のラクダ DOME TOUR 2026"
	}
	if strings.Contains(ev.EventName, "HORO") {
		return "Vaundy: ASIA ARENA TOUR HORO"
	}
	return ""
}

func dateBucket(days int) string {
	switch {
	case days <= 30:
		return "0-30d"
	case days <= 60:
		return "31-60d"
	case days <= 90:
		return "61-90d"
	case days <= 120:
		return "91-120d"
	case days <= 180:
		return "121-180d"
	default:
		return "180d+"
	}
}

func classifyVenue(v string) string {
	switch {
	case strings.Contains(v, "ドーム"):
		return "ドーム (Tokyo/京セラ など)"
	case strings.Contains(v, "アリーナ") || strings.Contains(v, "Arena"):
		return "アリーナ"
	case strings.Contains(v, "メッセ"):
		return "メッセ (大型会場)"
	case strings.Contains(v, "ホール"):
		return "ホール (中型会場)"
	case strings.Contains(v, "公園") || strings.Contains(v, "スポーツアイランド"):
		return "野外/公園 (フェス会場)"
	case strings.Contains(v, "会館"):
		return "会館 (公共系)"
	case strings.Contains(v, "Zepp"):
		return "Zepp 系"
	default:
		return "その他"
	}
}

func sortKV(m map[string]int) []string {
	type kv struct {
		k string
		v int
	}
	var s []kv
	for k, v := range m {
		s = append(s, kv{k, v})
	}
	sort.Slice(s, func(i, j int) bool { return s[i].v > s[j].v })
	out := make([]string, 0, len(s))
	for _, x := range s {
		out = append(out, x.k)
	}
	return out
}
