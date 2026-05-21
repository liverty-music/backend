package gemini

import (
	_ "embed"
	"encoding/json"
	"fmt"
)

//go:embed testdata/ab_ground_truth.json
var groundTruthJSON []byte

// GroundTruth is the top-level fixture structure for the A/B harness.
type GroundTruth struct {
	EvaluationFrom string              `json:"evaluation_from"`
	CapturedAt     string              `json:"captured_at"`
	Artists        []GroundTruthArtist `json:"artists"`
}

// GroundTruthArtist is one artist's expected concert set as of EvaluationFrom.
type GroundTruthArtist struct {
	ID              string             `json:"id"`
	Name            string             `json:"name"`
	OfficialSiteURL string             `json:"official_site_url"`
	Events          []GroundTruthEvent `json:"events"`
}

// GroundTruthEvent is one expected concert. AdminArea == "" is the canonical
// correct value for venues outside Japan.
type GroundTruthEvent struct {
	EventName string `json:"event_name"`
	Venue     string `json:"venue"`
	AdminArea string `json:"admin_area"`
	LocalDate string `json:"local_date"`
	OpenTime  string `json:"open_time"`
	StartTime string `json:"start_time"`
	SourceURL string `json:"source_url"`
	// Confidence is the human-annotation certainty for this fixture entry.
	Confidence string `json:"confidence"`
	// Visibility is "public" or "members-only" — used to split recall metrics.
	Visibility string `json:"visibility"`
	// ExcludedPerSpec marks events that are intentionally OUT OF SCOPE for the
	// current product spec (e.g. multi-artist music festivals with 10+ acts).
	// They are kept in the fixture as negative samples: returning one is a
	// "festival_leak" false positive, and they are NOT counted in the recall
	// denominator.
	ExcludedPerSpec bool `json:"excluded_per_spec,omitempty"`
}

// LoadGroundTruth parses the embedded A/B fixture. Callers may freely mutate
// the returned value — every call unmarshals afresh.
func LoadGroundTruth() (*GroundTruth, error) {
	var gt GroundTruth
	if err := json.Unmarshal(groundTruthJSON, &gt); err != nil {
		return nil, fmt.Errorf("parse ground truth fixture: %w", err)
	}
	return &gt, nil
}
