package usecase

import "context"

// ImageStorer stores binary media objects in an object-storage bucket.
// It abstracts the GCS dependency so the authoring use case is not tied to a
// specific storage provider and can be tested with a stub.
//
// Object keys follow the scheme `cdn/{organizer_id}/{media_id}`. Content-Type
// is carried by GCS object metadata, not the key.
type ImageStorer interface {
	// Put writes data to the given bucket under key with the supplied content
	// type. The object is stored with an immutable cache-control header so
	// caches can serve it indefinitely; a replaced image is written under a new
	// key (new media id). No URL is returned — the caller composes the CDN URL
	// from the media id and ORGANIZER_MEDIA_CDN_BASE.
	//
	// # Possible errors
	//
	//  - Internal: if the write to storage fails.
	Put(ctx context.Context, bucket, key, contentType string, data []byte) error

	// Delete removes a single object by key. Deleting an object that does not
	// exist is not an error (idempotent), so callers can best-effort remove a
	// replaced object without racing a concurrent delete.
	//
	// # Possible errors
	//
	//  - Internal: if the delete fails for a reason other than "not found".
	Delete(ctx context.Context, bucket, key string) error
}
