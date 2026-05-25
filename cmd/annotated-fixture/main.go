// Command annotated-fixture prints the full fixture as a table, annotating
// each event with whether 3.1-flash-lite returned it correctly in the runs
// referenced by the given raw-response directories. False-positive events
// (returned by lite on the right date but with wrong venue) are matched
// back to their fixture row when possible.
//
// Usage:
//
//	go run ./cmd/annotated-fixture <raw-dir> [<raw-dir> ...]
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/gcp/gemini"
)

const targetModel = "gemini-3.1-flash-lite"

type rawConcert struct {
	Title           string `json:"title"`
	ListedVenueName string `json:"listed_venue_name"`
	AdminArea       any    `json:"admin_area"`
	LocalDate       string `json:"local_date"`
	StartTime       string `json:"start_time"`
	OpenTime        string `json:"open_time"`
	SourceURL       string `json:"source_url"`
}

type cellRaw struct {
	Model          string       `json:"model"`
	ArtistName     string       `json:"artist_name"`
	Repetition     int          `json:"repetition"`
	Error          string       `json:"error"`
	ParsedConcerts []rawConcert `json:"parsed_concerts"`
}

type seenEvent struct {
	Cell    string
	Concert rawConcert
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: annotated-fixture <raw-dir> [<raw-dir> ...]")
		os.Exit(2)
	}

	gt, err := gemini.LoadGroundTruth()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load fixture: %v\n", err)
		os.Exit(1)
	}

	// Collect all lite returned events keyed by (artist, date) so we can
	// look up both exact matches and "right date wrong venue" cases.
	byArtistDate := map[string][]seenEvent{} // key: artistName|YYYY-MM-DD
	matchedKeys := map[string]int{}          // key: artistName|MatchKey -> rep count
	totalLiteCells := map[string]int{}

	for _, dir := range os.Args[1:] {
		files, _ := filepath.Glob(filepath.Join(dir, "cell_*.json"))
		for _, p := range files {
			b, err := os.ReadFile(p)
			if err != nil {
				continue
			}
			var c cellRaw
			if err := json.Unmarshal(b, &c); err != nil {
				continue
			}
			if c.Model != targetModel {
				continue
			}
			totalLiteCells[c.ArtistName]++
			cellLabel := fmt.Sprintf("%s-rep%d", c.ArtistName, c.Repetition)
			for _, rc := range c.ParsedConcerts {
				date := trimDate(rc.LocalDate)
				k := c.ArtistName + "|" + date
				byArtistDate[k] = append(byArtistDate[k], seenEvent{Cell: cellLabel, Concert: rc})

				sc := toScraped(rc)
				key := gemini.KeyForScraped(sc)
				matchedKeys[c.ArtistName+"|"+date+"|"+key.Venue]++
			}
		}
	}

	for _, a := range gt.Artists {
		fmt.Printf("\n## %s (%d events in fixture, lite cells = %d)\n\n",
			a.Name, len(a.Events), totalLiteCells[a.Name])

		sort.Slice(a.Events, func(i, j int) bool {
			return a.Events[i].LocalDate < a.Events[j].LocalDate
		})

		// Header
		fmt.Println("| # | date | event_name | venue | admin | open | start | source | conf | vis | 3.1-lite 状態 |")
		fmt.Println("|---|---|---|---|---|---|---|---|---|---|---|")

		for i, ev := range a.Events {
			// Compute status
			status := determineStatus(a.Name, ev, byArtistDate, matchedKeys, totalLiteCells[a.Name])

			// Truncate / format fields
			row := fmt.Sprintf("| %d | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s |",
				i+1,
				ev.LocalDate,
				short(ev.EventName, 32),
				short(ev.Venue, 28),
				ev.AdminArea,
				short(timeOnly(ev.OpenTime), 5),
				short(timeOnly(ev.StartTime), 5),
				short(lastURLPart(ev.SourceURL), 24),
				ev.Confidence,
				ev.Visibility,
				status,
			)
			fmt.Println(row)
		}
	}
}

func determineStatus(
	artist string,
	ev gemini.GroundTruthEvent,
	byArtistDate map[string][]seenEvent,
	matchedKeys map[string]int,
	totalCells int,
) string {
	if totalCells == 0 {
		return "(N/A)"
	}
	fixtureKey := ev.Key()
	matchKey := artist + "|" + ev.LocalDate + "|" + fixtureKey.Venue
	matchedReps := matchedKeys[matchKey]

	// On the same date, check what lite returned
	dateKey := artist + "|" + ev.LocalDate
	sameDateReturns := byArtistDate[dateKey]

	if matchedReps > 0 {
		// Full match. Check field-level issues by inspecting one such match.
		var first *rawConcert
		for _, se := range sameDateReturns {
			sc := toScraped(se.Concert)
			if gemini.KeyForScraped(sc).Venue == fixtureKey.Venue {
				c := se.Concert
				first = &c
				break
			}
		}
		if first == nil {
			return fmt.Sprintf("✅ 取得 (%d/%d cell)", matchedReps, totalCells)
		}
		acc := gemini.CompareEvent(toScraped(*first), ev)
		issues := []string{}
		if !acc.AdminArea {
			issues = append(issues, "admin_area")
		}
		if !acc.StartTime {
			issues = append(issues, "start_time")
		}
		if !acc.OpenTime {
			issues = append(issues, "open_time")
		}
		if !acc.SourceURL {
			gotURL := first.SourceURL
			wantURL := ev.SourceURL
			if gotURL == "" {
				gotURL = "<empty>"
			}
			issues = append(issues, fmt.Sprintf("source_url(got=%s)", lastURLPart(gotURL)))
			_ = wantURL
		}
		if len(issues) == 0 {
			return fmt.Sprintf("✅ 完全一致 (%d/%d cell)", matchedReps, totalCells)
		}
		return fmt.Sprintf("⚠ %d/%d cell 取得、field mismatch: %s", matchedReps, totalCells, strings.Join(issues, ", "))
	}

	// Not matched on this fixture row. Check if anything was returned on same date.
	if len(sameDateReturns) > 0 {
		// venue 違いで返された (FP-style) ケース
		got := sameDateReturns[0].Concert.ListedVenueName
		if got == "" {
			got = "<empty>"
		}
		return fmt.Sprintf("⚠ 誤情報: venue=%s", short(got, 20))
	}

	// No mention on this date at all.
	if ev.Visibility == "members-only" {
		return "—— 欠落 (members-only, grounding 不可)"
	}
	if ev.Confidence == "tentative" {
		return "—— 欠落 (tentative date)"
	}
	if ev.AdminArea == "" {
		return "—— 欠落 (海外公演)"
	}
	if ev.LocalDate >= "2027-01-01" {
		return "—— 欠落 (1 年以上先)"
	}
	return "—— 欠落"
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

func trimDate(s string) string {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.Format("2006-01-02")
	}
	return s
}

func timeOnly(s string) string {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.Format("15:04")
	}
	return ""
}

func lastURLPart(s string) string {
	if s == "" {
		return "—"
	}
	parts := strings.Split(s, "/")
	if len(parts) > 0 && parts[len(parts)-1] != "" {
		return ".../" + parts[len(parts)-1]
	}
	return s
}

func short(s string, n int) string {
	if len(s) <= n {
		return s
	}
	// Truncate at rune boundary
	rs := []rune(s)
	if len(rs) > n {
		return string(rs[:n]) + "…"
	}
	return s
}
