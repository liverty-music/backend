package storage

import "strings"

// SeriesImageURLBuilder composes public CDN URLs for organizer series image
// variants (thumb/large). It lives next to [VariantObjectKey] because the CDN
// serves the served bucket as is, so a variant's URL is the CDN base followed
// by its object key. The CDN base comes from config.ServerConfig.CDNBaseURL
// and is injected at DI wiring time (see internal/di/provider.go).
type SeriesImageURLBuilder struct {
	cdnBase string
}

// NewSeriesImageURLBuilder creates a SeriesImageURLBuilder for the given CDN base URL.
// Trailing slashes are trimmed so composed URLs never contain a double slash.
func NewSeriesImageURLBuilder(cdnBase string) *SeriesImageURLBuilder {
	return &SeriesImageURLBuilder{cdnBase: strings.TrimRight(cdnBase, "/")}
}

// VariantURL composes the public CDN URL for one variant of a series image:
// {cdnBase}/{VariantObjectKey}. Returns "" when no CDN base is configured,
// so callers never emit a malformed relative URL.
//
// variant must be one of "thumb" or "large".
func (b *SeriesImageURLBuilder) VariantURL(organizerID, seriesImageID, variant string) string {
	if b == nil || b.cdnBase == "" {
		return ""
	}
	return b.cdnBase + "/" + VariantObjectKey(organizerID, seriesImageID, variant)
}
