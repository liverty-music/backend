// Command analyze-ab-errors categorizes the kinds of mistakes and omissions
// in 3.1-flash-lite responses by walking a run log, extracting each cell's
// returned events, and bucketing them against the ground truth fixture.
//
// Usage:
//
//	go run ./cmd/analyze-ab-errors <log-path> [temperature-filter]
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

type returnedEvent struct {
	Coord    cellCoord
	Raw      rawConcert
	Matched  *gemini.GroundTruthEvent
	FieldAcc gemini.FieldAccuracy
	FailKind string // populated when Matched == nil
}

type missedEvent struct {
	Coord cellCoord
	Event gemini.GroundTruthEvent
}

var cellRe = regexp.MustCompile(`\[(\d+)/\d+\] model=(\S+) temp=([\d.]+) thinking=(\S+) artist=(.+?) rep=(\d+)$`)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: analyze-ab-errors <log-path> [temperature] [model]")
		os.Exit(2)
	}
	var tempFilter *float32
	if len(os.Args) >= 3 && os.Args[2] != "" {
		v, err := strconv.ParseFloat(os.Args[2], 32)
		if err != nil {
			fail("bad temperature %q: %v", os.Args[2], err)
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
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1<<20), 1<<22)

	var current cellCoord
	var have bool
	var allReturned []returnedEvent
	var allMissed []missedEvent
	cellCount := 0
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
		fixByKey := map[gemini.MatchKey]gemini.GroundTruthEvent{}
		for _, f := range fixture {
			fixByKey[f.Key()] = f
		}
		matchedKeys := map[gemini.MatchKey]bool{}

		for _, rc := range pl.Concerts {
			sc := toScraped(rc)
			key := gemini.KeyForScraped(sc)
			fx, ok := fixByKey[key]
			ev := returnedEvent{Coord: current, Raw: rc}
			if ok {
				ev.Matched = &fx
				ev.FieldAcc = gemini.CompareEvent(sc, fx)
				matchedKeys[key] = true
			} else {
				ev.FailKind = classifyFalsePositive(rc, fixture)
			}
			allReturned = append(allReturned, ev)
		}
		for _, f := range fixture {
			if !matchedKeys[f.Key()] {
				allMissed = append(allMissed, missedEvent{Coord: current, Event: f})
			}
		}
		cellsByArtist[current.Artist]++
		cellCount++
		have = false
	}
	if err := scanner.Err(); err != nil {
		fail("scan: %v", err)
	}

	if tempFilter != nil {
		fmt.Printf("# Analysis (filter: temperature=%.1f)\n", *tempFilter)
	} else {
		fmt.Println("# Analysis (all temperatures)")
	}
	fmt.Printf("\n%d cells analyzed, %d total returned events, %d fixture-misses across cells\n\n", cellCount, len(allReturned), len(allMissed))

	// ---- FALSE POSITIVES ----
	var fp []returnedEvent
	for _, r := range allReturned {
		if r.Matched == nil {
			fp = append(fp, r)
		}
	}
	fmt.Printf("## False positives: %d / %d returned (%.0f%%)\n\n", len(fp), len(allReturned), 100*float64(len(fp))/float64(len(allReturned)))

	fpReasons := map[string]int{}
	for _, r := range fp {
		fpReasons[r.FailKind]++
	}
	for _, k := range sortedKeys(fpReasons) {
		fmt.Printf("  %-50s %4d (%.0f%% of FP)\n", k, fpReasons[k], 100*float64(fpReasons[k])/float64(len(fp)))
	}
	fmt.Println()

	// Sample FP titles per artist
	fmt.Println("### Recurring (title|date|venue) false positives per artist (top 8):")
	fpByArtist := map[string][]returnedEvent{}
	for _, r := range fp {
		fpByArtist[r.Coord.Artist] = append(fpByArtist[r.Coord.Artist], r)
	}
	for _, k := range []string{"UVERworld", "Vaundy", "SUPER BEAVER"} {
		group := fpByArtist[k]
		seen := map[string]int{}
		for _, r := range group {
			key := fmt.Sprintf("%s | %s | %s", r.Raw.Title, r.Raw.LocalDate, r.Raw.ListedVenueName)
			seen[key]++
		}
		fmt.Printf("\n  %s (%d FP across cells):\n", k, len(group))
		for _, key := range topByCount(seen, 8) {
			fmt.Printf("    [%3dx] %s\n", seen[key], key)
		}
	}
	fmt.Println()

	// ---- FALSE NEGATIVES ----
	fmt.Printf("## False negatives: %d misses across cells\n\n", len(allMissed))

	missByEvent := map[string]int{}
	missByCategory := map[string]int{}
	for _, m := range allMissed {
		k := fmt.Sprintf("%s — %s @ %s on %s", m.Coord.Artist, m.Event.EventName, m.Event.Venue, m.Event.LocalDate)
		missByEvent[k]++
		missByCategory[categorizeFixtureEvent(m.Event)]++
	}

	fmt.Println("By fixture-event category:")
	for _, k := range sortedKeys(missByCategory) {
		fmt.Printf("  %-50s %4d\n", k, missByCategory[k])
	}
	fmt.Println()

	type evCount struct {
		Key   string
		Count int
	}
	var allEv []evCount
	for k, v := range missByEvent {
		allEv = append(allEv, evCount{k, v})
	}
	sort.Slice(allEv, func(i, j int) bool { return allEv[i].Count > allEv[j].Count })

	fmt.Println("Events missed in EVERY cell of their artist (always-miss):")
	for _, e := range allEv {
		artist := strings.SplitN(e.Key, " — ", 2)[0]
		if e.Count >= cellsByArtist[artist] && cellsByArtist[artist] > 0 {
			fmt.Printf("  [%dx] %s\n", e.Count, e.Key)
		}
	}
	fmt.Println()

	// ---- FIELD-LEVEL ERRORS on matched events ----
	var matched []returnedEvent
	for _, r := range allReturned {
		if r.Matched != nil {
			matched = append(matched, r)
		}
	}
	fmt.Printf("## Field errors on matched events (%d matched)\n\n", len(matched))

	var venueErr, adminErr, startErr, openErr, urlErr int
	adminPatterns := map[string]int{}
	urlPatterns := map[string]int{}
	startTimePatterns := map[string]int{}
	for _, r := range matched {
		if !r.FieldAcc.Venue {
			venueErr++
		}
		if !r.FieldAcc.AdminArea {
			adminErr++
			got := "<nil>"
			if s, ok := r.Raw.AdminArea.(string); ok {
				got = s
			}
			adminPatterns[fmt.Sprintf("got=%q want=%q", got, r.Matched.AdminArea)]++
		}
		if !r.FieldAcc.StartTime {
			startErr++
			startTimePatterns[fmt.Sprintf("got=%q want=%q", r.Raw.StartTime, r.Matched.StartTime)]++
		}
		if !r.FieldAcc.OpenTime {
			openErr++
		}
		if !r.FieldAcc.SourceURL {
			urlErr++
			urlPatterns[r.Raw.SourceURL+" → "+r.Matched.SourceURL]++
		}
	}
	if len(matched) == 0 {
		fmt.Println("  (no matched events to score)")
		return
	}
	fmt.Printf("  venue mismatched:      %3d / %3d  (%.0f%%)\n", venueErr, len(matched), 100*float64(venueErr)/float64(len(matched)))
	fmt.Printf("  admin_area mismatched: %3d / %3d  (%.0f%%)\n", adminErr, len(matched), 100*float64(adminErr)/float64(len(matched)))
	fmt.Printf("  start_time mismatched: %3d / %3d  (%.0f%%)\n", startErr, len(matched), 100*float64(startErr)/float64(len(matched)))
	fmt.Printf("  open_time mismatched:  %3d / %3d  (%.0f%%)\n", openErr, len(matched), 100*float64(openErr)/float64(len(matched)))
	fmt.Printf("  source_url mismatched: %3d / %3d  (%.0f%%)\n", urlErr, len(matched), 100*float64(urlErr)/float64(len(matched)))

	fmt.Println("\nTop admin_area mismatch patterns:")
	for _, k := range topByCount(adminPatterns, 10) {
		fmt.Printf("  [%3dx] %s\n", adminPatterns[k], k)
	}
	fmt.Println("\nTop start_time mismatch patterns (showing 6):")
	for _, k := range topByCount(startTimePatterns, 6) {
		fmt.Printf("  [%3dx] %s\n", startTimePatterns[k], k)
	}
	fmt.Println("\nTop source_url mismatch patterns:")
	for _, k := range topByCount(urlPatterns, 8) {
		fmt.Printf("  [%3dx] %s\n", urlPatterns[k], k)
	}
}

