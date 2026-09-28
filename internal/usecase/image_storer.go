package usecase

import (
	"context"
	"time"
)

// ImageStorer stores and retrieves binary media objects backing
// organizer-authored series media (the ORIGINAL upload and its derived
// thumb/large variants). It abstracts the GCS dependency so the media use
// case and the media-processor consumer are not tied to a specific storage
// provider and can be tested with a stub.
//
// Object addressing is identified purely by (organizerID, mediaID[, variant]);
// the infrastructure implementation is responsible for composing the actual
// storage key (see internal/infrastructure/gcp/storage.OriginalObjectKey /
// VariantObjectKey / VariantObjectPrefix), so callers never construct or parse
// storage keys themselves.
type ImageStorer interface {
	// SignedPutURLForOriginal returns a V4-signed GCS PUT URL for uploading the
	// ORIGINAL object of the given organizer/media pair to bucket. The URL is
	// valid for ttl, enforces contentType via a Content-Type condition, and
	// restricts the upload size to [0, maxBytes] via an
	// x-goog-content-length-range condition. Signing is keyless: the
	// implementation uses IAM SignBlob via the Workload Identity Service
	// Account, so no private key file is needed in GKE.
	//
	// # Possible errors
	//
	//  - Internal: if IAM SignBlob or the URL construction fails.
	SignedPutURLForOriginal(ctx context.Context, bucket, organizerID, mediaID, contentType string, maxBytes int64, ttl time.Duration) (string, error)

	// ReadOriginal downloads the ORIGINAL object bytes for the given
	// organizer/media pair from bucket. Used by the media-processor consumer to
	// read the upload before transcoding.
	//
	// # Possible errors
	//
	//  - Internal: if the read fails.
	ReadOriginal(ctx context.Context, bucket, organizerID, mediaID string) ([]byte, error)

	// DeleteOriginal removes the ORIGINAL object for the given organizer/media
	// pair from bucket. Deleting an object that does not exist is not an error
	// (idempotent), so callers can best-effort remove a processed original
	// without racing a concurrent delete.
	//
	// # Possible errors
	//
	//  - Internal: if the delete fails for a reason other than "not found".
	DeleteOriginal(ctx context.Context, bucket, organizerID, mediaID string) error

	// PutVariant writes a processed image variant (e.g. "thumb" or "large") for
	// the given organizer/media pair to bucket with the supplied content type.
	// The object is stored with an immutable cache-control header so caches can
	// serve it indefinitely; a replaced image is written under a new media id.
	//
	// # Possible errors
	//
	//  - Internal: if the write to storage fails.
	PutVariant(ctx context.Context, bucket, organizerID, mediaID, variant, contentType string, data []byte) error

	// DeleteVariants removes every variant object (thumb, large, ...) for the
	// given organizer/media pair from bucket. A missing object is silently
	// skipped so the operation is idempotent. Used to reclaim all variant files
	// of a replaced media id in a single sweep.
	//
	// # Possible errors
	//
	//  - Internal: if listing or deletion fails.
	DeleteVariants(ctx context.Context, bucket, organizerID, mediaID string) error
}
