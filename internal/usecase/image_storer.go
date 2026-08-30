package usecase

import (
	"context"
	"time"
)

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

	// SignedPutURL returns a V4-signed GCS PUT URL for the given bucket and key.
	// The URL is valid for ttl, enforces contentType via a Content-Type condition,
	// and restricts the upload size to [0, maxBytes] via an
	// x-goog-content-length-range condition. Signing is keyless: the
	// implementation uses IAM SignBlob via the Workload Identity Service Account,
	// so no private key file is needed in GKE.
	//
	// # Possible errors
	//
	//  - Internal: if IAM SignBlob or the URL construction fails.
	SignedPutURL(ctx context.Context, bucket, key, contentType string, maxBytes int64, ttl time.Duration) (string, error)

	// DeletePrefix removes every object whose key begins with the given prefix
	// from the specified bucket. A missing object (or an empty prefix) is silently
	// skipped so the operation is idempotent. Used to reclaim all variant files
	// (thumb, large) of a replaced media id in a single sweep.
	//
	// # Possible errors
	//
	//  - Internal: if listing or deletion fails.
	DeletePrefix(ctx context.Context, bucket, prefix string) error
}
