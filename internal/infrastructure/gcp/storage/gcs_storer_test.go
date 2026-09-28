package storage_test

import (
	"testing"

	storage "github.com/liverty-music/backend/internal/infrastructure/gcp/storage"
	"github.com/stretchr/testify/assert"
)

// TestObjectKeyComposition verifies that the originals-bucket key and the
// served-bucket variant key/prefix follow the schemes documented on
// OriginalObjectKey, VariantObjectKey, and VariantObjectPrefix. Moved from
// internal/adapter/event/media_consumer_test.go as part of liverty-music/backend#485
// (object-storage key composition belongs to the infrastructure layer that
// owns object storage, not the entity package).
func TestObjectKeyComposition(t *testing.T) {
	t.Parallel()
	const orgID = "org-test"
	const mediaID = "media-abc"

	assert.Equal(t, "org-test/media-abc", storage.OriginalObjectKey(orgID, mediaID))
	assert.Equal(t, "cdn/org-test/media-abc/thumb.webp", storage.VariantObjectKey(orgID, mediaID, "thumb"))
	assert.Equal(t, "cdn/org-test/media-abc/large.webp", storage.VariantObjectKey(orgID, mediaID, "large"))
	assert.Equal(t, "cdn/org-test/media-abc/", storage.VariantObjectPrefix(orgID, mediaID))
}
