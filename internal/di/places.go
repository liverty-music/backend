package di

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/pannpers/go-logging/logging"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"golang.org/x/oauth2/google"

	"github.com/liverty-music/backend/internal/entity"
	googlemaps "github.com/liverty-music/backend/internal/infrastructure/maps/google"
	"github.com/liverty-music/backend/pkg/httpx"
)

// newPlaceSearcher returns the Google Maps Places API venue searcher of
// projectID, which CreateFromDiscovered requires. It authenticates with ADC
// (Workload Identity in GKE).
func newPlaceSearcher(ctx context.Context, projectID string, logger *logging.Logger) (entity.VenuePlaceSearcher, error) {
	if projectID == "" {
		return nil, fmt.Errorf("GCP project ID is required for Google Maps Places API")
	}
	tokenSource, err := google.DefaultTokenSource(ctx, "https://www.googleapis.com/auth/cloud-platform")
	if err != nil {
		return nil, fmt.Errorf("obtain google maps token source: %w", err)
	}
	httpClient := &http.Client{
		Transport: otelhttp.NewTransport(httpx.NewRetryTransport(nil)),
		Timeout:   10 * time.Second,
	}
	return googlemaps.NewPlaceSearcher(googlemaps.NewClient(tokenSource, projectID, httpClient, logger)), nil
}
