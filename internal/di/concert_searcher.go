package di

import (
	"context"
	"net/http"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/gcp/gemini"
	"github.com/liverty-music/backend/pkg/config"
	"github.com/pannpers/go-logging/logging"
)

// provideConcertSearcher builds the Gemini concert searcher, or returns nil
// without GCP_GEMINI_SEARCH_API_KEY, which only local development may run
// without. reporter receives one report per request the searcher sends to
// the model; it may be nil. areas resolves the admin areas the model returns.
func provideConcertSearcher(ctx context.Context, cfg config.GCPConfig, reporter gemini.GenerationReporter, httpClient *http.Client, areas entity.AreaRepository, logger *logging.Logger) (entity.ConcertSearcher, error) {
	if cfg.GeminiSearchAPIKey == "" {
		return nil, nil
	}
	return gemini.NewConcertSearcher(ctx, gemini.Config{
		APIKey:          cfg.GeminiSearchAPIKey,
		Model:           cfg.ConcertSearchModel(),
		ThinkingLevel:   cfg.ConcertSearchThinkingLevel(),
		OmitTemperature: true,

		IncludeServerSideToolInvocations: true,
		Reporter:                         reporter,
	}, httpClient, gemini.NewOfficialPageClient(), areas, logger)
}
