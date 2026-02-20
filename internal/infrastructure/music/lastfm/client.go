// Package lastfm provides a client for the Last.fm API.
//
// Usage Guidelines and Constraints (from Official Terms of Service):
//
//  1. Attribution and Branding
//     Any data retrieved from the Last.fm API (Artist names, Biographies, Images, etc.)
//     must be accompanied by a clear credit to Last.fm. You must include a link
//     back to the Last.fm website (last.fm) within the application UI.
//
//  2. Commercial and Research Usage
//     The API is primarily intended for non-commercial use. If Liverty Music is
//     used for commercial purposes (e.g., advertising, subscriptions) or for
//     academic research, you must contact partners@last.fm for prior approval
//     before deployment.
//
//  3. Data Retention and Caching
//     Permanent storage of Last.fm data to create a standalone database that
//     substitutes for Last.fm's own services is strictly prohibited.
//     Temporary caching is permitted for performance optimization, but a
//     reasonable Time-To-Live (TTL) must be implemented to ensure data freshness.
//
//  4. Rate Limiting
//     While no specific limit is quantified in the TOS, "reasonable usage" is
//     required. A common best practice is to limit requests to approximately
//     1 request per second. Excessive polling may lead to API key suspension.
//
//  5. User-Agent Header
//     It is recommended to set a descriptive User-Agent header (e.g., "LivertyMusic/1.0.0")
//     to identify your application. This helps Last.fm contact the developer
//     in case of technical issues and prevents broad IP blocking.
//
//  6. Prohibited Activities
//     - Audio Streaming: The API must not be used to stream music files. It is
//     strictly for metadata and discovery.
//     - Sublicensing: You may not redistribute or sublicense the data to third parties.
//     - Impersonation: Do not use or spoof API keys belonging to other applications.
//
// For more details, refer to: https://www.last.fm/api/tos
package lastfm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/pkg/api"
	"github.com/liverty-music/backend/pkg/throttle"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
)

// Response structures for Last.fm API

type artist struct {
	Name string `json:"name"`
	MBID string `json:"mbid"`
}

type artistSearchResponse struct {
	Results struct {
		ArtistMatches struct {
			Artist []artist `json:"artist"`
		} `json:"artistmatches"`
	} `json:"results"`
}

type similarArtistsResponse struct {
	SimilarArtists struct {
		Artist []artist `json:"artist"`
	} `json:"similarartists"`
}

type topArtistsResponse struct {
	Artists *struct {
		Artist []artist `json:"artist"`
	} `json:"artists,omitempty"`
	TopArtists *struct {
		Artist []artist `json:"artist"`
	} `json:"topartists,omitempty"`
}

type errorResponse struct {
	ErrorCode int    `json:"error"`
	Message   string `json:"message"`
}

const (
	baseURL = "https://ws.audioscrobbler.com/2.0/"
	// Last.fm rate limit is 5 requests per second.
	// We use 200ms interval to comply.
	rateLimitInterval = 200 * time.Millisecond
)

// client implements entity.ArtistSearcher using Last.fm API.
type client struct {
	apiKey     string
	httpClient *http.Client
	baseURL    string
	throttler  *throttle.Throttler
}

// NewClient creates a new Last.fm client.
func NewClient(apiKey string, httpClient *http.Client) *client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &client{
		apiKey:     apiKey,
		httpClient: httpClient,
		baseURL:    baseURL,
		throttler:  throttle.New(rateLimitInterval, 100), // Buffer up to 100 requests
	}
}

// Search finds artists by name using the Last.fm artist.search method.
func (c *client) Search(ctx context.Context, query string) ([]*entity.Artist, error) {
	params := url.Values{}
	params.Set("method", "artist.search")
	params.Set("artist", query)

	var resp artistSearchResponse

	if err := c.get(ctx, params, &resp); err != nil {
		return nil, err
	}

	artists := make([]*entity.Artist, 0, len(resp.Results.ArtistMatches.Artist))
	for _, a := range resp.Results.ArtistMatches.Artist {
		artists = append(artists, entity.NewArtist(a.Name, a.MBID))
	}

	return artists, nil
}

