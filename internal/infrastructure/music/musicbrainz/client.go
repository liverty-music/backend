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
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
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

const (
	baseURL      = "https://musicbrainz.org/ws/2/artist/"
	placeBaseURL = "https://musicbrainz.org/ws/2/place/"
	userAgent    = "LivertyMusic/1.0.0 ( contact: pannpers@gmail.com )"
	// MusicBrainz rate limit is 1 request per second.
	rateLimitInterval = 1 * time.Second
)

type artistResponse struct {
	ID        string        `json:"id"`
	Name      string        `json:"name"`
	LifeSpan  lifeSpan      `json:"life-span"`
	Relations []urlRelation `json:"relations"`
}

// lifeSpan is the artist's active period. MusicBrainz sets ended to true when a
// group dissolved or a solo artist died, also without an end date, and to null
// or false otherwise.
type lifeSpan struct {
	Ended *bool `json:"ended"`
}

type urlRelation struct {
	Type         string      `json:"type"`
	SourceCredit string      `json:"source-credit"`
	Ended        bool        `json:"ended"`
	URL          urlResource `json:"url"`
}

type urlResource struct {
	Resource string `json:"resource"`
}

// Place represents a MusicBrainz place (venue) record.
type Place struct {
	// ID is the MusicBrainz Place identifier (MBID).
	ID string
	// Name is the canonical name of the place.
	Name string
	// Latitude is the WGS 84 latitude of the place.
	Latitude *float64
	// Longitude is the WGS 84 longitude of the place.
	Longitude *float64
}

type placeSearchResponse struct {
	Places []struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Coordinates struct {
			Latitude  string `json:"latitude"`
			Longitude string `json:"longitude"`
		} `json:"coordinates"`
	} `json:"places"`
}

// client implements entity.ArtistIdentityManager and entity.OfficialSiteCatalog
// for specific MusicBrainz lookups.
type client struct {
	httpClient   *http.Client
	baseURL      string
	placeBaseURL string
	throttler    *throttle.Throttler
	logger       *logging.Logger
}

// NewClient creates a new MusicBrainz client instance.
func NewClient(httpClient *http.Client, logger *logging.Logger) *client {
	if httpClient == nil {
		httpClient = &http.Client{
			Timeout: 10 * time.Second,
		}
	}
	return &client{
		httpClient:   httpClient,
		baseURL:      baseURL,
		placeBaseURL: placeBaseURL,
		throttler:    throttle.New(rateLimitInterval, 100), // Buffer up to 100 requests
		logger:       logger.With(slog.String("component", "musicbrainz")),
	}
}

// doWithRetry executes an HTTP request through the throttler with retry on
// transient errors (429, 503, 504). Retry wraps the throttle call (Pattern A)
// so that backoff waits do not block the throttle slot for other callers.
func (c *client) doWithRetry(ctx context.Context, req *http.Request) (*http.Response, error) {
	return backoff.Retry(ctx, func() (*http.Response, error) {
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
			c.logger.Warn(ctx, "musicbrainz returned retryable status",
				slog.Int("statusCode", resp.StatusCode))
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

// GetArtist retrieves canonical artist data using an MBID: the name, the MBID
// and whether the artist's activity has ended (life-span.ended).
func (c *client) GetArtist(ctx context.Context, mbid string) (*entity.Artist, error) {
	c.logger.Info(ctx, "getting artist", slog.String("mbid", mbid))

	url := fmt.Sprintf("%s%s?fmt=json", c.baseURL, mbid)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, apperr.Wrap(err, codes.Internal, "failed to create musicbrainz request")
	}

	req.Header.Set("User-Agent", userAgent)

	resp, err := c.doWithRetry(ctx, req)
	if err != nil {
		return nil, api.FromHTTP(err, nil, "musicbrainz api request failed")
	}
	defer func() { _ = resp.Body.Close() }()

	if err := api.FromHTTP(nil, resp, "musicbrainz api request failed"); err != nil {
		c.logger.Error(ctx, "musicbrainz artist request failed", err, slog.String("mbid", mbid))
		return nil, err
	}

	var data artistResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, apperr.Wrap(err, codes.Internal, "failed to decode musicbrainz response")
	}

	artist := entity.NewArtist(data.Name, data.ID)
	artist.ActivityEnded = data.LifeSpan.Ended != nil && *data.LifeSpan.Ended
	return artist, nil
}

// ListOfficialSiteRelations returns the artist's name and its "official
// homepage" url relations, ended ones included, read from MusicBrainz url-rels
// for the artist identified by the given MBID.
func (c *client) ListOfficialSiteRelations(ctx context.Context, mbid string) (*entity.OfficialSiteRelations, error) {
	c.logger.Info(ctx, "listing official site relations", slog.String("mbid", mbid))

	url := fmt.Sprintf("%s%s?inc=url-rels&fmt=json", c.baseURL, mbid)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, apperr.Wrap(err, codes.Internal, "failed to create musicbrainz url-rels request")
	}

	req.Header.Set("User-Agent", userAgent)

	resp, err := c.doWithRetry(ctx, req)
	if err != nil {
		return nil, api.FromHTTP(err, nil, "musicbrainz url-rels request failed")
	}
	defer func() { _ = resp.Body.Close() }()

	if err := api.FromHTTP(nil, resp, "musicbrainz url-rels request failed"); err != nil {
		c.logger.Error(ctx, "musicbrainz url-rels request failed", err, slog.String("mbid", mbid))
		return nil, err
	}

	var data artistResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, apperr.Wrap(err, codes.Internal, "failed to decode musicbrainz url-rels response")
	}

	rels := &entity.OfficialSiteRelations{ArtistName: data.Name}
	for _, r := range data.Relations {
		if r.Type != officialHomepageRelation {
			continue
		}
		rels.Relations = append(rels.Relations, entity.OfficialSiteRelation{
			URL:          r.URL.Resource,
			SourceCredit: r.SourceCredit,
			Ended:        r.Ended,
		})
	}
	return rels, nil
}

