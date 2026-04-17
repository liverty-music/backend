package fanarttv

import (
	"context"
	"fmt"
	"image"
	_ "image/png" // Register PNG decoder.
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/pkg/httpx"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
)

// maxLogoBytes is the maximum response body size for logo image downloads.
// Prevents decompression bombs from consuming unbounded memory.
const maxLogoBytes = 10 << 20 // 10 MB

// LogoFetcher downloads logo images from fanart.tv CDN URLs.
type LogoFetcher struct {
	httpClient *http.Client
}

// Compile-time interface compliance check.
var _ entity.LogoImageFetcher = (*LogoFetcher)(nil)

// NewLogoFetcher creates a new LogoFetcher.
// If httpClient is nil, http.DefaultClient is used.
func NewLogoFetcher(httpClient *http.Client) *LogoFetcher {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &LogoFetcher{httpClient: httpClient}
}

// validateLogoURLFn is the URL validator used by FetchImage. It is a package-level
// variable so that tests can substitute a no-op validator via export_test.go.
var validateLogoURLFn = validateLogoURL

// FetchImage downloads the image at the given URL and decodes it as PNG.
// Returns nil without error when the server returns HTTP 404.
//
// The URL is validated against an allowlist (HTTPS, *.fanart.tv) to prevent
// SSRF, and the response body is capped at maxLogoBytes to guard against
// decompression bombs.
func (f *LogoFetcher) FetchImage(ctx context.Context, logoURL string) (image.Image, error) {
	if err := validateLogoURLFn(logoURL); err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, logoURL, nil)
	if err != nil {
		return nil, apperr.Wrap(err, codes.Internal, "create logo fetch request")
	}

	resp, err := f.httpClient.Do(req)
	if err != nil {
		return nil, apperr.Wrap(err, codes.Unavailable, fmt.Sprintf("fetch logo image from %s", logoURL))
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		var attrs []slog.Attr
		if body := httpx.CaptureResponseBody(resp.Body); body != "" {
			attrs = append(attrs, slog.String("responseBody", body))
		}
		return nil, apperr.New(codes.Unavailable, fmt.Sprintf("logo fetch returned HTTP %d", resp.StatusCode), attrs...)
	}

	img, _, err := image.Decode(io.LimitReader(resp.Body, maxLogoBytes))
	if err != nil {
		return nil, apperr.Wrap(err, codes.Internal, "decode logo image")
	}

	return img, nil
}

// validateLogoURL ensures the URL uses HTTPS and points to a fanart.tv CDN host.
func validateLogoURL(logoURL string) error {
	parsed, err := url.Parse(logoURL)
	if err != nil {
		return apperr.New(codes.InvalidArgument, "logo URL is not a valid URL")
	}
	if parsed.Scheme != "https" {
		return apperr.New(codes.InvalidArgument, "logo URL must use HTTPS")
	}
	host := strings.ToLower(parsed.Hostname())
	if host != "fanart.tv" && !strings.HasSuffix(host, ".fanart.tv") {
		return apperr.New(codes.InvalidArgument, "logo URL host is not on the fanart.tv allowlist")
	}
	return nil
}