// ListSimilar identifies artists with musical affinity using the Last.fm artist.getsimilar method.
func (c *client) ListSimilar(ctx context.Context, artist *entity.Artist) ([]*entity.Artist, error) {
	params := url.Values{}
	params.Set("method", "artist.getsimilar")
	if artist.MBID != "" {
		params.Set("mbid", artist.MBID)
	} else {
		params.Set("artist", artist.Name)
	}

	var resp similarArtistsResponse

	if err := c.get(ctx, params, &resp); err != nil {
		return nil, err
	}

	artists := make([]*entity.Artist, 0, len(resp.SimilarArtists.Artist))
	for _, a := range resp.SimilarArtists.Artist {
		artists = append(artists, entity.NewArtist(a.Name, a.MBID))
	}

	return artists, nil
}

// ListTop identifies trending artists using Last.fm chart.gettopartists or geo.gettopartists methods.
func (c *client) ListTop(ctx context.Context, country string) ([]*entity.Artist, error) {
	params := url.Values{}
	if country != "" {
		params.Set("method", "geo.gettopartists")
		params.Set("country", country)
	} else {
		params.Set("method", "chart.gettopartists")
	}

	var resp topArtistsResponse

	if err := c.get(ctx, params, &resp); err != nil {
		return nil, err
	}

	var apiArtists []artist
	if resp.TopArtists != nil {
		apiArtists = resp.TopArtists.Artist
	} else if resp.Artists != nil {
		apiArtists = resp.Artists.Artist
	}

	artists := make([]*entity.Artist, 0, len(apiArtists))
	for _, a := range apiArtists {
		artists = append(artists, entity.NewArtist(a.Name, a.MBID))
	}

	return artists, nil
}

func (c *client) get(ctx context.Context, params url.Values, result interface{}) error {
	params.Set("api_key", c.apiKey)
	params.Set("format", "json")

	u, _ := url.Parse(c.baseURL)
	u.RawQuery = params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return apperr.Wrap(err, codes.Internal, "failed to create lastfm request")
	}

	var resp *http.Response
	err = c.throttler.Do(ctx, func() error {
		var err error
		resp, err = c.httpClient.Do(req)
		return err
	})

	if err := api.FromHTTP(err, resp, "lastfm api request failed"); err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	// Limit response body to 1MB to prevent OOM attacks
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return apperr.Wrap(err, codes.DataLoss, "failed to read lastfm response body")
	}

	// Last.fm can return HTTP 200 with an error object
	var errResp errorResponse
	if err := json.Unmarshal(body, &errResp); err == nil && errResp.ErrorCode != 0 {
		return apperr.New(mapLastFMCode(errResp.ErrorCode), errResp.Message)
	}

	if err := json.Unmarshal(body, result); err != nil {
		return apperr.Wrap(err, codes.DataLoss, "failed to decode lastfm response")
	}

	return nil
}

func mapLastFMCode(code int) codes.Code {
	switch code {
	case 6: // Invalid parameters (often used for not found)
		return codes.NotFound
	case 4, 9, 10, 14, 15:
		return codes.PermissionDenied
	case 11, 16:
		return codes.Unavailable
	case 29:
		return codes.ResourceExhausted
	default:
		return codes.Internal
	}
}

// SetBaseURL allows overriding the base URL used by the client. This is
// primarily intended for tests to point the client at an httptest server.
func (c *client) SetBaseURL(u string) {
	c.baseURL = u
}

// Close stops the background throttler goroutine and releases resources.
// It should be called when the client is no longer needed.
func (c *client) Close() error {
	if c.throttler != nil {
		c.throttler.Close()
	}
	return nil
}
