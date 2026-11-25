package gemini_test

import (
	"context"
	"strings"
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/gcp/gemini"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genai"
)

func TestVenueAreaAsker_AskVenueArea(t *testing.T) {
	t.Parallel()

	answer := func(text string) *genai.GenerateContentResponse {
		return &genai.GenerateContentResponse{Candidates: []*genai.Candidate{{
			Content: &genai.Content{Parts: []*genai.Part{{Text: text}}},
		}}}
	}
	tests := []struct {
		name    string
		reply   string
		want    *entity.VenueAreaAnswer
		wantErr bool
	}{
		{
			name:  "read the area and the zone from the JSON object",
			reply: "```json\n{\"admin_area\": \" JP-13 \", \"time_zone\": \"Asia/Tokyo\"}\n```",
			want:  &entity.VenueAreaAnswer{AdminArea: "JP-13", TimeZone: "Asia/Tokyo"},
		},
		{
			name:  "keep an empty answer empty",
			reply: `{"admin_area": "", "time_zone": ""}`,
			want:  &entity.VenueAreaAnswer{},
		},
		{name: "fail without a JSON object", reply: "I cannot tell.", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			logger, err := logging.New()
			require.NoError(t, err)
			asker, err := gemini.NewVenueAreaAsker(context.Background(), "test-key", "test-model", nil, logger)
			require.NoError(t, err)
			var prompt string
			var tools []*genai.Tool
			asker.SetGenerate(func(_ context.Context, model string, contents []*genai.Content, cfg *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error) {
				assert.Equal(t, "test-model", model)
				prompt = contents[0].Parts[0].Text
				tools = cfg.Tools
				return answer(tt.reply), nil
			})

			got, err := asker.AskVenueArea(context.Background(), "Zepp Haneda", "https://example.com/tour")

			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
			assert.True(t, strings.Contains(prompt, `"Zepp Haneda"`) && strings.Contains(prompt, "https://example.com/tour"))
			require.Len(t, tools, 1)
			assert.NotNil(t, tools[0].URLContext, "the page is read with URL context")
		})
	}
}
