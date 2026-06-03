//go:build integration

package gemini_test

import (
	"context"
	"os"
	"testing"
	"time"

	genai "google.golang.org/genai"
)

// TestGemini_GroundingProbe is a minimal reproduction that isolates whether
// Google Search grounding actually fires per model. It sends a prompt that
// CANNOT be answered without a live web search and explicitly asks the model to
// search, then dumps the grounding metadata. Comparing gemini-3.1-flash-lite vs
// gemini-3.5-flash on the identical request distinguishes "the model chose not
// to search (AUTO)" from "this model/SDK never grounds at all".
//
// Run (real, billed calls):
//
//	GCP_GEMINI_SEARCH_API_KEY=$(esc env get liverty-music/dev pulumiConfig.geminiSearchApiKey --show-secrets --value string) \
//	MERCH_EVAL=1 go test -tags integration -run TestGemini_GroundingProbe -v ./internal/infrastructure/gcp/gemini/
func TestGemini_GroundingProbe(t *testing.T) {
	if os.Getenv(merchEvalEnvVar) != "1" {
		t.Skipf("disabled; set %s=1 (and -tags integration) to run real Gemini calls", merchEvalEnvVar)
	}
	apiKey := os.Getenv(merchAPIKeyEnvVar)
	if apiKey == "" {
		t.Fatalf("%s is required", merchAPIKeyEnvVar)
	}

	ctx := context.Background()
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		Backend: genai.BackendGeminiAPI,
		APIKey:  apiKey,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	// Requires fresh, post-training-cutoff web data + explicit search instruction.
	const prompt = "You MUST use Google Search. Find one news headline published in June 2026 and output the headline followed by its source URL. Do not answer from memory."

	for _, model := range []string{"gemini-3.1-flash-lite", "gemini-3.5-flash"} {
		model := model
		t.Run(model, func(t *testing.T) {
			cfg := &genai.GenerateContentConfig{
				Tools: []*genai.Tool{{GoogleSearch: &genai.GoogleSearch{}}},
			}
			reqCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
			defer cancel()

			resp, err := client.Models.GenerateContent(reqCtx, model, genai.Text(prompt), cfg)
			if err != nil {
				t.Fatalf("GenerateContent(%s): %v", model, err)
			}

			var (
				toolUse  int32
				finish   string
				queries  []string
				chunkURL []string
			)
			if u := resp.UsageMetadata; u != nil {
				toolUse = u.ToolUsePromptTokenCount
			}
			if len(resp.Candidates) > 0 {
				c := resp.Candidates[0]
				finish = string(c.FinishReason)
				if g := c.GroundingMetadata; g != nil {
					queries = g.WebSearchQueries
					for _, ch := range g.GroundingChunks {
						if ch != nil && ch.Web != nil {
							chunkURL = append(chunkURL, ch.Web.URI)
						}
					}
				}
			}

			t.Logf("model=%s finish=%s tool_use_tokens=%d web_search_queries=%v grounding_chunks=%d urls=%v",
				model, finish, toolUse, queries, len(chunkURL), chunkURL)

			// Evidence assertion: with a forcing prompt, a grounding-capable model
			// MUST run at least one web search query. A model that documents
			// "Search grounding: Supported" yet returns zero queries here is the
			// reportable discrepancy.
			if len(queries) == 0 {
				t.Errorf("model %s ran ZERO web search queries despite a forcing prompt (grounding did not fire)", model)
			}
		})
	}
}
