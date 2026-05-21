package gemini

import (
	"strings"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/geo"
)

// venuePunctStripper is the punctuation set the matching algorithm strips
// (per the gemini-search-ab-evaluation spec).
var venuePunctStripper = strings.NewReplacer(
	",", " ",
	".", " ",
	"・", " ",
	"–", " ",
	"-", " ",
	"(", " ",
	")", " ",
	"「", " ",
	"」", " ",
	"『", " ",
	"』", " ",
)

// NormalizeVenue lowercases, strips a fixed punctuation set, and collapses
// consecutive whitespace. Used by the A/B harness to match returned events
// against the ground truth on (date, venue) key.
func NormalizeVenue(s string) string {
	s = strings.ToLower(s)
	s = venuePunctStripper.Replace(s)
	return strings.Join(strings.Fields(s), " ")
}

// MatchKey is the primary key used to match a returned event to a fixture
// event: exact local date plus normalized venue.
type MatchKey struct {
	LocalDate string
	Venue     string
}

// Key returns the MatchKey for a fixture event.
func (e GroundTruthEvent) Key() MatchKey {
	return MatchKey{LocalDate: e.LocalDate, Venue: NormalizeVenue(e.Venue)}
}

// KeyForScraped returns the MatchKey for a model-produced event.
func KeyForScraped(sc *entity.ScrapedConcert) MatchKey {
	return MatchKey{
		LocalDate: sc.LocalDate.Format("2006-01-02"),
		Venue:     NormalizeVenue(sc.ListedVenueName),
	}
}

// FieldAccuracy is the per-field comparison result for one matched event.
type FieldAccuracy struct {
	Venue     bool `json:"venue"`
	AdminArea bool `json:"admin_area"`
	LocalDate bool `json:"local_date"`
	StartTime bool `json:"start_time"`
	OpenTime  bool `json:"open_time"`
	SourceURL bool `json:"source_url"`
}

// CompareEvent returns a FieldAccuracy describing which fields of the
// model-produced event agree with the ground truth event.
//
// Matching rules:
//   - Venue: normalized equality.
//   - AdminArea: pointer-aware. Treat "" and nil as equivalent.
//   - LocalDate: string equality (already in matching key, redundant check).
//   - StartTime / OpenTime: time.Equal (timezone-aware) when both present.
//     A zero StartTime/OpenTime in the scraped event matches an empty
//     ISO string in the fixture and vice versa.
//   - SourceURL: case-insensitive exact match.
func CompareEvent(scraped *entity.ScrapedConcert, expected GroundTruthEvent) FieldAccuracy {
	got := FieldAccuracy{
		Venue:     NormalizeVenue(scraped.ListedVenueName) == NormalizeVenue(expected.Venue),
		LocalDate: scraped.LocalDate.Format("2006-01-02") == expected.LocalDate,
	}

	// Both sides go through geo.NormalizeAdminArea so the comparison is
	// in ISO 3166-2 space (the production storage form). Fixture values are
	// written as human-readable Japanese prefecture names (e.g. "大阪府")
	// for review-ability; normalization turns them into "JP-27" before
	// comparison.
	gotAdmin := ""
	if scraped.AdminArea != nil {
		gotAdmin = *scraped.AdminArea
	}
	wantAdmin := ""
	if expected.AdminArea != "" {
		if n := geo.NormalizeAdminArea(expected.AdminArea); n != nil {
			wantAdmin = *n
		} else {
			wantAdmin = expected.AdminArea
		}
	}
	got.AdminArea = gotAdmin == wantAdmin

	got.StartTime = timesEqual(scraped.StartTime, expected.StartTime)
	got.OpenTime = timesEqual(scraped.OpenTime, expected.OpenTime)
	got.SourceURL = strings.EqualFold(scraped.SourceURL, expected.SourceURL)
	return got
}

// timesEqual treats a zero time.Time and an empty/invalid ISO string as
// equivalent. Otherwise it requires time.Equal (which is timezone-aware).
func timesEqual(got time.Time, want string) bool {
	if got.IsZero() && want == "" {
		return true
	}
	if got.IsZero() || want == "" {
		return false
	}
	parsed, err := time.Parse(time.RFC3339, want)
	if err != nil {
		return false
	}
	return got.Equal(parsed)
}

// PricingTable maps a model ID to its USD-per-1M-token prices.
// Sourced from the Google Cloud Agent Platform pricing page as of 2026-05-20.
// Thinking tokens are billed as output tokens — no separate premium for the
// Gemini 3 series.
type PricingTable map[string]Pricing

// Pricing is the standard tier price for one model.
type Pricing struct {
	InputPerM  float64 // USD per 1M input tokens (prompt + tool-use)
	OutputPerM float64 // USD per 1M output tokens (candidates + thinking)
	CachedPerM float64 // USD per 1M cached input tokens (not yet exposed by SDK)
	// SearchPerK is the USD price per 1000 GoogleSearch grounding queries,
	// billed separately from tokens by Google. Daily free tier (~1500 queries)
	// is ignored here — this is the marginal price applied to every query.
	SearchPerK float64
}

// googleSearchPerK is the published price for GoogleSearch grounding on
// Gemini API direct, paid tier ($35 per 1000 queries as of 2026-05).
const googleSearchPerK = 35.0

// DefaultPricing is the inline pricing table for models under matrix evaluation.
var DefaultPricing = PricingTable{
	"gemini-3-flash-preview": {InputPerM: 0.50, OutputPerM: 3.00, CachedPerM: 0.05, SearchPerK: googleSearchPerK},
	"gemini-3.1-flash-lite":  {InputPerM: 0.25, OutputPerM: 1.50, CachedPerM: 0.025, SearchPerK: googleSearchPerK},
	"gemini-3.5-flash":       {InputPerM: 1.50, OutputPerM: 9.00, CachedPerM: 0.15, SearchPerK: googleSearchPerK},
}

// CostUSD returns the standard-tier dollar cost for a single call.
// Thinking tokens bill as output; tool-use tokens (e.g. URLContext fetches)
// bill as input. searchQueries is the number of GoogleSearch grounding
// requests the model issued — billed separately at SearchPerK per 1000.
// Returns 0 if the model is unknown.
func (t PricingTable) CostUSD(model string, promptTokens, candidatesTokens, thinkingTokens, toolUseTokens, searchQueries int32) float64 {
	p, ok := t[model]
	if !ok {
		return 0
	}
	input := float64(promptTokens+toolUseTokens) / 1_000_000.0 * p.InputPerM
	output := float64(candidatesTokens+thinkingTokens) / 1_000_000.0 * p.OutputPerM
	search := float64(searchQueries) / 1_000.0 * p.SearchPerK
	return input + output + search
}
