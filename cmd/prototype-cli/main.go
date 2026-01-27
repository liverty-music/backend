// Package main provides the prototype CLI tool for live event information extraction.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"sort"

	"time"

	"google.golang.org/genai"
)

// EventsResponse matches the desired JSON output
type EventsResponse struct {
	Events []ScrapedEvent `json:"events"`
}

type ScrapedEvent struct {
	ArtistName string `json:"artist_name"`
	EventName  string `json:"event_name"`
	Venue      string `json:"venue"`
	Date       string `json:"date"`
	StartTime  string `json:"start_time"`
	URL        string `json:"url"`
}

func main() {
	projectPtr := flag.String("project", "liverty-music-dev", "GCP Project ID")
	locationPtr := flag.String("location", "us-central1", "GCP Location for Vertex AI")
	modelPtr := flag.String("model", "gemini-2.0-flash-exp", "Gemini Model Name")
	artistPtr := flag.String("artist", "UVERworld", "Artist Name")
	// DataStore ID should ideally be passed or configured. Assuming a default or flag.
	// For "artist-site-search", let's use a hardcoded value or flag if we had one,
	// but based on previous code it was "artist-site-search".
	// The retrieval tool configuration needs the full resource path or just ID depending on SDK helper.
	// In the Go SDK for Vertex AI, we often specify the datastore.
	// Let's assume the datastore ID is "artist-site-search" and collection "default_collection".
	// However, the GenAI SDK tool config might differ slightly.
	// We will use the GoogleSearchRetrieval tool concept mapped to Vertex AI Search.
	// Note: The specific Go SDK type for Vertex AI Search grounding might be `Tool` with `Retrieval`.
	flag.Parse()

	projectID := *projectPtr
	location := *locationPtr
	modelName := *modelPtr
	artistName := *artistPtr

	ctx := context.Background()

	// --- Phase 1: Gemini Extraction with Grounding ---
	log.Printf("Starting Grounded Extraction for '%s'...", artistName)

	// Initialize GenAI Client
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		Project:  projectID,
		Location: location,
		Backend:  genai.BackendVertexAI,
	})
	if err != nil {
		log.Fatalf("Failed to create GenAI client: %v", err)
	}

	// Schema Definition
	eventSchema := &genai.Schema{
		Type: genai.TypeObject,
		Properties: map[string]*genai.Schema{
			"artist_name": {Type: genai.TypeString},
			"event_name":  {Type: genai.TypeString, Description: "ツアータイトルやイベント名を正確に記述"},
			"venue":       {Type: genai.TypeString},
			"date":        {Type: genai.TypeString, Description: "YYYY-MM-DD"},
			"start_time":  {Type: genai.TypeString, Description: "HH:MM"},
			"url":         {Type: genai.TypeString},
		},
		Required: []string{"artist_name", "event_name", "venue", "date", "start_time", "url"},
	}

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

	// Define Tool for Vertex AI Search
	// Note: We need to construct the full resource name for the data store.
	// Format: projects/{project}/locations/{location}/collections/{collection}/dataStores/{data_store_id}
	// Using hardcoded values from previous code or standard defaults.
	// Project Number was "1058199000631" in previous code. We should prefer Project ID if possible, but
	// Vertex AI Search resources often use Project Number or ID. Let's try Project ID first or
	// assume the user has the correct permissions. The previous code used projectNumber.
	// Let's stick to the previous hardcoded project number for safety if we can't look it up,
	// but cleaner to use Project ID if the SDK supports it.
	// Given the previous code explicitly used "1058199000631", we will use it here to be safe.
	projectNumber := "1058199000631"
	dataStoreRes := fmt.Sprintf("projects/%s/locations/global/collections/default_collection/dataStores/artist-site-search", projectNumber)

	tool := &genai.Tool{
		Retrieval: &genai.Retrieval{
			VertexAISearch: &genai.VertexAISearch{
				Datastore: dataStoreRes,
			},
		},
	}

	systemInstruction := "ライブ情報の抽出スペシャリスト。提供された検索結果から、正確な情報を抽出し、指定のJSON形式で返してください。"
	conf := &genai.GenerateContentConfig{
		SystemInstruction: &genai.Content{
			Parts: []*genai.Part{{Text: systemInstruction}},
		},
		Tools:            []*genai.Tool{tool},
		Temperature:      genai.Ptr(float32(0.0)),
		MaxOutputTokens:  int32(8192),
		ResponseMIMEType: "application/json",
		ResponseSchema:   responseSchema,
	}

	today := time.Now().Format("2006-01-02")
	prompt := fmt.Sprintf(`
あなたはアーティスト「%s」のライブ情報抽出エージェントです。
本日（%s）以降に開催されるライブ情報（ツアー日程、フェス出演など）を網羅的に検索し、抽出してください。

【制約事項】
1. 本日より過去の公演は除外してください。
2. 情報が重複している場合は、最も詳細な情報を優先してください。
3. "start_time" が不明な場合は空文字にしてください。
4. 情報が見つからない場合は、空のリストを返してください。
`, artistName, today)

	log.Printf("[Phase 1] Generating content with Gemini Grounding...")

	resp, err := client.Models.GenerateContent(ctx, modelName, genai.Text(prompt), conf)
	if err != nil {
		log.Fatalf("GenerateContent failed: %v", err)
	}

	if len(resp.Candidates) > 0 && len(resp.Candidates[0].Content.Parts) > 0 {
		text := resp.Candidates[0].Content.Parts[0].Text

		// Unmarshal
		var eventsResp EventsResponse
		if err := json.Unmarshal([]byte(text), &eventsResp); err != nil {
			log.Printf("Warning: JSON Unmarshal error: %v. Raw: %s", err, text)
			return
		}

		// --- Phase 2: Post-Processing (Go) ---
		log.Printf("[Phase 2] Post-processing %d events...", len(eventsResp.Events))

		validEvents := []ScrapedEvent{}
		seen := make(map[string]bool)

		currentDate, _ := time.Parse("2006-01-02", today)

		for _, ev := range eventsResp.Events {
			// 1. Filter Past Events
			if ev.Date != "" {
				eventDate, err := time.Parse("2006-01-02", ev.Date)
				if err == nil && eventDate.Before(currentDate) {
					continue // Skip past events
				}
			}

			// 2. Deduplicate
			// Key: EventName + Date + Venue
			key := fmt.Sprintf("%s|%s|%s", ev.EventName, ev.Date, ev.Venue)
			if seen[key] {
				continue
			}
			seen[key] = true

			validEvents = append(validEvents, ev)
		}

		// 3. Sort by Date
		sort.Slice(validEvents, func(i, j int) bool {
			return validEvents[i].Date < validEvents[j].Date
		})

		// Output
		finalResp := EventsResponse{Events: validEvents}
		finalJSON, _ := json.MarshalIndent(finalResp, "", "  ")
		fmt.Println(string(finalJSON))

	} else {
		log.Println("No content generated.")
	}
}
