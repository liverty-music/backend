package storage

import (
	gcs "cloud.google.com/go/storage"
	"github.com/pannpers/go-logging/logging"
)

// NewGCSStorerWithClient builds a GCSStorer around the given client, so tests
// can point it at a fake GCS server.
func NewGCSStorerWithClient(client *gcs.Client, logger *logging.Logger) *GCSStorer {
	return &GCSStorer{client: client, logger: logger}
}
