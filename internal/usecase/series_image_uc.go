package usecase

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
)

// seriesImageUploadMaxBytes is the maximum accepted upload size for a series image
// (10 MiB). This value is returned to the client as max_bytes in
// CreateUploadUrlResponse and is enforced by the signed-URL condition
// x-goog-content-length-range:0,<max_bytes>.
const seriesImageUploadMaxBytes int64 = 10 * 1024 * 1024

// signedURLTTL is the lifetime of a signed PUT URL returned by
// CreateUploadURL. 15 minutes gives a reasonable upload window while
// limiting the exposure of a leaked URL.
const signedURLTTL = 15 * time.Minute

// storageInternalBucketEnv is the environment variable name for the GCS bucket
// that holds raw originals uploaded by organizers.
const storageInternalBucketEnv = "STORAGE_INTERNAL_BUCKET"

// storageBucketEnv is the environment variable name for the CDN-served
// GCS bucket that holds the processed thumb/large WebP variants.
const storageBucketEnv = "STORAGE_BUCKET"

// allowedImageContentTypes is the set of accepted MIME types for organizer
// image upload. Only image/* types are allowed at MVP; SVG is explicitly
// excluded because it cannot be safely decoded by libvips without SVG-specific
// handling and is a common vector for XXE/SSRF attacks.
var allowedImageContentTypes = map[string]struct{}{
	"image/jpeg": {},
	"image/png":  {},
	"image/webp": {},
}

// storageInternalBucket returns the originals-bucket name from the environment.
func storageInternalBucket() string {
	return os.Getenv(storageInternalBucketEnv)
}

// CreateSeriesImageUploadURLInput carries the parameters for CreateUploadURL.
type CreateSeriesImageUploadURLInput struct {
	// ContentType is the MIME type declared by the client. Validated against
	// the allowed-types allowlist before the signed URL is issued.
	ContentType string
}

// CreateSeriesImageUploadURLOutput is the result of CreateUploadURL.
type CreateSeriesImageUploadURLOutput struct {
	// UploadURL is the V4-signed GCS PUT URL. The client must PUT the file to
	// this URL exactly as described: Content-Type and
	// x-goog-content-length-range headers must match what was signed.
	UploadURL string
	// SeriesImageID is the server-minted UUIDv7 identifier. The client passes it
	// back to SetCoverImage after the upload completes.
	SeriesImageID string
	// MaxBytes is the maximum upload size enforced by the signed URL condition.
	MaxBytes int64
}

// SeriesImageUseCase defines the organizer series image surface for the
// two-step direct-to-storage upload flow (CreateUploadURL → upload to
// GCS → SetCoverImage).
type SeriesImageUseCase interface {
	// CreateUploadURL validates the content type, mints a new series image id
	// (UUIDv7), and returns a V4-signed GCS PUT URL for the ORIGINALS bucket
	// together with the series image id and the maximum allowed bytes. The caller's
	// organizer id is the tenant segment of the object key; no series is
	// referenced at this stage.
	//
	// # Possible errors
	//
	//  - InvalidArgument: if the content type is not accepted.
	//  - Internal: if STORAGE_INTERNAL_BUCKET is unset or signing fails.
	CreateUploadURL(ctx context.Context, callerOrgID string, in CreateSeriesImageUploadURLInput) (*CreateSeriesImageUploadURLOutput, error)

	// Process reads the original upload from the internal bucket,
	// validates and transcodes it into WebP thumb/large variants, switches
	// the series' cover to the new image, and reclaims the replaced cover and
	// the original upload. It is triggered by the SERIES_IMAGE.uploaded event; the
	// event consumer only decodes the message and calls this method.
	//
	// A missing series image row (upload cancelled before processing) or a missing
	// series (deleted before the cut-over) is not an error: the original is
	// cleaned up and processing ends silently. An unsupported/unsafe image
	// (entity.ErrUnsupportedImage) is a permanent failure: the original is
	// deleted and nil is returned so the event is not retried. Every other
	// failure is returned so the caller can retry.
	//
	// # Possible errors
	//
	//  - Internal: reading the original, processing the image, writing
	//    variants, or the series cut-over fails for a reason other than the
	//    permanent/missing-row cases above.
	Process(ctx context.Context, seriesImageID, seriesID string) error
}

