package mapper_test

import (
	"testing"

	"github.com/liverty-music/backend/internal/adapter/rpc/mapper"
	"github.com/stretchr/testify/assert"
)

// TestMediaURLBuilder_VariantURL verifies that MediaURLBuilder composes correct
// CDN URLs when a CDN base is configured, and returns "" (never a malformed
// relative URL) when it is not. Moved from
// internal/adapter/event/media_consumer_test.go as part of
// liverty-music/backend#485: the CDN base is now injected at construction
// (config.ServerConfig.OrganizerMediaCDNBase via DI) instead of being read
// from the environment by the entity package.
func TestMediaURLBuilder_VariantURL(t *testing.T) {
	t.Parallel()

	const orgID = "org-test"
	const mediaID = "media-abc"

	t.Run("composes CDN URL when base is configured", func(t *testing.T) {
		t.Parallel()
		b := mapper.NewMediaURLBuilder("https://cdn.example.com")

		assert.Equal(t, "https://cdn.example.com/cdn/org-test/media-abc/thumb.webp",
			b.VariantURL(orgID, mediaID, "thumb"))
		assert.Equal(t, "https://cdn.example.com/cdn/org-test/media-abc/large.webp",
			b.VariantURL(orgID, mediaID, "large"))
	})

	t.Run("trims a trailing slash on the configured base", func(t *testing.T) {
		t.Parallel()
		b := mapper.NewMediaURLBuilder("https://cdn.example.com/")

		assert.Equal(t, "https://cdn.example.com/cdn/org-test/media-abc/thumb.webp",
			b.VariantURL(orgID, mediaID, "thumb"))
	})

	t.Run("returns empty string when no CDN base is configured", func(t *testing.T) {
		t.Parallel()
		b := mapper.NewMediaURLBuilder("")

		assert.Equal(t, "", b.VariantURL(orgID, mediaID, "thumb"))
	})
}
