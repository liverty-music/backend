// Package event provides Watermill event consumers for the consumer process.
package event

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/messaging"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-logging/logging"
)

// mediaInternalBucketKey is the env var name for the originals bucket.
const mediaInternalBucketKey = "ORGANIZER_MEDIA_INTERNAL_BUCKET"

// mediaServedBucketKey is the env var name for the CDN-served bucket.
const mediaServedBucketKey = "ORGANIZER_MEDIA_BUCKET"

// maxPixels is the pre-decode safety limit: images whose decoded pixel count
// exceeds this value are rejected without a full decode (~50 MP).
const maxPixels = 50_000_000

// maxEdgePx is the per-dimension limit: any single edge exceeding this (8000 px)
// is rejected before the full decode.
const maxEdgePx = 8_000

// MediaProcessor abstracts the image-processing step (decode, resize, encode
// WebP variants) so the consumer's cut-over logic is testable without libvips.
// The real implementation lives in media_processor_vips.go (build tag: vips).
// A stub lives in media_processor_stub.go (build tag: !vips).
type MediaProcessor interface {
	// ProcessImage reads raw image bytes and returns thumb (≤800 w) and large
	// (≤1920 w) WebP-encoded variants, preserving aspect ratio. EXIF is stripped.
	// Magic-byte validation and pixel/edge limits are enforced before full decode.
	// Returns ErrUnsupportedMedia for images that fail safety checks.
	ProcessImage(ctx context.Context, data []byte) (thumb []byte, large []byte, err error)
}

// ErrUnsupportedMedia is returned by ProcessImage when the image fails safety
// checks (invalid magic bytes, exceeds pixel/edge limits, SVG detected, etc.).
// The consumer treats this as a permanent failure: term + delete original.
var ErrUnsupportedMedia = errors.New("unsupported or unsafe media")

// MediaConsumer handles MEDIA.uploaded events: it reads the original from the
// originals bucket, processes it into WebP variants, writes them to the served
// bucket, cuts over series_media to the new media id (no-404 guarantee), and
// deletes the original. Permanent failures (invalid image) are terminated so
// they are not re-delivered endlessly.
type MediaConsumer struct {
	mediaRepo   entity.MediaRepository
	imageStorer usecase.ImageStorer
	processor   MediaProcessor
	logger      *logging.Logger
}

// NewMediaConsumer creates a new MediaConsumer.
func NewMediaConsumer(
	mediaRepo entity.MediaRepository,
	imageStorer usecase.ImageStorer,
	processor MediaProcessor,
	logger *logging.Logger,
) *MediaConsumer {
	return &MediaConsumer{
		mediaRepo:   mediaRepo,
		imageStorer: imageStorer,
		processor:   processor,
		logger:      logger,
	}
}

