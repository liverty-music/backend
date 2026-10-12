package entity

import (
	"context"
	"errors"
)

// ErrUnsupportedImage is returned by [SeriesImageProcessor.ProcessImage] when the
// original fails a safety check (invalid magic bytes, exceeds the pixel/edge
// limits, is an SVG, etc.). SeriesImageUseCase.Process treats this as a
// permanent failure: the original is deleted and the event is not retried.
var ErrUnsupportedImage = errors.New("unsupported or unsafe image")

// SeriesImageProcessor abstracts the image-processing step (decode, resize, encode
// WebP variants) that SeriesImageUseCase.Process orchestrates. The concrete
// implementation is infrastructure (libvips); declaring the port here keeps
// the usecase free of any infrastructure import.
type SeriesImageProcessor interface {
	// ProcessImage reads raw image bytes and returns thumb (≤800 w) and large
	// (≤1920 w) WebP-encoded variants, preserving aspect ratio. EXIF is
	// stripped. Magic-byte validation and pixel/edge limits are enforced
	// before the image is fully decoded. Returns ErrUnsupportedImage for
	// images that fail a safety check.
	ProcessImage(ctx context.Context, data []byte) (thumb []byte, large []byte, err error)
}
