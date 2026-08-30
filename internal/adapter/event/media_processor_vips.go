//go:build vips

package event

import (
	"bytes"
	"context"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"

	"github.com/davidbyttow/govips/v2/vips"
	"github.com/pannpers/go-logging/logging"
)

// vipsProcessor implements MediaProcessor using libvips via govips.
// Build with: CGO_ENABLED=1 go build -tags vips
type vipsProcessor struct {
	logger *logging.Logger
}

// NewMediaProcessor returns the production vips-backed processor.
func NewMediaProcessor(logger *logging.Logger) MediaProcessor {
	vips.Startup(nil)
	return &vipsProcessor{logger: logger}
}

// ProcessImage decodes raw image bytes using libvips, applies safety limits,
// strips EXIF, and encodes WebP thumb (≤800 w) and large (≤1920 w) variants
// with aspect ratio preserved and no cropping.
func (p *vipsProcessor) ProcessImage(ctx context.Context, data []byte) (thumb []byte, large []byte, err error) {
	// Magic-byte + header-first safety check via stdlib image.DecodeConfig.
	// This is fast (does not decode the full image) and catches:
	//   - invalid/corrupt headers
	//   - SVG (not registered → unknown format → ErrUnsupportedMedia)
	cfg, format, decodeErr := image.DecodeConfig(bytes.NewReader(data))
	if decodeErr != nil {
		return nil, nil, fmt.Errorf("%w: decode config: %v", ErrUnsupportedMedia, decodeErr)
	}
	if format == "svg" || format == "" {
		return nil, nil, fmt.Errorf("%w: format %q not allowed", ErrUnsupportedMedia, format)
	}
	// Edge limit (before full decode).
	if cfg.Width > maxEdgePx || cfg.Height > maxEdgePx {
		return nil, nil, fmt.Errorf("%w: image edge %dx%d exceeds limit %d",
			ErrUnsupportedMedia, cfg.Width, cfg.Height, maxEdgePx)
	}
	// Pixel-count limit (before full decode).
	if int64(cfg.Width)*int64(cfg.Height) > maxPixels {
		return nil, nil, fmt.Errorf("%w: pixel count %d exceeds limit %d",
			ErrUnsupportedMedia, int64(cfg.Width)*int64(cfg.Height), maxPixels)
	}

	// Load via libvips.
	img, err := vips.NewImageFromBuffer(data)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: vips load: %v", ErrUnsupportedMedia, err)
	}
	defer img.Close()

	// Strip EXIF / ICC profile metadata.
	if err := img.RemoveMetadata(); err != nil {
		return nil, nil, fmt.Errorf("strip metadata: %w", err)
	}

	// Encode thumb (max 800 w, aspect-preserved, no crop).
	thumb, err = resizeAndEncodeWebP(img, 800)
	if err != nil {
		return nil, nil, fmt.Errorf("encode thumb: %w", err)
	}

	// Encode large (max 1920 w, aspect-preserved, no crop).
	large, err = resizeAndEncodeWebP(img, 1920)
	if err != nil {
		return nil, nil, fmt.Errorf("encode large: %w", err)
	}

	return thumb, large, nil
}

// resizeAndEncodeWebP resizes img to at most maxWidth pixels wide (preserving
// aspect ratio, no crop) and returns the WebP-encoded bytes.
func resizeAndEncodeWebP(img *vips.ImageRef, maxWidth int) ([]byte, error) {
	// Clone so we can resize independently for each variant.
	clone, err := img.Copy()
	if err != nil {
		return nil, fmt.Errorf("clone image: %w", err)
	}
	defer clone.Close()

	w := clone.Width()
	if w > maxWidth {
		scale := float64(maxWidth) / float64(w)
		if err := clone.Resize(scale, vips.KernelLanczos3); err != nil {
			return nil, fmt.Errorf("resize: %w", err)
		}
	}

	ep := vips.NewWebpExportParams()
	ep.Quality = 85
	ep.Lossless = false

	buf, _, err := clone.ExportWebp(ep)
	if err != nil {
		return nil, fmt.Errorf("export webp: %w", err)
	}
	return buf, nil
}
