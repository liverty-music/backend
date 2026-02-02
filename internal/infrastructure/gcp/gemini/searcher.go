package gemini

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-logging/logging"
	"google.golang.org/genai"
)

// Config holds the configuration for Gemini searcher.
type Config struct {
	ProjectID   string
	Location    string
	ModelName   string
	DataStoreID string
}

const (
	systemInstruction = `You are a specialized agent for extracting concert information from search results.
	Your goal is to extract structured event data with high precision.
	You must VALIDATE that every extracted event is a musical concert performance by the specified artist.
	Answer purely based on the provided Search Tool results. DO NOT use internal knowledge.`

	promptTemplate = `
You are an agent extracting concert information for the artist "%s".
Focus on information related to the official site (%s) and the provided search results.
Find ALL future concert events (tour dates, festival appearances, etc.) happening on or after today (%s).

Constraints:
1. Itemize specific performances. Do NOT output a single summary item for a tour; instead, output individual items for each performance date and venue.
2. Do NOT infer dates or times if they are not explicitly stated.
3. Infer the time zone of the event based on the venue location or context of the website.
4. Extract ALL discovered events. De-duplication will be handled downstream.
5. If no information is found conformant to the schema, return an empty list.
6. Return the response in JSON format matching the schema: { "events": [ { "artist_name": "string", "event_name": "string", "venue": "string", "local_date": "YYYY-MM-DD", "start_time": "ISO8601 (e.g. 2026-02-14T18:30:00+09:00)", "open_time": "ISO8601", "source_url": "string" } ] }
`

	// maxOutputTokens defines the maximum length of Gemini's response.
	// 8192 tokens provides sufficient headroom for large batches of concert data
	// (e.g., a tour list with 30+ dates) in detailed JSON format.
	maxOutputTokens = int32(8192)
)

var (
	eventSchema = &genai.Schema{
		Type: genai.TypeObject,
		Properties: map[string]*genai.Schema{
			"artist_name": {Type: genai.TypeString},
			"event_name":  {Type: genai.TypeString, Description: "The exact title of the tour or event."},
			"venue":       {Type: genai.TypeString},
			"local_date":  {Type: genai.TypeString, Description: "The date of the concert in YYYY-MM-DD format (local time)."},
			"start_time":  {Type: genai.TypeString, Description: "The start time in ISO 8601 format including time zone (e.g. 2026-02-14T18:30:00+09:00). If time zone is unambiguous from context, apply it. Return null if unknown."},
			"open_time":   {Type: genai.TypeString, Description: "The door opening time in ISO 8601 format including time zone. Return null if unknown."},
			"source_url": {
				Type:        genai.TypeString,
				Description: "The specific URL where this event information was found. Prefer direct links to event details.",
			},
		},
		Required: []string{"artist_name", "event_name", "venue", "local_date", "source_url"},
	}
)

// ScrapedEvent matches the schema defined for Gemini output.
type ScrapedEvent struct {
	ArtistName string  `json:"artist_name"`
	EventName  string  `json:"event_name"`
	Venue      string  `json:"venue"`
	LocalDate  string  `json:"local_date"`
	StartTime  *string `json:"start_time"`
	OpenTime   *string `json:"open_time"`
	SourceURL  string  `json:"source_url"`
}

// EventsResponse matches the JSON output from Gemini.
type EventsResponse struct {
	Events []ScrapedEvent `json:"events"`
}

// ConcertSearcher implements entity.ConcertSearcher using Vertex AI Gemini.
// It leverages Gemini's reasoning capabilities combined with Google Search Grounding
// to discover and extract structured concert information from the web.
type ConcertSearcher struct {
	client *genai.Client
	config Config
	logger *logging.Logger
}

// NewConcertSearcher creates a new ConcertSearcher.
// The httpClient parameter allows for custom transport configuration, which is
// particularly useful for unit testing with httptest. If nil, the default transport is used.
func NewConcertSearcher(ctx context.Context, cfg Config, httpClient *http.Client, logger *logging.Logger) (*ConcertSearcher, error) {
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		Project:    cfg.ProjectID,
		Location:   cfg.Location,
		Backend:    genai.BackendVertexAI,
		HTTPClient: httpClient,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create genai client: %w", err)
	}

	return &ConcertSearcher{
		client: client,
		config: cfg,
		logger: logger,
	}, nil
}

