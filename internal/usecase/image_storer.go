package usecase

import "context"

// ImageStorer stores binary image objects in an object-storage bucket.
// It abstracts the GCS dependency so the authoring use case is not tied to a
// specific storage provider and can be tested with a stub.
type ImageStorer interface {
	// Put writes data to the given bucket under key with the supplied content
	// type and returns the public HTTPS URL of the stored object. The caller is
	// responsible for choosing a key that is stable across re-uploads (so that
	// repeated UploadCoverImage calls replace the prior object rather than
	// creating a new key each time).
	//
	// # Possible errors
	//
	//  - Internal: if the write to storage fails.
	Put(ctx context.Context, bucket, key, contentType string, data []byte) (url string, err error)
}
