// Package storage provides a Google Cloud Storage implementation of the
// usecase.ImageStorer interface for persisting organizer cover images.
package storage

import (
	"bytes"
	"context"
	"fmt"
	"io"

	"cloud.google.com/go/storage"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
)

// GCSStorer stores image objects in Google Cloud Storage using the default
// Application Default Credentials (Workload Identity in GKE).
type GCSStorer struct {
	client *storage.Client
	logger *logging.Logger
}

// NewGCSStorer creates a new GCSStorer. It establishes the GCS client using
// Application Default Credentials so the caller does not need to supply
// explicit credentials. The client is reused across all Put calls.
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
// and returns the public HTTPS URL of the stored object. The object is written
// with public-read access so it can be served directly to browsers.
func (s *GCSStorer) Put(ctx context.Context, bucket, key, contentType string, data []byte) (string, error) {
	wc := s.client.Bucket(bucket).Object(key).NewWriter(ctx)
	wc.ContentType = contentType

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
