// Package google provides a client for the Google Maps Places API (New).
//
// This client uses the Places Text Search API (New) to search for venues by name.
// It authenticates via OAuth 2.0 using Application Default Credentials (ADC),
// which integrates with GKE Workload Identity for automatic token management.
//
// For more details, refer to: https://developers.google.com/maps/documentation/places/web-service/text-search
package google

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"golang.org/x/oauth2"

	"github.com/liverty-music/backend/pkg/api"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
)

const defaultBaseURL = "https://places.googleapis.com/v1/places:searchText"

// Place represents a Google Maps place (venue) result.
type Place struct {
	// PlaceID is the stable Google Maps Place ID.
	PlaceID string
	// Name is the name of the place as returned by Google Maps.
	Name string
	// Latitude is the WGS 84 latitude of the place.
	Latitude *float64
	// Longitude is the WGS 84 longitude of the place.
	Longitude *float64
}

// textSearchRequest is the JSON body for the Places Text Search (New) API.
type textSearchRequest struct {
	TextQuery string `json:"textQuery"`
}

// textSearchResponse is the JSON response from the Places Text Search (New) API.
type textSearchResponse struct {
	Places []struct {
		ID          string `json:"id"`
		DisplayName struct {
			Text string `json:"text"`
		} `json:"displayName"`
		Location struct {
			Latitude  float64 `json:"latitude"`
			Longitude float64 `json:"longitude"`
		} `json:"location"`
	} `json:"places"`
}

// Client is a Google Maps Places Text Search client.
type Client struct {
	httpClient  *http.Client
	tokenSource oauth2.TokenSource
	projectID   string
	baseURL     string
	logger      *logging.Logger
}

// NewClient creates a new Google Maps Places client using the provided OAuth2
// TokenSource for authentication. The projectID is sent in the X-Goog-User-Project
// header for billing attribution.
func NewClient(ts oauth2.TokenSource, projectID string, httpClient *http.Client, logger *logging.Logger) *Client {
	if httpClient == nil {
		httpClient = &http.Client{
			Timeout: 10 * time.Second,
		}
	}
	return &Client{
		httpClient:  httpClient,
		tokenSource: ts,
		projectID:   projectID,
		baseURL:     defaultBaseURL,
		logger:      logger.With(slog.String("component", "googlemaps")),
	}
}

// SearchPlace searches for a venue by name using the Google Maps Places Text Search (New) API.
// It returns the top match or apperr.ErrNotFound if no results are returned.
func (c *Client) SearchPlace(ctx context.Context, name, adminArea string) (*Place, error) {
	c.logger.Info(ctx, "searching place", slog.String("venueName", name), slog.String("adminArea", adminArea))

	query := name
	if adminArea != "" {
		query = fmt.Sprintf("%s %s", name, adminArea)
	}

	body, err := json.Marshal(textSearchRequest{TextQuery: query})
	if err != nil {
		return nil, apperr.Wrap(err, codes.Internal, "failed to marshal google maps request body")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL, bytes.NewReader(body))
	if err != nil {
		return nil, apperr.Wrap(err, codes.Internal, "failed to create google maps request")
	}

	token, err := c.tokenSource.Token()
	if err != nil {
		return nil, apperr.Wrap(err, codes.Unavailable, "failed to obtain oauth2 token for google maps")
	}
	req.Header.Set("Authorization", "Bearer "+token.AccessToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Goog-FieldMask", "places.id,places.displayName,places.location")
	req.Header.Set("X-Goog-User-Project", c.projectID)

	resp, err := c.httpClient.Do(req)
	if resp != nil {
		defer func() { _ = resp.Body.Close() }()
	}
	if err := api.FromHTTP(err, resp, "google maps places search failed"); err != nil {
		attrs := []slog.Attr{slog.String("venueName", name)}
		if resp != nil {
			attrs = append(attrs, slog.Int("statusCode", resp.StatusCode))
		}
		c.logger.Error(ctx, "google maps search failed", err, attrs...)
		return nil, err
	}

	var data textSearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, apperr.Wrap(err, codes.DataLoss, "failed to decode google maps response")
	}

	if len(data.Places) == 0 {
		return nil, apperr.New(codes.NotFound, "no matching place found in google maps")
	}

	p := data.Places[0]
	place := &Place{PlaceID: p.ID, Name: p.DisplayName.Text}
	if p.Location.Latitude != 0 || p.Location.Longitude != 0 {
		lat := p.Location.Latitude
		lng := p.Location.Longitude
		place.Latitude = &lat
		place.Longitude = &lng
	}
	return place, nil
}

// SetBaseURL overrides the API base URL. Intended for tests.
func (c *Client) SetBaseURL(u string) {
	c.baseURL = u
}
