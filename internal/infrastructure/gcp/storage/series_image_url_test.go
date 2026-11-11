package storage_test

import (
	"testing"

	"github.com/liverty-music/backend/internal/infrastructure/gcp/storage"
	"github.com/stretchr/testify/assert"
)

// TestSeriesImageURLBuilder_VariantURL verifies that SeriesImageURLBuilder composes correct
// CDN URLs when a CDN base is configured, and returns "" (never a malformed
// relative URL) when it is not.
func TestSeriesImageURLBuilder_VariantURL(t *testing.T) {
	t.Parallel()

	const orgID = "org-test"
	const seriesImageID = "image-abc"

	t.Run("composes CDN URL when base is configured", func(t *testing.T) {
		t.Parallel()
		b := storage.NewSeriesImageURLBuilder("https://cdn.example.com")

		assert.Equal(t, "https://cdn.example.com/cdn/org-test/image-abc/thumb.webp",
			b.VariantURL(orgID, seriesImageID, "thumb"))
		assert.Equal(t, "https://cdn.example.com/cdn/org-test/image-abc/large.webp",
			b.VariantURL(orgID, seriesImageID, "large"))
	})

	t.Run("trims a trailing slash on the configured base", func(t *testing.T) {
		t.Parallel()
		b := storage.NewSeriesImageURLBuilder("https://cdn.example.com/")

		assert.Equal(t, "https://cdn.example.com/cdn/org-test/image-abc/thumb.webp",
			b.VariantURL(orgID, seriesImageID, "thumb"))
	})

	t.Run("returns empty string when no CDN base is configured", func(t *testing.T) {
		t.Parallel()
		b := storage.NewSeriesImageURLBuilder("")

		assert.Equal(t, "", b.VariantURL(orgID, seriesImageID, "thumb"))
	})
}
