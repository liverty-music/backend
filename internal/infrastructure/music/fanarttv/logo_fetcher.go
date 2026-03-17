package fanarttv

import (
	"context"
	"fmt"
	"image"
	_ "image/png" // Register PNG decoder.
	"net/http"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
)

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

// FetchImage downloads the image at the given URL and decodes it as PNG.
// Returns nil without error when the server returns HTTP 404.
func (f *LogoFetcher) FetchImage(ctx context.Context, url string) (image.Image, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, apperr.Wrap(err, codes.Internal, "create logo fetch request")
	}

	resp, err := f.httpClient.Do(req)
	if err != nil {
		return nil, apperr.Wrap(err, codes.Unavailable, fmt.Sprintf("fetch logo image from %s", url))
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, apperr.New(codes.Unavailable, fmt.Sprintf("logo fetch returned HTTP %d", resp.StatusCode))
	}

	img, _, err := image.Decode(resp.Body)
	if err != nil {
		return nil, apperr.Wrap(err, codes.Internal, "decode logo image")
	}

	return img, nil
}