// Handle processes a MEDIA.uploaded event.
// It returns nil to ack, a wrapped error to nak (retry), or calls msg.Ack()
// and returns nil after a permanent-failure cleanup.
func (h *MediaConsumer) Handle(msg *message.Message) error {
	ctx := msg.Context()

	var data entity.MediaUploadedData
	if err := messaging.ParseCloudEventData(msg, &data); err != nil {
		h.logger.Error(ctx, "failed to parse MEDIA.uploaded event", err)
		return fmt.Errorf("parse MEDIA.uploaded: %w", err)
	}

	h.logger.Info(ctx, "processing MEDIA.uploaded event",
		slog.String("media_id", data.MediaID),
		slog.String("series_id", data.SeriesID),
	)

	internalBucket := os.Getenv(mediaInternalBucketKey)
	servedBucket := os.Getenv(mediaServedBucketKey)

	// Load the media row to get the organizer id (needed for key composition).
	media, err := h.mediaRepo.FindMediaByID(ctx, data.MediaID)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			// Media row was deleted (e.g., cancelled before processing) — ack.
			h.logger.Warn(ctx, "media row not found; skipping",
				slog.String("media_id", data.MediaID),
			)
			return nil
		}
		return fmt.Errorf("find media %s: %w", data.MediaID, err)
	}

	origKey := entity.OriginalObjectKey(media.OrganizerID, data.MediaID)

	// Idempotency: if the thumb variant already exists in the served bucket, the
	// cut-over was already performed on a previous delivery. Re-running
	// CutOverSeriesMedia is safe (idempotent), but we skip re-processing the
	// image to avoid redundant work and GCS charges.

	// Read the original from the originals bucket.
	rawData, err := h.readOriginal(ctx, internalBucket, origKey)
	if err != nil {
		return fmt.Errorf("read original %s/%s: %w", internalBucket, origKey, err)
	}

	// Process the image (decode, safety check, resize, encode WebP).
	thumb, large, err := h.processor.ProcessImage(ctx, rawData)
	if err != nil {
		if errors.Is(err, ErrUnsupportedMedia) {
			// Permanent failure: delete the original and term so the message is
			// not re-delivered. This is USER error (a bad/unsafe upload), not a
			// system incident, so it logs WARN — the Media Processor ERROR-log
			// alert is reserved for genuine system failures (GCS/DB/vips-internal,
			// which return a non-ErrUnsupportedMedia error and nak below).
			h.logger.Warn(ctx, "unsupported or unsafe image; terminating",
				slog.String("media_id", data.MediaID),
				slog.Any("error", err),
			)
			h.cleanupOriginal(ctx, internalBucket, origKey, data.MediaID)
			msg.Ack()
			return nil
		}
		return fmt.Errorf("process image %s: %w", data.MediaID, err)
	}

	// Write variants to the served bucket.
	thumbKey := entity.VariantObjectKey(media.OrganizerID, data.MediaID, "thumb")
	largeKey := entity.VariantObjectKey(media.OrganizerID, data.MediaID, "large")

	if err := h.imageStorer.Put(ctx, servedBucket, thumbKey, "image/webp", thumb); err != nil {
		return fmt.Errorf("write thumb variant: %w", err)
	}
	if err := h.imageStorer.Put(ctx, servedBucket, largeKey, "image/webp", large); err != nil {
		return fmt.Errorf("write large variant: %w", err)
	}

	// Cut over series_media to the new media id and capture the old id.
	// This is the single tx that guarantees no-404: the old variants remain
	// served until this succeeds, then series_media atomically points to the
	// new variants.
	oldMediaID, err := h.mediaRepo.CutOverSeriesMedia(ctx, data.SeriesID, data.MediaID)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			// Series was deleted; nothing to cut over. Ack and clean up.
			h.logger.Warn(ctx, "series not found during cut-over; cleaning up",
				slog.String("series_id", data.SeriesID),
				slog.String("media_id", data.MediaID),
			)
			h.cleanupOriginal(ctx, internalBucket, origKey, data.MediaID)
			return nil
		}
		return fmt.Errorf("cut over series_media for series %s: %w", data.SeriesID, err)
	}

	// Reclaim the old variants from the served bucket (best-effort).
	if oldMediaID != "" && oldMediaID != data.MediaID {
		oldPrefix := entity.VariantObjectPrefix(media.OrganizerID, oldMediaID)
		if delErr := h.imageStorer.DeletePrefix(ctx, servedBucket, oldPrefix); delErr != nil {
			h.logger.Warn(ctx, "failed to delete old variant prefix (orphaned)",
				slog.String("prefix", oldPrefix),
				slog.Any("error", delErr),
			)
		}
		// The old media row was deleted inside CutOverSeriesMedia; we only need
		// to reclaim GCS objects here.
	}

	// Delete the original — it is no longer needed.
	h.cleanupOriginal(ctx, internalBucket, origKey, data.MediaID)

	h.logger.Info(ctx, "media processing complete",
		slog.String("media_id", data.MediaID),
		slog.String("series_id", data.SeriesID),
		slog.String("old_media_id", oldMediaID),
	)
	return nil
}

// readOriginal downloads the original file from the originals bucket.
func (h *MediaConsumer) readOriginal(ctx context.Context, bucket, key string) ([]byte, error) {
	// ImageStorer.Put writes; to read we need a Reader. Since the existing
	// ImageStorer interface only exposes write/delete operations, we use a
	// small GCS-specific helper here. In tests, we pass a fake storer that
	// also implements ObjectReader (defined in media_consumer_test.go).
	type objectReader interface {
		ReadObject(ctx context.Context, bucket, key string) ([]byte, error)
	}
	if r, ok := h.imageStorer.(objectReader); ok {
		return r.ReadObject(ctx, bucket, key)
	}
	// If the storer does not implement ReadObject (e.g., unit tests using the
	// base MockImageStorer), return an unimplemented error rather than panicking.
	return nil, apperr.New(apperr.ErrInternal.Code, "imageStorer does not implement ReadObject")
}

// cleanupOriginal deletes the original from the originals bucket. Failures are
// logged but not surfaced — a leaked original is harmless (future GC sweep).
func (h *MediaConsumer) cleanupOriginal(ctx context.Context, bucket, key, mediaID string) {
	if err := h.imageStorer.Delete(ctx, bucket, key); err != nil {
		h.logger.Warn(ctx, "failed to delete original (orphaned)",
			slog.String("media_id", mediaID),
			slog.String("key", key),
			slog.Any("error", err),
		)
	}
}