// seriesImageUseCase implements SeriesImageUseCase.
type seriesImageUseCase struct {
	seriesRepo      entity.SeriesRepository
	seriesImageRepo entity.SeriesImageRepository
	imageStorer     ImageStorer
	processor       entity.SeriesImageProcessor
	publisher       EventPublisher
	logger          *logging.Logger
}

// Compile-time interface check.
var _ SeriesImageUseCase = (*seriesImageUseCase)(nil)

// NewSeriesImageUseCase creates a new SeriesImageUseCase. imageStorer may be nil in local
// development; CreateUploadURL returns Internal in that case rather than
// panicking at boot.
func NewSeriesImageUseCase(
	seriesRepo entity.SeriesRepository,
	seriesImageRepo entity.SeriesImageRepository,
	imageStorer ImageStorer,
	processor entity.SeriesImageProcessor,
	publisher EventPublisher,
	logger *logging.Logger,
) SeriesImageUseCase {
	return &seriesImageUseCase{
		seriesRepo:      seriesRepo,
		seriesImageRepo: seriesImageRepo,
		imageStorer:     imageStorer,
		processor:       processor,
		publisher:       publisher,
		logger:          logger,
	}
}

// CreateUploadURL validates the content type, mints a UUIDv7 series image id,
// and returns a V4-signed PUT URL for the originals bucket.
func (uc *seriesImageUseCase) CreateUploadURL(ctx context.Context, callerOrgID string, in CreateSeriesImageUploadURLInput) (*CreateSeriesImageUploadURLOutput, error) {
	ct := strings.ToLower(strings.TrimSpace(in.ContentType))
	if _, ok := allowedImageContentTypes[ct]; !ok {
		return nil, apperr.New(codes.InvalidArgument,
			fmt.Sprintf("unsupported content type %q; accepted: image/jpeg, image/png, image/webp", in.ContentType),
		)
	}

	if uc.imageStorer == nil {
		return nil, apperr.New(codes.Internal, "image storage is not configured")
	}

	bucket := storageInternalBucket()
	if bucket == "" {
		return nil, apperr.New(codes.Internal, "STORAGE_INTERNAL_BUCKET is not set")
	}

	// Mint the series image id now so it is the unique key for both the signed URL
	// and the later SetCoverImage call. UUIDv7 provides monotone ordering and
	// an embedded creation timestamp.
	seriesImageID := entity.NewID()

	uploadURL, err := uc.imageStorer.SignedPutURLForOriginal(ctx, bucket, callerOrgID, seriesImageID, ct, seriesImageUploadMaxBytes, signedURLTTL)
	if err != nil {
		return nil, fmt.Errorf("sign put url: %w", err)
	}

	uc.logger.Info(ctx, "image upload URL issued",
		slog.String("organizer_id", callerOrgID),
		slog.String("series_image_id", seriesImageID),
		slog.String("content_type", ct),
	)

	return &CreateSeriesImageUploadURLOutput{
		UploadURL:     uploadURL,
		SeriesImageID: seriesImageID,
		MaxBytes:      seriesImageUploadMaxBytes,
	}, nil
}

