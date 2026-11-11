//go:build !vips

package event

import (
	"context"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
)

// StubSeriesImageProcessor is the non-vips fallback that always returns an error.
// It is used in environments where libvips is not available (CI, local dev
// without the vips build tag). The series-image-consumer binary should be built
// with `//go:build vips` in production; when this stub is active, every
// ProcessImage call fails with a clear message so the container crashloops
// rather than silently doing nothing.
type StubSeriesImageProcessor struct {
	logger *logging.Logger
}

// Compile-time interface check.
var _ entity.SeriesImageProcessor = (*StubSeriesImageProcessor)(nil)

// NewSeriesImageProcessor returns the stub processor used when libvips is absent.
// Production binaries must be built with `CGO_ENABLED=1 go build -tags vips`.
func NewSeriesImageProcessor(logger *logging.Logger) entity.SeriesImageProcessor {
	logger.Warn(context.Background(), "libvips not available: image processing is a no-op stub; rebuild with -tags vips for production")
	return &StubSeriesImageProcessor{logger: logger}
}

// ProcessImage always fails with InvalidArgument when libvips is not
// compiled in. SeriesImageUseCase.Process treats this as a permanent failure
// and terminates processing without an error.
func (s *StubSeriesImageProcessor) ProcessImage(_ context.Context, _ []byte) ([]byte, []byte, error) {
	return nil, nil, apperr.New(codes.InvalidArgument, "unsupported image: libvips not compiled in; rebuild with -tags vips")
}
