// Package google provides a client for the Google Maps Places API.
//
// This client uses the Places Text Search API (New) to search for venues by name.
// An API key with the Places API enabled must be provided.
//
// For more details, refer to: https://developers.google.com/maps/documentation/places/web-service/text-search
package google

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/liverty-music/backend/pkg/api"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
)

const defaultBaseURL = "https://maps.googleapis.com/maps/api/place/textsearch/json"

// Place represents a Google Maps place (venue) result.
type Place struct {
	// PlaceID is the stable Google Maps Place ID.
	PlaceID string
	// Name is the name of the place as returned by Google Maps.
	Name string
}

type textSearchResponse struct {
	Results []struct {
		PlaceID string `json:"place_id"`
		Name    string `json:"name"`
	} `json:"results"`
	Status string `json:"status"`
}

// Client is a Google Maps Places Text Search client.
type Client struct {
	httpClient *http.Client
	apiKey     string
	baseURL    string
}

// NewClient creates a new Google Maps Places client using the provided API key.
func NewClient(apiKey string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{
			Timeout: 10 * time.Second,
		}
	}
	return &Client{
		httpClient: httpClient,
		apiKey:     apiKey,
		baseURL:    defaultBaseURL,
	}
}

// SearchPlace searches for a venue by name using the Google Maps Places Text Search API.
// It returns the top match or apperr.ErrNotFound if no results are returned.
func (c *Client) SearchPlace(ctx context.Context, name, adminArea string) (*Place, error) {
	query := name
	if adminArea != "" {
		query = fmt.Sprintf("%s %s", name, adminArea)
	}

	endpoint := fmt.Sprintf("%s?query=%s&key=%s", c.baseURL, url.QueryEscape(query), c.apiKey)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, apperr.Wrap(err, codes.Internal, "failed to create google maps request")
	}

	resp, err := c.httpClient.Do(req)
	if resp != nil {
		defer func() { _ = resp.Body.Close() }()
	}
	if err := api.FromHTTP(err, resp, "google maps places search failed"); err != nil {
		return nil, err
	}

	var data textSearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, apperr.Wrap(err, codes.DataLoss, "failed to decode google maps response")
	}

	// The Places API returns application-level status codes in the response body.
	switch data.Status {
	case "OK":
		// proceed
	case "ZERO_RESULTS":
		return nil, apperr.New(codes.NotFound, "no matching place found in google maps")
	default:
		return nil, apperr.New(codes.Unavailable, fmt.Sprintf("google maps api returned status: %s", data.Status))
	}

	if len(data.Results) == 0 {
		return nil, apperr.New(codes.NotFound, "no matching place found in google maps")
	}

	r := data.Results[0]
	return &Place{PlaceID: r.PlaceID, Name: r.Name}, nil
}

// SetBaseURL overrides the API base URL. Intended for tests.
func (c *Client) SetBaseURL(u string) {
	c.baseURL = u
}