// officialHomepageRelation is the MusicBrainz url relation type of an
// artist's official homepage.
const officialHomepageRelation = "official homepage"

// escapeLucenePhrase escapes characters that are special inside a Lucene
// double-quoted phrase (backslash and double-quote).
func escapeLucenePhrase(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return r.Replace(s)
}

// SearchPlace searches for a venue by name (and optionally admin area) using
// the MusicBrainz place search endpoint. It returns at most
// placeSearchLimit places in MusicBrainz's order of relevance, and an empty
// slice when it found none.
func (c *client) SearchPlace(ctx context.Context, name, adminArea string) ([]Place, error) {
	c.logger.Info(ctx, "searching place", slog.String("venueName", name), slog.String("adminArea", adminArea))
	// Wrap terms in double quotes to force Lucene phrase matching.
	// Without quotes, names with spaces (e.g. "Zepp Nagoya") are tokenised into
	// separate terms and the query is misinterpreted.
	// Escape backslash and double-quote inside phrase to prevent Lucene injection.
	lucene := fmt.Sprintf(`place:"%s"`, escapeLucenePhrase(name))
	if adminArea != "" {
		lucene += fmt.Sprintf(` AND area:"%s"`, escapeLucenePhrase(adminArea))
	}
	params := url.Values{}
	params.Set("query", lucene)
	params.Set("limit", strconv.Itoa(placeSearchLimit))
	params.Set("fmt", "json")
	endpoint := fmt.Sprintf("%s?%s", c.placeBaseURL, params.Encode())

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, apperr.Wrap(err, codes.Internal, "failed to create musicbrainz place request")
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := c.doWithRetry(ctx, req)
	if err != nil {
		return nil, api.FromHTTP(err, nil, "musicbrainz place search failed")
	}
	defer func() { _ = resp.Body.Close() }()

	if err := api.FromHTTP(nil, resp, "musicbrainz place search failed"); err != nil {
		c.logger.Error(ctx, "musicbrainz place search failed", err, slog.String("venueName", name))
		return nil, err
	}

	var data placeSearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, apperr.Wrap(err, codes.Internal, "failed to decode musicbrainz place response")
	}
	places := make([]Place, 0, len(data.Places))
	for _, r := range data.Places {
		place := Place{ID: r.ID, Name: r.Name}
		if r.Coordinates.Latitude != "" && r.Coordinates.Longitude != "" {
			if lat, err := strconv.ParseFloat(r.Coordinates.Latitude, 64); err == nil {
				place.Latitude = &lat
			}
			if lng, err := strconv.ParseFloat(r.Coordinates.Longitude, 64); err == nil {
				place.Longitude = &lng
			}
		}
		places = append(places, place)
	}
	return places, nil
}

// placeSearchLimit is the most places SearchPlace asks MusicBrainz for.
const placeSearchLimit = 5

// Compile-time interface compliance checks.
var (
	_ entity.ArtistIdentityManager = (*client)(nil)
	_ entity.OfficialSiteCatalog   = (*client)(nil)
)

// SetBaseURL allows overriding the base URL used by the client. This is
// primarily intended for tests to point the client at an httptest server.
func (c *client) SetBaseURL(u string) {
	c.baseURL = u
}

// SetPlaceBaseURL allows overriding the place search base URL used by the client.
// This is primarily intended for tests to point the client at an httptest server.
func (c *client) SetPlaceBaseURL(u string) {
	c.placeBaseURL = u
}

// Close stops the background throttler goroutine and releases resources.
// It should be called when the client is no longer needed.
func (c *client) Close() error {
	if c.throttler != nil {
		c.throttler.Close()
	}
	return nil
}
