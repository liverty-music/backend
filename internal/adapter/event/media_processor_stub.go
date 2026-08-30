//go:build !vips

package event

import (
	"context"
	"fmt"

	"github.com/pannpers/go-logging/logging"
)

// StubMediaProcessor is the non-vips fallback that always returns an error.
// It is used in environments where libvips is not available (CI, local dev
// without the vips build tag). The media-processor job binary should be built
// with `//go:build vips` in production; when this stub is active, every
// ProcessImage call fails with a clear message so the container crashloops
// rather than silently doing nothing.
type StubMediaProcessor struct {
	logger *logging.Logger
}

// NewMediaProcessor returns the stub processor used when libvips is absent.
// Production binaries must be built with `CGO_ENABLED=1 go build -tags vips`.
func NewMediaProcessor(logger *logging.Logger) MediaProcessor {
	logger.Warn(context.Background(), "libvips not available: media processing is a no-op stub; rebuild with -tags vips for production")
	return &StubMediaProcessor{logger: logger}
}

// ProcessImage always returns ErrUnsupportedMedia when libvips is not compiled in.
// The consumer treats this as a permanent failure and terms the message.
func (s *StubMediaProcessor) ProcessImage(_ context.Context, _ []byte) ([]byte, []byte, error) {
	return nil, nil, fmt.Errorf("%w: libvips not compiled in; rebuild with -tags vips", ErrUnsupportedMedia)
}
