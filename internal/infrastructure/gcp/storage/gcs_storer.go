// Package storage provides a Google Cloud Storage implementation of the
// usecase.ImageStorer interface for persisting organizer-authored media.
package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"cloud.google.com/go/storage"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
	"google.golang.org/api/iterator"
)

// immutableCacheControl is set on every stored object. Object keys are
// version-addressed (the key embeds a per-upload media id), so a given key
// never changes content and may be cached indefinitely; a replaced image is
// written under a new key. This makes served media CDN- and browser-cache
// friendly.
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
// and an immutable cache-control header. All objects are written under the
// cdn/ key prefix; the caller composes the served URL from the media id and
// ORGANIZER_MEDIA_CDN_BASE.
func (s *GCSStorer) Put(ctx context.Context, bucket, key, contentType string, data []byte) error {
	wc := s.client.Bucket(bucket).Object(key).NewWriter(ctx)
	wc.ContentType = contentType
	wc.CacheControl = immutableCacheControl

	if _, err := io.Copy(wc, bytes.NewReader(data)); err != nil {
		_ = wc.Close()
		return apperr.New(codes.Internal, fmt.Sprintf("write to gcs: %v", err))
	}
	if err := wc.Close(); err != nil {
		return apperr.New(codes.Internal, fmt.Sprintf("close gcs writer: %v", err))
	}
	return nil
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

// SignedPutURL returns a V4-signed GCS PUT URL for the given bucket and key.
// The URL is valid for ttl and enforces contentType and a content-length-range
// header condition ([0, maxBytes]) so the client cannot exceed the quota.
// Signing is keyless: the GCS client auto-detects the Workload Identity SA
// email via the GCE metadata service and signs using IAM credentials.Projects.
// ServiceAccounts.SignBlob, so no private key file is needed.
func (s *GCSStorer) SignedPutURL(ctx context.Context, bucket, key, contentType string, maxBytes int64, ttl time.Duration) (string, error) {
	url, err := s.client.Bucket(bucket).SignedURL(key, &storage.SignedURLOptions{
		Method:  "PUT",
		Expires: time.Now().Add(ttl),
		Scheme:  storage.SigningSchemeV4,
		// The client MUST supply both Content-Type and X-Goog-Content-Length-Range
		// headers on the PUT request exactly as specified here; GCS validates the
		// signed conditions and rejects requests that omit or alter them.
		ContentType: contentType,
		Headers: []string{
			fmt.Sprintf("x-goog-content-length-range:0,%d", maxBytes),
		},
	})
	if err != nil {
		return "", apperr.New(codes.Internal, fmt.Sprintf("sign put url for %q: %v", key, err))
	}
	return url, nil
}

// DeletePrefix removes every object whose key begins with prefix from bucket.
// A missing prefix (no matching objects) is silently skipped. Used to reclaim
// all variant files of a replaced media id in a single sweep.
func (s *GCSStorer) DeletePrefix(ctx context.Context, bucket, prefix string) error {
	if prefix == "" {
		return nil
	}
	it := s.client.Bucket(bucket).Objects(ctx, &storage.Query{Prefix: prefix})
	for {
		attrs, err := it.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return apperr.New(codes.Internal, fmt.Sprintf("list gcs objects with prefix %q: %v", prefix, err))
		}
		if delErr := s.client.Bucket(bucket).Object(attrs.Name).Delete(ctx); delErr != nil {
			if !errors.Is(delErr, storage.ErrObjectNotExist) {
				return apperr.New(codes.Internal, fmt.Sprintf("delete gcs object %q: %v", attrs.Name, delErr))
			}
		}
	}
	return nil
}
