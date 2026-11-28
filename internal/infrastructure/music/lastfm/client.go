// Package lastfm provides a client for the Last.fm API.
//
// Usage guidelines, from the API Terms of Service
// (https://www.last.fm/api/tos) and the API introduction
// (https://www.last.fm/api/intro), both read on 2026-11-28:
//
//  1. Non-commercial use only (ToS 3.1). Last.fm Data may be used solely for
//     non-commercial purposes; commercial or research use needs Last.fm's
//     approval first (partners@last.fm). Tracked in liverty-music/backend#953.
//
//  2. Storage (ToS 2.3). Last.fm Data may be accessed, retrieved, stored and
//     used in accordance with the data protection law of the country we live
//     in. The terms do not prohibit permanent storage.
//
//  3. Caching (ToS 4.3). Responses are to be cached as the HTTP headers sent
//     with them say. This client does not read those headers; ArtistUseCase
//     caches each result for 6 hours.
//
//  4. Rate limits (ToS 4.4). Last.fm sets and enforces limits at its sole
//     discretion and publishes no number. The introduction warns that an
//     account may be suspended when an application is "continuously making
//     several calls per second" or makes excessive calls. This client
//     throttles itself to one request every 200 ms (5 per second), the value
//     chosen when it was written; ArtistUseCase caches results for 6 hours, so
//     sustained traffic stays far below that.
//
//  5. Attribution (ToS 2.7 and the preamble). Data shown to users credits
//     Last.fm and links back to it.
//
//  6. User-Agent (introduction). Every request sends an identifiable
//     User-Agent header.
package lastfm

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/cenkalti/backoff/v7"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/pkg/api"
	"github.com/liverty-music/backend/pkg/httpx"
	"github.com/liverty-music/backend/pkg/throttle"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
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
	// userAgent identifies the application on every request, as the API
	// introduction asks.
	userAgent = "LivertyMusic/1.0.0 ( https://liverty-music.app )"
	// Last.fm publishes no numeric rate limit (see the package comment); the
	// client sends at most one request every 200 ms.
	rateLimitInterval = 200 * time.Millisecond
)

// client implements entity.ArtistSearcher using Last.fm API.
type client struct {
	apiKey     string
	httpClient *http.Client
	baseURL    string
	throttler  *throttle.Throttler
	logger     *logging.Logger
}

// NewClient creates a new Last.fm client.
func NewClient(apiKey string, httpClient *http.Client, logger *logging.Logger) *client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &client{
		apiKey:     apiKey,
		httpClient: httpClient,
		baseURL:    baseURL,
		throttler:  throttle.New(rateLimitInterval, 100), // Buffer up to 100 requests
		logger:     logger.With(slog.String("component", "lastfm")),
	}
}

