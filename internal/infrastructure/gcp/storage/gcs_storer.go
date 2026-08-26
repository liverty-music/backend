// Package storage provides a Google Cloud Storage implementation of the
// usecase.ImageStorer interface for persisting organizer-authored media.
package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"

	"cloud.google.com/go/storage"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
	"google.golang.org/api/iterator"
)

// immutableCacheControl is set on every stored object. Objects are
// content-addressed (the key embeds a hash of the bytes), so a given URL never
// changes content and may be cached indefinitely; a replaced image is written
// under a new URL. This makes served media CDN- and browser-cache friendly.
const immutableCacheControl = "public, max-age=31536000, immutable"

// GCSStorer stores media objects in Google Cloud Storage using the default
// Application Default Credentials (Workload Identity in GKE).
type GCSStorer struct {
	client *storage.Client
	logger *logging.Logger
}

// NewGCSStorer creates a new GCSStorer. It establishes the GCS client using
// Application Default Credentials so the caller does not need to supply
// explicit credentials. The client is reused across all calls.
func NewGCSStorer(ctx context.Context, logger *logging.Logger) (*GCSStorer, error) {
	client, err := storage.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("create gcs client: %w", err)
	}
	return &GCSStorer{client: client, logger: logger}, nil
}

// Close releases the GCS client resources.
func (s *GCSStorer) Close() error {
	return s.client.Close()
}

// Put writes data to the given bucket under key with the supplied content type
// and an immutable cache-control header, and returns the public HTTPS URL of
// the stored object. The object is served publicly (bucket IAM grants allUsers
// object-viewer) so it can be rendered directly by browsers.
func (s *GCSStorer) Put(ctx context.Context, bucket, key, contentType string, data []byte) (string, error) {
	wc := s.client.Bucket(bucket).Object(key).NewWriter(ctx)
	wc.ContentType = contentType
	wc.CacheControl = immutableCacheControl

	if _, err := io.Copy(wc, bytes.NewReader(data)); err != nil {
		_ = wc.Close()
		return "", apperr.New(codes.Internal, fmt.Sprintf("write to gcs: %v", err))
	}
	if err := wc.Close(); err != nil {
		return "", apperr.New(codes.Internal, fmt.Sprintf("close gcs writer: %v", err))
	}

	url := fmt.Sprintf("https://storage.googleapis.com/%s/%s", bucket, key)
	return url, nil
}

// Delete removes a single object by key. A missing object is treated as success
// so replace/cleanup paths are idempotent.
func (s *GCSStorer) Delete(ctx context.Context, bucket, key string) error {
	err := s.client.Bucket(bucket).Object(key).Delete(ctx)
	if err != nil && !errors.Is(err, storage.ErrObjectNotExist) {
		return apperr.New(codes.Internal, fmt.Sprintf("delete gcs object %q: %v", key, err))
	}
	return nil
}

// DeletePrefix removes every object whose key starts with prefix. Deleting zero
// objects is success. A single object failing to delete aborts the sweep and
// returns the error (the caller treats media cleanup as best-effort).
func (s *GCSStorer) DeletePrefix(ctx context.Context, bucket, prefix string) error {
	it := s.client.Bucket(bucket).Objects(ctx, &storage.Query{Prefix: prefix})
	for {
		attrs, err := it.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return apperr.New(codes.Internal, fmt.Sprintf("list gcs objects under %q: %v", prefix, err))
		}
		if delErr := s.client.Bucket(bucket).Object(attrs.Name).Delete(ctx); delErr != nil &&
			!errors.Is(delErr, storage.ErrObjectNotExist) {
			return apperr.New(codes.Internal, fmt.Sprintf("delete gcs object %q: %v", attrs.Name, delErr))
		}
	}
	return nil
}
