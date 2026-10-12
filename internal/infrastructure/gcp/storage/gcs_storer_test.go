package storage_test

import (
	"testing"

	storage "github.com/liverty-music/backend/internal/infrastructure/gcp/storage"
	"github.com/stretchr/testify/assert"
)

// TestObjectKeyComposition verifies that the originals-bucket key and the
// served-bucket variant key/prefix follow the schemes documented on
// OriginalObjectKey, VariantObjectKey, and VariantObjectPrefix. Moved from
// internal/adapter/event/series_image_consumer_test.go as part of liverty-music/backend#485
// (object-storage key composition belongs to the infrastructure layer that
// owns object storage, not the entity package).
func TestObjectKeyComposition(t *testing.T) {
	t.Parallel()
	const orgID = "org-test"
	const seriesImageID = "image-abc"

	assert.Equal(t, "org-test/image-abc", storage.OriginalObjectKey(orgID, seriesImageID))
	assert.Equal(t, "cdn/org-test/image-abc/thumb.webp", storage.VariantObjectKey(orgID, seriesImageID, "thumb"))
	assert.Equal(t, "cdn/org-test/image-abc/large.webp", storage.VariantObjectKey(orgID, seriesImageID, "large"))
	assert.Equal(t, "cdn/org-test/image-abc/", storage.VariantObjectPrefix(orgID, seriesImageID))
}
