// Package fanarttv provides a client for the fanart.tv API.
//
// # Usage Guidelines (from fanart.tv API documentation)
//
//  1. API Key
//     A project API key is required. Obtain one by registering at https://fanart.tv/xAPyI.
//
//  2. Rate Limiting
//     Rate limits are rarely enforced. When triggered, the API returns HTTP 429
//     with a Retry-After header. The client handles this with exponential backoff.
//
//  3. Image Availability
//     Free-tier API keys have a 7-day delay before newly uploaded images appear.
//     Not all artists have images. Coverage varies, especially for indie/local artists.
//
//  4. API Versions
//     This client uses v3. v3.1 and v3.2 provide additional fields (added, width, height)
//     that are not required for the current use case.
//
// # Terms of Use (https://fanart.tv/terms-of-service/)
//
// General Terms:
//   - If you have a publicly available program, you must inform your users of
//     this website and the images you use.
//   - You should allow your users to input their own API key into your application,
//     this should be sent in addition to your project key.
//   - Do not use our API methods embedded in your own API for 3rd party use.
//   - Do not use our API for commercial use without written consent.
//   - Do not perform more requests than are necessary for each user. This means
//     no downloading all of our content. Play nice with our server.
//   - Do not directly access our data without using the documented API methods.
//   - You MUST keep the email address in your account information current and
//     accurate in case we need to contact you regarding your key.
//
// Commercial Use:
//   - All general terms still apply.
//   - If you don't want to inform your users about us, you should subscribe to
//     one of the sponsorship levels, depending on the level of traffic you will generate.
//   - If you want to use your own personal API key to bypass the 7 day image limits,
//     you should subscribe to one of the sponsorship levels, the accepted practice is
//     to allow and encourage your users to enter their own personal API keys.
//   - We are a free service, it is not mandatory to donate or subscribe in any way,
//     however, if you are bringing no benefit to the service, either through users or
//     offsetting some of our costs, we have no obligation to continue providing service.
package fanarttv

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/cenkalti/backoff/v5"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/pkg/api"
	"github.com/liverty-music/backend/pkg/httpx"
	"github.com/liverty-music/backend/pkg/throttle"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
)

const (
	baseURL = "https://webservice.fanart.tv/v3/music/"
	// fanart.tv rate limits are generous but we throttle to be a good citizen.
	rateLimitInterval = 200 * time.Millisecond
)

// client implements entity.ArtistImageResolver using the fanart.tv API.
type client struct {
	apiKey     string
	httpClient *http.Client
	baseURL    string
	throttler  *throttle.Throttler
	logger     *logging.Logger
}

// NewClient creates a new fanart.tv client.
func NewClient(apiKey string, httpClient *http.Client, logger *logging.Logger) *client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &client{
		apiKey:     apiKey,
		httpClient: httpClient,
		baseURL:    baseURL,
		throttler:  throttle.New(rateLimitInterval, 100),
		logger:     logger.With(slog.String("component", "fanarttv")),
	}
}

// ResolveImages fetches artist image data from fanart.tv using the artist's MusicBrainz ID.
// Returns nil without error when the artist has no fanart.tv entry (HTTP 404).
func (c *client) ResolveImages(ctx context.Context, mbid string) (*entity.Fanart, error) {
	c.logger.Info(ctx, "resolving artist images", slog.String("mbid", mbid))

	u := fmt.Sprintf("%s%s", c.baseURL, mbid)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, apperr.Wrap(err, codes.Internal, "failed to create fanarttv request")
	}
	req.Header.Set("api-key", c.apiKey)

	resp, err := backoff.Retry(ctx, func() (*http.Response, error) {
		c.logger.Debug(ctx, "rate limiter backoff", slog.String("mbid", mbid))

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
			c.logger.Warn(ctx, "fanarttv returned retryable status",
				slog.String("mbid", mbid), slog.Int("statusCode", resp.StatusCode))
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
	if err != nil {
		return nil, api.FromHTTP(err, nil, "fanarttv api request failed")
	}
	defer func() { _ = resp.Body.Close() }()

	// fanart.tv returns 404 when no artist entry exists.
	if resp.StatusCode == http.StatusNotFound {
		c.logger.Info(ctx, "no fanart.tv entry for artist", slog.String("mbid", mbid))
		return nil, nil
	}

	if err := api.FromHTTP(nil, resp, "fanarttv api request failed"); err != nil {
		c.logger.Error(ctx, "fanarttv HTTP request failed", err,
			slog.String("mbid", mbid), slog.Int("statusCode", resp.StatusCode))
		return nil, err
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, apperr.Wrap(err, codes.DataLoss, "failed to read fanarttv response body")
	}

	var fanart entity.Fanart
	if err := json.Unmarshal(body, &fanart); err != nil {
		return nil, apperr.Wrap(err, codes.DataLoss, "failed to decode fanarttv response")
	}

	c.logger.Info(ctx, "resolved artist images",
		slog.String("mbid", mbid),
		slog.Int("thumbs", len(fanart.ArtistThumb)),
		slog.Int("logos", len(fanart.HDMusicLogo)),
	)

	return &fanart, nil
}

// SetBaseURL allows overriding the base URL used by the client. This is
// primarily intended for tests to point the client at an httptest server.
func (c *client) SetBaseURL(u string) {
	c.baseURL = u
}

// Close stops the background throttler goroutine and releases resources.
func (c *client) Close() error {
	if c.throttler != nil {
		c.throttler.Close()
	}
	return nil
}
