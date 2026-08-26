package usecase

import "context"

// ImageStorer stores binary image objects in an object-storage bucket.
// It abstracts the GCS dependency so the authoring use case is not tied to a
// specific storage provider and can be tested with a stub.
type ImageStorer interface {
	// Put writes data to the given bucket under key with the supplied content
	// type and returns the public HTTPS URL of the stored object. Object keys are
	// version-addressed (the key embeds a per-upload media id), so a replaced
	// image is written under a NEW key; the caller deletes the prior object via
	// Delete. The stored object is served as immutable, cacheable media.
	//
	// # Possible errors
	//
	//  - Internal: if the write to storage fails.
	Put(ctx context.Context, bucket, key, contentType string, data []byte) (url string, err error)

	// Delete removes a single object by key. Deleting an object that does not
	// exist is not an error (idempotent), so callers can best-effort remove a
	// replaced object without racing a concurrent delete.
	//
	// # Possible errors
	//
	//  - Internal: if the delete fails for a reason other than "not found".
	Delete(ctx context.Context, bucket, key string) error

	// DeletePrefix removes every object whose key starts with prefix. It reclaims
	// all media of an entity (e.g. `series/<id>/`) when that entity is cancelled
	// or deleted. Removing zero objects is not an error.
	//
	// # Possible errors
	//
	//  - Internal: if listing or deleting fails.
	DeletePrefix(ctx context.Context, bucket, prefix string) error
}