// Process reads the original from the internal bucket, processes it into
// WebP variants, writes them to the served bucket, cuts over series_cover_images to
// the new series image id (no-404 guarantee), and deletes the original. Permanent
// failures (invalid image, missing series image or series row) end processing without
// an error so the SERIES_IMAGE.uploaded event is not redelivered endlessly.
func (uc *seriesImageUseCase) Process(ctx context.Context, seriesImageID, seriesID string) error {
	internalBucket := os.Getenv(storageInternalBucketEnv)
	servedBucket := os.Getenv(storageBucketEnv)

	// Load the series image row to get the organizer id the storage keys are scoped to.
	seriesImage, err := uc.seriesImageRepo.Get(ctx, seriesImageID)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			// SeriesImage row was deleted (e.g., cancelled before processing) — ack.
			uc.logger.Warn(ctx, "series image row not found; skipping",
				slog.String("series_image_id", seriesImageID),
			)
			return nil
		}
		return fmt.Errorf("find series image %s: %w", seriesImageID, err)
	}

	// Read the original from the originals bucket.
	rawData, err := uc.imageStorer.ReadOriginal(ctx, internalBucket, seriesImage.OrganizerID, seriesImageID)
	if err != nil {
		return fmt.Errorf("read original for series image %s (org %s): %w", seriesImageID, seriesImage.OrganizerID, err)
	}

	// Process the image (decode, safety check, resize, encode WebP).
	thumb, large, err := uc.processor.ProcessImage(ctx, rawData)
	if err != nil {
		if errors.Is(err, entity.ErrUnsupportedImage) {
			// Permanent failure: delete the original and stop so the message is
			// not re-delivered. This is USER error (a bad/unsafe upload), not a
			// system incident, so it logs WARN.
			uc.logger.Warn(ctx, "unsupported or unsafe image; terminating",
				slog.String("series_image_id", seriesImageID),
				slog.Any("error", err),
			)
			uc.cleanupOriginal(ctx, internalBucket, seriesImage.OrganizerID, seriesImageID)
			return nil
		}
		return fmt.Errorf("process image %s: %w", seriesImageID, err)
	}

	// Write variants to the served bucket.
	if err := uc.imageStorer.PutVariant(ctx, servedBucket, seriesImage.OrganizerID, seriesImageID, "thumb", "image/webp", thumb); err != nil {
		return fmt.Errorf("write thumb variant: %w", err)
	}
	if err := uc.imageStorer.PutVariant(ctx, servedBucket, seriesImage.OrganizerID, seriesImageID, "large", "image/webp", large); err != nil {
		return fmt.Errorf("write large variant: %w", err)
	}

	// Cut over series_cover_images to the new series image id and capture the old id.
	// This is the single tx that guarantees no-404: the old variants remain
	// served until this succeeds, then series_cover_images atomically points to the
	// new variants.
	oldSeriesImageID, err := uc.seriesImageRepo.ReplaceCover(ctx, seriesID, seriesImageID)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			// Series was deleted; nothing to cut over. Clean up and stop.
			uc.logger.Warn(ctx, "series not found during cut-over; cleaning up",
				slog.String("series_id", seriesID),
				slog.String("series_image_id", seriesImageID),
			)
			uc.cleanupOriginal(ctx, internalBucket, seriesImage.OrganizerID, seriesImageID)
			return nil
		}
		return fmt.Errorf("cut over series_cover_images for series %s: %w", seriesID, err)
	}

	// Reclaim the old variants from the served bucket (best-effort). The old
	// series image row was deleted inside ReplaceCover; only the objects remain.
	if oldSeriesImageID != "" && oldSeriesImageID != seriesImageID {
		if delErr := uc.imageStorer.DeleteVariants(ctx, servedBucket, seriesImage.OrganizerID, oldSeriesImageID); delErr != nil {
			uc.logger.Warn(ctx, "failed to delete old variants (orphaned)",
				slog.String("organizer_id", seriesImage.OrganizerID),
				slog.String("old_series_image_id", oldSeriesImageID),
				slog.Any("error", delErr),
			)
		}
	}

	// Delete the original — it is no longer needed.
	uc.cleanupOriginal(ctx, internalBucket, seriesImage.OrganizerID, seriesImageID)

	uc.logger.Info(ctx, "image processing complete",
		slog.String("series_image_id", seriesImageID),
		slog.String("series_id", seriesID),
		slog.String("old_series_image_id", oldSeriesImageID),
	)
	return nil
}

// cleanupOriginal deletes the original from the originals bucket. Failures are
// logged but not surfaced — a leaked original is harmless (future GC sweep).
func (uc *seriesImageUseCase) cleanupOriginal(ctx context.Context, bucket, organizerID, seriesImageID string) {
	if err := uc.imageStorer.DeleteOriginal(ctx, bucket, organizerID, seriesImageID); err != nil {
		uc.logger.Warn(ctx, "failed to delete original (orphaned)",
			slog.String("organizer_id", organizerID),
			slog.String("series_image_id", seriesImageID),
			slog.Any("error", err),
		)
	}
}