// classifyFalsePositive categorizes why a returned event did not match any
// fixture entry. Checks in priority order:
//  1. date matches a fixture event but venue differs → wrong_venue
//  2. venue matches a fixture event but date differs → wrong_date
//  3. date is before evaluation_from → stale_date
//  4. title closely resembles a fixture event_name → mislabeled
//  5. otherwise → hallucinated_or_unknown
func classifyFalsePositive(rc rawConcert, fixture []gemini.GroundTruthEvent) string {
	scDate := parseDate(rc.LocalDate)
	normReturned := gemini.NormalizeVenue(rc.ListedVenueName)

	if !scDate.IsZero() && scDate.Before(parseDate("2026-05-20")) {
		return "stale_date (before evaluation_from)"
	}
	for _, f := range fixture {
		fDate := parseDate(f.LocalDate)
		if !fDate.IsZero() && !scDate.IsZero() && fDate.Equal(scDate) && normReturned != "" &&
			normReturned != gemini.NormalizeVenue(f.Venue) {
			return "right_date_wrong_venue"
		}
	}
	for _, f := range fixture {
		if normReturned != "" && normReturned == gemini.NormalizeVenue(f.Venue) {
			fDate := parseDate(f.LocalDate)
			if !fDate.IsZero() && !scDate.IsZero() && !fDate.Equal(scDate) {
				return "right_venue_wrong_date"
			}
		}
	}
	for _, f := range fixture {
		if strings.Contains(rc.Title, f.EventName) || strings.Contains(f.EventName, rc.Title) {
			return "title_overlap_no_date_venue_match"
		}
	}
	return "hallucinated_or_unknown"
}

func categorizeFixtureEvent(ev gemini.GroundTruthEvent) string {
	if ev.Visibility == "members-only" {
		return "members-only"
	}
	if ev.AdminArea == "" {
		return "overseas (admin_area empty)"
	}
	if ev.Confidence == "tentative" {
		return "tentative (e.g., 2027 dates / TBA)"
	}
	if ev.Venue == "" {
		return "venue TBA / blank"
	}
	return "domestic public, fully populated"
}

func parseDate(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t
	}
	return time.Time{}
}

func sortedKeys[V any](m map[string]V) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

func topByCount(m map[string]int, n int) []string {
	type kv struct {
		k string
		v int
	}
	var s []kv
	for k, v := range m {
		s = append(s, kv{k, v})
	}
	sort.Slice(s, func(i, j int) bool { return s[i].v > s[j].v })
	if len(s) > n {
		s = s[:n]
	}
	out := make([]string, 0, len(s))
	for _, x := range s {
		out = append(out, x.k)
	}
	return out
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
	if s, ok := rc.AdminArea.(string); ok && s != "" {
		sc.AdminArea = &s
	}
	return sc
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "ERROR: "+format+"\n", args...)
	os.Exit(1)
}
