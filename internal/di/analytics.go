package di

import (
	"fmt"

	"github.com/pannpers/go-logging/logging"

	"github.com/liverty-music/backend/internal/infrastructure/analytics/posthog"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/liverty-music/backend/pkg/config"
	"github.com/liverty-music/backend/pkg/shutdown"
)

// provideAnalyticsClient returns the PostHog analytics client of a workload,
// or a nil client when no project API key is configured (local development,
// or a workload whose PostHog settings are not deployed yet): callers then
// send no product-analytics events. The process name and release version of
// its personless events are the workload's telemetry service name and
// version. A created client is registered with the shutdown manager so queued
// events are flushed on exit.
func provideAnalyticsClient(cfg config.PostHogConfig, tel config.TelemetryConfig, logger *logging.Logger) (usecase.AnalyticsClient, error) {
	if cfg.ProjectAPIKey == "" {
		return nil, nil
	}
	client, err := posthog.New(cfg.APIHost, cfg.ProjectAPIKey, tel.ServiceName, tel.ServiceVersion, logger)
	if err != nil {
		return nil, fmt.Errorf("create posthog analytics client: %w", err)
	}
	shutdown.AddExternalPhase(client)
	return client, nil
}
