package entity

import (
	"context"
	"image"
)

// LogoColorProfile holds the dominant color characteristics extracted from an
// artist's logo image via OKLCH color space analysis.
// DominantHue is nil for achromatic logos where hue is meaningless.
type LogoColorProfile struct {
	// DominantHue is the peak hue angle (0–360) from the OKLCH histogram.
	// Nil when the logo is achromatic (IsChromatic == false).
	DominantHue *float64 `json:"dominantHue,omitempty"`
	// DominantLightness is the mean OKLCH lightness (0–1) of non-transparent pixels.
	DominantLightness float64 `json:"dominantLightness"`
	// IsChromatic is true when more than 30% of non-transparent pixels
	// have OKLCH chroma above 0.04.
	IsChromatic bool `json:"isChromatic"`
}

// Fanart holds community-curated artist images sourced from fanart.tv.
// The struct mirrors the fanart.tv API response structure so that JSON
// marshalling/unmarshalling works directly with both the API and the
// database JSONB column.
type Fanart struct {
	// ArtistThumb contains square portrait photos of the artist (1000x1000).
	ArtistThumb []FanartImage `json:"artistthumb"`
	// ArtistBackground contains high-resolution backdrop images (1920x1080).
	ArtistBackground []FanartImage `json:"artistbackground"`
	// HDMusicLogo contains high-definition transparent logos (800x310).
	HDMusicLogo []FanartImage `json:"hdmusiclogo"`
	// MusicLogo contains standard-definition transparent logos (400x155).
	MusicLogo []FanartImage `json:"musiclogo"`
	// MusicBanner contains wide banner images (1000x185).
	MusicBanner []FanartImage `json:"musicbanner"`
	// LogoColorProfile holds analysis results from the best logo image.
	// Nil when no logo is available or analysis failed.
	LogoColorProfile *LogoColorProfile `json:"logoColorProfile,omitempty"`
}

// FanartImage represents a single community-curated image from fanart.tv.
type FanartImage struct {
	// ID is the fanart.tv internal image identifier.
	ID string `json:"id"`
	// URL is the fully qualified URL to the hosted image.
	URL string `json:"url"`
	// Likes is the number of community votes for this image.
	Likes int `json:"likes,string"`
	// Lang is the ISO 639 language code associated with the image.
	Lang string `json:"lang"`
}

// BestByLikes returns the URL of the image with the highest community vote
// count from the given slice. Returns an empty string when the slice is empty.
func BestByLikes(images []FanartImage) string {
	if len(images) == 0 {
		return ""
	}
	best := images[0]
	for _, img := range images[1:] {
		if img.Likes > best.Likes {
			best = img
		}
	}
	return best.URL
}

// ArtistImageResolver fetches artist image data from an external provider.
type ArtistImageResolver interface {
	// ResolveImages fetches image data for the artist identified by the given
	// MusicBrainz ID. Returns nil without error when no images are found.
	//
	// # Possible errors:
	//
	//   - Unavailable: the external image service is down or rate-limited.
	//   - Internal: unexpected failure during resolution.
	ResolveImages(ctx context.Context, mbid string) (*Fanart, error)
}

// LogoImageFetcher downloads a logo image from a URL and decodes it.
type LogoImageFetcher interface {
	// FetchImage downloads the image at the given URL and returns it decoded.
	// Returns nil without error when the image cannot be fetched (e.g. 404).
	//
	// # Possible errors:
	//
	//   - InvalidArgument: logo URL is malformed, non-HTTPS, or not on the allowed host list.
	//   - Unavailable: the remote server is unreachable or returned an error.
	//   - Internal: unexpected failure during download or decoding.
	FetchImage(ctx context.Context, url string) (image.Image, error)
}
