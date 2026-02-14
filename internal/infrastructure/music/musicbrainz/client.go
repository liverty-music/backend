// Package musicbrainz provides a client for the MusicBrainz XML/JSON Web Service.
//
// Usage Guidelines and Constraints (based on MusicBrainz API TOS and Social Contract):
//
//  1. Rate Limiting (The "1.0s" Rule)
//     MusicBrainz enforces a strict rate limit of 1 request per second per IP address.
//     Exceeding this limit will result in a 503 Service Unavailable error and
//     potential temporary IP blocking. Implement a robust throttling mechanism
//     within your application to ensure compliance.
//
//  2. User-Agent Identification
//
// A descriptive User-Agent header is MANDATORY. It must follow the format:
// "ApplicationName/Version ( ContactEmailOrWebsite )"
// Generic User-Agents (like "Go-http-client/1.1") are frequently blocked to
// prevent anonymous scraping.
//
// 3. Commercial Usage
// While the data is licensed under CC0 (public domain) or CC BY-NC-SA, the
// public API infrastructure is provided for free for non-commercial use.
// High-traffic or commercial applications (like Liverty Music if monetized)
// are strongly encouraged to use a local "MusicBrainz Mirror" or a
// commercial data provider (e.g., MetaBrainz) to offload the public servers.
//
// 4. Data Attribution
// Although much of the data is CC0, it is requested and considered good
// practice to provide attribution to MusicBrainz and its contributors
// when displaying data or providing links to the MusicBrainz database.
//
// 5. Caching and Efficiency
// Users are expected to be good citizens of the community. Cache data
// locally whenever possible (e.g., using MBIDs as keys) to avoid redundant
// requests for static metadata. Do not perform "blanket crawls" of the database.
//
// 6. Use of MBIDs
// The MusicBrainz Identifier (MBID) is the core of the ecosystem. It is
// highly recommended to store and use MBIDs as the primary key for all
// music entities to ensure interoperability with other services like Last.fm
// and AcousticID.
//
// For more details, refer to: https://musicbrainz.org/doc/MusicBrainz_API/Ethics
package musicbrainz

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/pkg/api"
	"github.com/liverty-music/backend/pkg/throttle"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
)

const (
	baseURL   = "https://musicbrainz.org/ws/2/artist/"
	userAgent = "LivertyMusic/1.0.0 ( contact: pannpers@gmail.com )"
	// MusicBrainz rate limit is 1 request per second.
	rateLimitInterval = 1 * time.Second
)

type artistResponse struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// client implements entity.ArtistIdentityManager for specific MusicBrainz lookups.
type client struct {
	httpClient *http.Client
	baseURL    string
	throttler  *throttle.Throttler
}

// NewClient creates a new MusicBrainz client instance.
func NewClient(httpClient *http.Client) *client {
	if httpClient == nil {
		httpClient = &http.Client{
			Timeout: 10 * time.Second,
		}
	}
	return &client{
		httpClient: httpClient,
		baseURL:    baseURL,
		throttler:  throttle.New(rateLimitInterval, 100), // Buffer up to 100 requests
	}
}

// GetArtist retrieves canonical artist data using an MBID.
func (c *client) GetArtist(ctx context.Context, mbid string) (*entity.Artist, error) {
	url := fmt.Sprintf("%s%s?fmt=json", c.baseURL, mbid)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, apperr.Wrap(err, codes.Internal, "failed to create musicbrainz request")
	}

	req.Header.Set("User-Agent", userAgent)

	var resp *http.Response
	err = c.throttler.Do(ctx, func() error {
		var err error
		resp, err = c.httpClient.Do(req)
		return err
	})

	if err := api.FromHTTP(err, resp, "musicbrainz api request failed"); err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	var data artistResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, apperr.Wrap(err, codes.DataLoss, "failed to decode musicbrainz response")
	}

	return &entity.Artist{
		MBID: data.ID,
		Name: data.Name,
	}, nil
}

// SetBaseURL allows overriding the base URL used by the client. This is
// primarily intended for tests to point the client at an httptest server.
func (c *client) SetBaseURL(u string) {
	c.baseURL = u
}