// Search discovers new concerts for a given artist using Gemini with Grounding enabled.
// It sends a prompt to the model requesting a JSON list of upcoming events.
// Invalid dates are skipped, but invalid/null start times are preserved as nil pointers
// in the returned ScrapedConcert objects.
func (s *ConcertSearcher) Search(
	ctx context.Context,
	artist *entity.Artist,
	officialSite *entity.OfficialSite,
	from time.Time,
) ([]*entity.ScrapedConcert, error) {
	responseSchema := &genai.Schema{
		Type: genai.TypeObject,
		Properties: map[string]*genai.Schema{
			"events": {
				Type:  genai.TypeArray,
				Items: eventSchema,
			},
		},
		Required: []string{"events"},
	}

	// Tool setup for grounding
	tool := &genai.Tool{
		// NOTE: disabled because querying data store is not worked for some reasons.
		// TODO: enable it when it is worked.
		//
		// Retrieval: &genai.Retrieval{
		// 	VertexAISearch: &genai.VertexAISearch{
		// 		Datastore: s.config.DataStoreID,
		// 	},
		// },
		GoogleSearch: &genai.GoogleSearch{},
	}

	generateCfg := &genai.GenerateContentConfig{
		SystemInstruction: &genai.Content{
			Parts: []*genai.Part{{Text: systemInstruction}},
		},
		Tools:            []*genai.Tool{tool},
		Temperature:      genai.Ptr(float32(1.0)),
		MaxOutputTokens:  maxOutputTokens,
		ResponseMIMEType: "application/json",
		ResponseSchema:   responseSchema,
	}

	prompt := fmt.Sprintf(promptTemplate, artist.Name, officialSite.URL, from.Format("2006-01-02"))

	attrs := []slog.Attr{
		slog.String("model_version", s.config.ModelName),
		slog.String("artist", artist.Name),
		slog.String("official_site", officialSite.URL),
		slog.String("from", from.Format("2006-01-02")),
	}

	s.logger.Info(ctx, "start calling Gemini API to search concerts", attrs...)

	resp, err := s.client.Models.GenerateContent(ctx, s.config.ModelName, genai.Text(prompt), generateCfg)
	if err != nil {
		return nil, toAppErr(err, "failed to call Gemini API", attrs...)
	}

	usageMD := resp.UsageMetadata
	respAttrs := []slog.Attr{
		slog.String("response_id", resp.ResponseID),
		slog.Group("usage_metadata",
			slog.Int("prompt", int(usageMD.PromptTokenCount)),
			slog.Int("candidates", int(usageMD.CandidatesTokenCount)),
			slog.Int("total", int(usageMD.TotalTokenCount)),
			slog.Int("tool_use", int(usageMD.ToolUsePromptTokenCount)),
		),
	}

	if len(resp.Candidates) == 0 {
		s.logger.Info(ctx, "Gemini returned no concert candidates", append(attrs, respAttrs...)...)
		return nil, nil
	}

	candidate := resp.Candidates[0]
	groundingMD := candidate.GroundingMetadata
	var webSearchQueries []string
	if groundingMD != nil {
		webSearchQueries = groundingMD.WebSearchQueries
	}
	candidateAttrs := append(respAttrs,
		slog.String("finish_reason", string(candidate.FinishReason)),
		slog.Group("grounding_metadata",
			slog.Any("web_search_queries", webSearchQueries),
		),
	)

	parts := candidate.Content.Parts
	if len(parts) == 0 || parts[0].Text == "" {
		s.logger.Debug(ctx, "concert candidate has no parts", append(attrs, candidateAttrs...)...)
		return nil, nil
	}

	s.logger.Info(ctx, "successfully found concert candidates", append(attrs, candidateAttrs...)...)

	return s.parseEvents(ctx, parts[0].Text, from, attrs...)
}

func (s *ConcertSearcher) parseEvents(
	ctx context.Context,
	rawText string,
	from time.Time,
	attrs ...slog.Attr,
) ([]*entity.ScrapedConcert, error) {
	// Strip markdown code fences if present
	text := strings.TrimSpace(rawText)
	if strings.Contains(text, "```") {
		// Try to find content inside the block
		parts := strings.Split(text, "```")
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if strings.HasPrefix(p, "json") {
				text = strings.TrimPrefix(p, "json")
				break
			}
			if len(p) > 0 {
				// Fallback to first non-empty part if no json prefix
				text = p
			}
		}
	}
	text = strings.TrimSpace(text)

	// If empty or effectively empty after stripping, return nil
	if text == "" || text == "{}" || text == "{\"events\":[]}" {
		s.logger.Info(ctx, "Gemini response is effectively empty", append(attrs, slog.String("raw_text", rawText))...)
		return nil, nil
	}

	var eventsResp EventsResponse
	if err := json.Unmarshal([]byte(text), &eventsResp); err != nil {
		return nil, toAppErr(err, "failed to unmarshal gemini response",
			append(attrs, slog.String("text", text), slog.String("raw_text", rawText))...,
		)
	}

	// Convert to DTOs
	var discovered []*entity.ScrapedConcert
	for _, ev := range eventsResp.Events {
		date, err := time.Parse("2006-01-02", ev.LocalDate)
		if err != nil {
			s.logger.Warn(ctx, "failed to parse event date and skip", append(attrs, slog.String("date", ev.LocalDate))...)
			continue
		}

		if date.Before(from.Truncate(24 * time.Hour)) {
			s.logger.Debug(ctx, "filtered past event",
				append(attrs, slog.String("title", ev.EventName), slog.String("date", ev.LocalDate))...,
			)
			continue
		}

		var startTime *time.Time
		if ev.StartTime != nil && *ev.StartTime != "" {
			st, err := time.Parse(time.RFC3339, *ev.StartTime)
			if err != nil {
				startTimeStr := *ev.StartTime
				s.logger.Warn(ctx, "failed to parse event start time, using nil",
					append(attrs, slog.String("start_time", startTimeStr))...,
				)
			} else {
				startTime = &st
			}
		}

		var openTime *time.Time
		if ev.OpenTime != nil {
			if ot, err := time.Parse(time.RFC3339, *ev.OpenTime); err == nil {
				openTime = &ot
			}
		}

		discovered = append(discovered, &entity.ScrapedConcert{
			Title:          ev.EventName,
			VenueName:      ev.Venue,
			LocalEventDate: date,
			StartTime:      startTime,
			OpenTime:       openTime,
			SourceURL:      ev.SourceURL,
		})
	}

	s.logger.Info(ctx, "successfully parsed new concerts",
		append(attrs, slog.Int("discovered_count", len(discovered)),
			slog.Any("concerts", discovered),
		)...,
	)
	return discovered, nil
}
