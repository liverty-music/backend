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
	"strings"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/pkg/api"
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
	Relations []urlRelation `json:"relations"`
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
}

type placeSearchResponse struct {
	Places []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"places"`
}

// client implements entity.ArtistIdentityManager and entity.OfficialSiteResolver
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

// GetArtist retrieves canonical artist data using an MBID.
func (c *client) GetArtist(ctx context.Context, mbid string) (*entity.Artist, error) {
	c.logger.Info(ctx, "getting artist", slog.String("mbid", mbid))

	url := fmt.Sprintf("%s%s?fmt=json", c.baseURL, mbid)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, apperr.Wrap(err, codes.Internal, "failed to create musicbrainz request")
	}

	req.Header.Set("User-Agent", userAgent)

	c.logger.Debug(ctx, "rate limiter backoff", slog.String("mbid", mbid))

	var resp *http.Response
	err = c.throttler.Do(ctx, func() error {
		var err error
		resp, err = c.httpClient.Do(req)
		return err
	})

	if err := api.FromHTTP(err, resp, "musicbrainz api request failed"); err != nil {
		c.logger.Error(ctx, "musicbrainz artist request failed", err, slog.String("mbid", mbid))
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	var data artistResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, apperr.Wrap(err, codes.DataLoss, "failed to decode musicbrainz response")
	}

	return entity.NewArtist(data.Name, data.ID), nil
}

// ResolveOfficialSiteURL returns the primary official homepage URL for the artist
// identified by the given MBID using MusicBrainz url-rels.
//
// Selection priority (first match wins):
//  1. ended=false AND source-credit matches artistName (case-insensitive)
//  2. ended=false AND source-credit is empty
//  3. ended=false (any active relation, fallback)
//
// Returns an empty string without error when no active official homepage is found.
func (c *client) ResolveOfficialSiteURL(ctx context.Context, mbid string) (string, error) {
	c.logger.Info(ctx, "resolving official site URL", slog.String("mbid", mbid))

	url := fmt.Sprintf("%s%s?inc=url-rels&fmt=json", c.baseURL, mbid)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", apperr.Wrap(err, codes.Internal, "failed to create musicbrainz url-rels request")
	}

	req.Header.Set("User-Agent", userAgent)

	c.logger.Debug(ctx, "rate limiter backoff", slog.String("mbid", mbid))

	var resp *http.Response
	err = c.throttler.Do(ctx, func() error {
		var err error
		resp, err = c.httpClient.Do(req)
		return err
	})

	if err := api.FromHTTP(err, resp, "musicbrainz url-rels request failed"); err != nil {
		c.logger.Error(ctx, "musicbrainz url-rels request failed", err, slog.String("mbid", mbid))
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	var data artistResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return "", apperr.Wrap(err, codes.DataLoss, "failed to decode musicbrainz url-rels response")
	}

	selectedURL := selectOfficialSiteURL(data.Name, data.Relations)
	if selectedURL == "" {
		c.logger.Warn(ctx, "no official site URL found", slog.String("mbid", mbid), slog.String("artistName", data.Name))
	}
	return selectedURL, nil
}

// selectOfficialSiteURL picks the best official homepage URL from a list of url relations.
// Priority: name-matched active > unattributed active > any active > empty string.
func selectOfficialSiteURL(artistName string, relations []urlRelation) string {
	const officialHomepage = "official homepage"

	var fallbackEmpty, fallbackAny string

	for _, r := range relations {
		if r.Type != officialHomepage || r.Ended {
			continue
		}
		url := r.URL.Resource
		if strings.EqualFold(r.SourceCredit, artistName) {
			return url
		}
		if r.SourceCredit == "" && fallbackEmpty == "" {
			fallbackEmpty = url
		}
		if fallbackAny == "" {
			fallbackAny = url
		}
	}

	if fallbackEmpty != "" {
		return fallbackEmpty
	}
	return fallbackAny
}

// escapeLucenePhrase escapes characters that are special inside a Lucene
// double-quoted phrase (backslash and double-quote).
func escapeLucenePhrase(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return r.Replace(s)
}

// SearchPlace searches for a venue by name (and optionally admin area) using
// the MusicBrainz place search endpoint. It returns the top match or
// apperr.ErrNotFound if no results are returned.
func (c *client) SearchPlace(ctx context.Context, name, adminArea string) (*Place, error) {
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
	params.Set("limit", "1")
	params.Set("fmt", "json")
	endpoint := fmt.Sprintf("%s?%s", c.placeBaseURL, params.Encode())

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, apperr.Wrap(err, codes.Internal, "failed to create musicbrainz place request")
	}
	req.Header.Set("User-Agent", userAgent)

	c.logger.Debug(ctx, "rate limiter backoff", slog.String("venueName", name))

	var resp *http.Response
	err = c.throttler.Do(ctx, func() error {
		var err error
		resp, err = c.httpClient.Do(req)
		return err
	})
	if resp != nil {
		defer func() { _ = resp.Body.Close() }()
	}
	if err := api.FromHTTP(err, resp, "musicbrainz place search failed"); err != nil {
		c.logger.Error(ctx, "musicbrainz place search failed", err, slog.String("venueName", name))
		return nil, err
	}

	var data placeSearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, apperr.Wrap(err, codes.DataLoss, "failed to decode musicbrainz place response")
	}
	if len(data.Places) == 0 {
		return nil, apperr.New(codes.NotFound, "no matching place found in musicbrainz")
	}
	p := data.Places[0]
	return &Place{ID: p.ID, Name: p.Name}, nil
}

// Compile-time interface compliance checks.
var (
	_ entity.ArtistIdentityManager = (*client)(nil)
	_ entity.OfficialSiteResolver  = (*client)(nil)
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
