package gemini

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/pannpers/go-logging/logging"
	"google.golang.org/genai"

	"github.com/liverty-music/backend/internal/entity"
)

// venueAreaPrompt asks for the first-level area code and the IANA time zone
// of one venue, read from the page that listed it.
const venueAreaPrompt = `A concert page lists a show at the venue %q.
Page: %s

Read the page and tell where the venue is:
- admin_area: the ISO 3166-2 code of the first-level subdivision the venue is in (JP-13, US-CA, KR-11), or the ISO 3166-1 code of a country or territory that has no subdivisions (HK, SG). Empty when you cannot tell.
- time_zone: the IANA time zone of the venue (Asia/Tokyo, America/Los_Angeles). Empty when you cannot tell.

Answer with one JSON object only: {"admin_area": "...", "time_zone": "..."}`

// VenueAreaAsker asks Gemini, with URL context on the page that listed a
// venue, for the venue's first-level area code and time zone. It is used
// once, by the backfill of the Venues that have neither. It implements
// [entity.VenueAreaAsker].
type VenueAreaAsker struct {
	generate generateContentFunc
	model    string
	logger   *logging.Logger
}

var _ entity.VenueAreaAsker = (*VenueAreaAsker)(nil)

// NewVenueAreaAsker creates a VenueAreaAsker on the Gemini API direct
// backend. It fails when apiKey or model is empty.
func NewVenueAreaAsker(ctx context.Context, apiKey, model string, httpClient *http.Client, logger *logging.Logger) (*VenueAreaAsker, error) {
	if apiKey == "" || model == "" {
		return nil, fmt.Errorf("gemini.NewVenueAreaAsker: the API key and the model are required")
	}
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		HTTPClient: httpClient,
		Backend:    genai.BackendGeminiAPI,
		APIKey:     apiKey,
	})
	if err != nil {
		return nil, fmt.Errorf("create genai client for VenueAreaAsker: %w", err)
	}
	return &VenueAreaAsker{generate: client.Models.GenerateContent, model: model, logger: logger}, nil
}

// AskVenueArea implements [entity.VenueAreaAsker].
func (a *VenueAreaAsker) AskVenueArea(ctx context.Context, venueName, sourceURL string) (*entity.VenueAreaAnswer, error) {
	cfg := &genai.GenerateContentConfig{
		Tools:          []*genai.Tool{{URLContext: &genai.URLContext{}}},
		ThinkingConfig: &genai.ThinkingConfig{ThinkingLevel: genai.ThinkingLevelLow},
	}
	resp, err := a.generate(ctx, a.model, genai.Text(fmt.Sprintf(venueAreaPrompt, venueName, sourceURL)), cfg)
	if err != nil {
		return nil, toAppErr(err, "ask gemini for a venue's area")
	}
	text := strings.TrimSpace(resp.Text())
	start, end := strings.Index(text, "{"), strings.LastIndex(text, "}")
	if start < 0 || end < start {
		return nil, fmt.Errorf("gemini answered without a JSON object: %q", text)
	}
	var answer struct {
		AdminArea string `json:"admin_area"`
		TimeZone  string `json:"time_zone"`
	}
	if err := json.Unmarshal([]byte(text[start:end+1]), &answer); err != nil {
		return nil, fmt.Errorf("parse gemini's venue area answer: %w", err)
	}
	return &entity.VenueAreaAnswer{
		AdminArea: strings.TrimSpace(answer.AdminArea),
		TimeZone:  strings.TrimSpace(answer.TimeZone),
	}, nil
}

// SetGenerate replaces the Gemini call, for tests.
func (a *VenueAreaAsker) SetGenerate(fn func(context.Context, string, []*genai.Content, *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error)) {
	a.generate = fn
}