// Search finds artists by name using the Last.fm artist.search method.
func (c *client) Search(ctx context.Context, query string) ([]*entity.Artist, error) {
	c.logger.Info(ctx, "searching artists", slog.String("method", "artist.search"), slog.String("query", query))

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
// When limit is greater than zero it is forwarded as the Last.fm "limit" query parameter;
// otherwise the API default is used.
func (c *client) ListSimilar(ctx context.Context, artist *entity.Artist, limit int32) ([]*entity.Artist, error) {
	c.logger.Info(ctx, "listing similar artists",
		slog.String("method", "artist.getsimilar"),
		slog.String("artistName", artist.Name),
		slog.String("listening_mbid", artist.ListeningMBID),
	)

	// Last.fm files the artist under its listening MBID, which differs from
	// its MBID when that was corrected to the right person's: Last.fm may not
	// know the corrected one.
	params := url.Values{}
	params.Set("method", "artist.getsimilar")
	if artist.ListeningMBID != "" {
		params.Set("mbid", artist.ListeningMBID)
	} else {
		params.Set("artist", artist.Name)
	}
	if limit > 0 {
		params.Set("limit", strconv.Itoa(int(limit)))
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

// ListTop identifies trending artists using Last.fm chart, geo, or tag methods.
// When limit is greater than zero it is forwarded as the Last.fm "limit" query parameter;
// otherwise the API default is used.
func (c *client) ListTop(ctx context.Context, country string, tag string, limit int32) ([]*entity.Artist, error) {
	params := url.Values{}
	if tag != "" {
		params.Set("method", "tag.gettopartists")
		params.Set("tag", tag)
	} else if country != "" {
		params.Set("method", "geo.gettopartists")
		params.Set("country", country)
	} else {
		params.Set("method", "chart.gettopartists")
	}
	if limit > 0 {
		params.Set("limit", strconv.Itoa(int(limit)))
	}

	c.logger.Info(ctx, "listing top artists",
		slog.String("method", params.Get("method")),
		slog.String("country", country),
		slog.String("tag", tag),
	)

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

// send sends the request through the throttler, retrying a retryable status
// with exponential backoff. Retry wraps the throttle call so that backoff
// waits do not block the throttle slot for other callers; method only labels
// the log lines.
func (c *client) send(ctx context.Context, req *http.Request, method string) (*http.Response, error) {
	return backoff.Retry(ctx, func() (*http.Response, error) {
		c.logger.Debug(ctx, "rate limiter backoff", slog.String("method", method))

		var resp *http.Response
		tErr := c.throttler.Do(ctx, func() error {
			var err error
			resp, err = c.httpClient.Do(req)
			return err
		})
		if tErr != nil {
			return nil, backoff.Permanent(tErr)
		}

		if httpx.IsRetryableStatus(resp.StatusCode) {
			c.logger.Warn(ctx, "lastfm returned retryable status",
				slog.String("method", method), slog.Int("statusCode", resp.StatusCode))
			_ = resp.Body.Close()
			return nil, httpx.RetryAfterFromResponse(resp)
		}

		return resp, nil
	},
		backoff.WithBackOff(&backoff.ExponentialBackOff{
			InitialInterval:     1 * time.Second,
			RandomizationFactor: 0.5,
			Multiplier:          2.0,
			MaxInterval:         10 * time.Second,
		}),
		backoff.WithMaxTries(4),
		backoff.WithMaxElapsedTime(0),
	)
}

func (c *client) get(ctx context.Context, params url.Values, result any) error {
	params.Set("api_key", c.apiKey)
	params.Set("format", "json")

	u, _ := url.Parse(c.baseURL)
	u.RawQuery = params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return apperr.Wrap(err, codes.Internal, "failed to create lastfm request")
	}
	req.Header.Set("User-Agent", userAgent)

	method := params.Get("method")

	resp, err := c.send(ctx, req, method)
	if err != nil {
		return api.FromHTTP(err, nil, "lastfm api request failed")
	}
	defer func() { _ = resp.Body.Close() }()

	if err := api.FromHTTP(nil, resp, "lastfm api request failed"); err != nil {
		attrs := []slog.Attr{slog.String("method", method)}
		if resp != nil {
			attrs = append(attrs, slog.Int("statusCode", resp.StatusCode))
		}
		c.logger.Error(ctx, "lastfm HTTP request failed", err, attrs...)
		return err
	}

	// Limit response body to 1MB to prevent OOM attacks
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return apperr.Wrap(err, codes.Internal, "failed to read lastfm response body")
	}

	// Last.fm can return HTTP 200 with an error object
	var errResp errorResponse
	if err := json.Unmarshal(body, &errResp); err == nil && errResp.ErrorCode != 0 {
		c.logger.Error(ctx, "lastfm API returned error", nil,
			slog.String("method", method),
			slog.Int("errorCode", errResp.ErrorCode),
			slog.String("errorMessage", errResp.Message),
		)
		return apperr.New(mapLastFMCode(errResp.ErrorCode), errResp.Message)
	}

	if err := json.Unmarshal(body, result); err != nil {
		return apperr.Wrap(err, codes.Internal, "failed to decode lastfm response")
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
