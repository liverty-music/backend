// Command analyze-grounding inspects the WebSearchQueries / GroundingChunkURLs
// captured in per-cell raw response files to understand what each model is
// actually searching for and citing.
//
// Usage:
//
//	go run ./cmd/analyze-grounding <raw-dir> [<raw-dir> ...]
package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type cellRaw struct {
	Model              string   `json:"model"`
	ArtistName         string   `json:"artist_name"`
	Repetition         int      `json:"repetition"`
	WebSearchQueries   []string `json:"web_search_queries"`
	GroundingChunkURLs []string `json:"grounding_chunk_urls"`
	RenderedPartsCount int      `json:"rendered_parts_count"`
	ParsedConcerts     []any    `json:"parsed_concerts"`
	Error              string   `json:"error"`
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: analyze-grounding <raw-dir> [<raw-dir> ...]")
		os.Exit(2)
	}

	type cellKey struct {
		Model  string
		Artist string
		Rep    int
	}
	all := []cellRaw{}

	for _, dir := range os.Args[1:] {
		matches, err := filepath.Glob(filepath.Join(dir, "cell_*.json"))
		if err != nil {
			fmt.Fprintf(os.Stderr, "glob %s: %v\n", dir, err)
			continue
		}
		for _, p := range matches {
			b, err := os.ReadFile(p)
			if err != nil {
				continue
			}
			var c cellRaw
			if err := json.Unmarshal(b, &c); err != nil {
				continue
			}
			all = append(all, c)
		}
	}
	_ = cellKey{}
	if len(all) == 0 {
		fmt.Println("no cells found")
		return
	}
	fmt.Printf("Loaded %d cells\n\n", len(all))

	// ===== By model: query counts, URL counts, rendered_parts =====
	type modelStats struct {
		cells           int
		queriesTotal    int
		uniqueQueries   map[string]int
		urlsTotal       int
		uniqueURLs      map[string]int
		urlDomainCounts map[string]int
		renderedTotal   int
		emptyResponses  int
	}
	byModel := map[string]*modelStats{}
	for _, c := range all {
		s := byModel[c.Model]
		if s == nil {
			s = &modelStats{
				uniqueQueries:   map[string]int{},
				uniqueURLs:      map[string]int{},
				urlDomainCounts: map[string]int{},
			}
			byModel[c.Model] = s
		}
		s.cells++
		s.queriesTotal += len(c.WebSearchQueries)
		s.urlsTotal += len(c.GroundingChunkURLs)
		s.renderedTotal += c.RenderedPartsCount
		if len(c.ParsedConcerts) == 0 && c.Error == "" {
			s.emptyResponses++
		}
		for _, q := range c.WebSearchQueries {
			s.uniqueQueries[q]++
		}
		for _, u := range c.GroundingChunkURLs {
			s.uniqueURLs[u]++
			s.urlDomainCounts[domainOf(u)]++
		}
	}

	fmt.Println("## Grounding activity by model")
	fmt.Printf("%-25s  cells  queries(avg)  unique_q  urls(avg)  unique_u  rendered  empty\n", "model")
	for _, m := range sortedKeys(byModel) {
		s := byModel[m]
		fmt.Printf("%-25s  %5d  %12.1f  %8d  %9.1f  %8d  %8d  %5d\n",
			m, s.cells, float64(s.queriesTotal)/float64(s.cells), len(s.uniqueQueries),
			float64(s.urlsTotal)/float64(s.cells), len(s.uniqueURLs),
			s.renderedTotal, s.emptyResponses)
	}
	fmt.Println()

	// ===== Top search queries by model =====
	fmt.Println("## Top web search queries (by occurrence)")
	for _, m := range sortedKeys(byModel) {
		s := byModel[m]
		fmt.Printf("\n### %s\n", m)
		for _, k := range topByCount(s.uniqueQueries, 10) {
			fmt.Printf("  [%2dx] %s\n", s.uniqueQueries[k], k)
		}
	}
	fmt.Println()

	// ===== URL domain breakdown =====
	fmt.Println("\n## Grounding-chunk URL domains by model")
	for _, m := range sortedKeys(byModel) {
		s := byModel[m]
		if len(s.urlDomainCounts) == 0 {
			continue
		}
		fmt.Printf("\n### %s (%d total URLs across %d cells)\n", m, s.urlsTotal, s.cells)
		for _, k := range topByCount(s.urlDomainCounts, 12) {
			fmt.Printf("  [%3dx] %s\n", s.urlDomainCounts[k], k)
		}
	}
	fmt.Println()

	// ===== Vertex AI grounding-redirect URLs =====
	redirectCount := map[string]int{}
	for _, c := range all {
		for _, u := range c.GroundingChunkURLs {
			if strings.Contains(u, "vertexaisearch.cloud.google.com") || strings.Contains(u, "grounding-api-redirect") {
				redirectCount[c.Model]++
			}
		}
	}
	if len(redirectCount) > 0 {
		fmt.Println("## Vertex AI grounding redirect URLs (would need server-side resolve)")
		for _, m := range sortedKeys(redirectCount) {
			s := byModel[m]
			fmt.Printf("  %-25s %4d / %4d total URLs (%.0f%%)\n",
				m, redirectCount[m], s.urlsTotal, 100*float64(redirectCount[m])/float64(s.urlsTotal))
		}
		fmt.Println()
	}

	// ===== Empty queries / URLs nesting =====
	fmt.Println("## Cells with grounding anomalies")
	var noQueries, noURLs, noChunksButHasParsed int
	for _, c := range all {
		if len(c.WebSearchQueries) == 0 {
			noQueries++
		}
		if len(c.GroundingChunkURLs) == 0 {
			noURLs++
			if len(c.ParsedConcerts) > 0 {
				noChunksButHasParsed++
			}
		}
	}
	fmt.Printf("  cells with no web_search_queries:                 %3d / %3d\n", noQueries, len(all))
	fmt.Printf("  cells with no grounding_chunk_urls:               %3d / %3d\n", noURLs, len(all))
	fmt.Printf("  ↳ of which still returned ≥1 parsed concert:      %3d  (sourced from where?)\n", noChunksButHasParsed)
	fmt.Println()

	// ===== Per-cell summary =====
	fmt.Println("## Per-cell grounding output")
	sort.Slice(all, func(i, j int) bool {
		if all[i].Model != all[j].Model {
			return all[i].Model < all[j].Model
		}
		if all[i].ArtistName != all[j].ArtistName {
			return all[i].ArtistName < all[j].ArtistName
		}
		return all[i].Repetition < all[j].Repetition
	})
	for _, c := range all {
		fmt.Printf("  %-25s %-14s rep=%d   q=%2d  urls=%2d  rp=%2d  concerts=%2d\n",
			c.Model, c.ArtistName, c.Repetition,
			len(c.WebSearchQueries), len(c.GroundingChunkURLs), c.RenderedPartsCount,
			len(c.ParsedConcerts))
	}
}

func domainOf(u string) string {
	parsed, err := url.Parse(u)
	if err != nil || parsed.Host == "" {
		return "(unparseable)"
	}
	return parsed.Host
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

// keep regexp import used if needed later
var _ = regexp.MustCompile
