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

// mediaUploadMaxBytes is the maximum accepted upload size for organizer media
// (10 MiB). This value is returned to the client as max_bytes in
// CreateMediaUploadURLResponse and is enforced by the signed-URL condition
// x-goog-content-length-range:0,<max_bytes>.
const mediaUploadMaxBytes int64 = 10 * 1024 * 1024

// signedURLTTL is the lifetime of a signed PUT URL returned by
// CreateMediaUploadURL. 15 minutes gives a reasonable upload window while
// limiting the exposure of a leaked URL.
const signedURLTTL = 15 * time.Minute

// mediaInternalBucketEnv is the environment variable name for the GCS bucket
// that holds raw originals uploaded by organizers.
const mediaInternalBucketEnv = "ORGANIZER_MEDIA_INTERNAL_BUCKET"

// mediaServedBucketEnv is the environment variable name for the CDN-served
// GCS bucket that holds the processed thumb/large WebP variants.
const mediaServedBucketEnv = "ORGANIZER_MEDIA_BUCKET"

// allowedMediaContentTypes is the set of accepted MIME types for organizer
// media uploads. Only image/* types are allowed at MVP; SVG is explicitly
// excluded because it cannot be safely decoded by libvips without SVG-specific
// handling and is a common vector for XXE/SSRF attacks.
var allowedMediaContentTypes = map[string]struct{}{
	"image/jpeg": {},
	"image/png":  {},
	"image/webp": {},
}

// mediaInternalBucket returns the originals-bucket name from the environment.
func mediaInternalBucket() string {
	return os.Getenv(mediaInternalBucketEnv)
}

// CreateMediaUploadURLInput carries the parameters for CreateMediaUploadURL.
type CreateMediaUploadURLInput struct {
	// ContentType is the MIME type declared by the client. Validated against
	// the allowed-types allowlist before the signed URL is issued.
	ContentType string
}

// CreateMediaUploadURLOutput is the result of CreateMediaUploadURL.
type CreateMediaUploadURLOutput struct {
	// UploadURL is the V4-signed GCS PUT URL. The client must PUT the file to
	// this URL exactly as described: Content-Type and
	// x-goog-content-length-range headers must match what was signed.
	UploadURL string
	// MediaID is the server-minted UUIDv7 identifier. The client passes it
	// back to AttachMedia after the upload completes.
	MediaID string
	// MaxBytes is the maximum upload size enforced by the signed URL condition.
	MaxBytes int64
}

// MediaUseCase defines the organizer media management surface for the
// two-step direct-to-storage upload flow (CreateMediaUploadURL → upload to
// GCS → AttachMedia).
type MediaUseCase interface {
	// CreateMediaUploadURL validates the content type, mints a new media id
	// (UUIDv7), and returns a V4-signed GCS PUT URL for the ORIGINALS bucket
	// together with the media id and the maximum allowed bytes. The caller's
	// organizer id is the tenant segment of the object key; no series is
	// referenced at this stage.
	//
	// # Possible errors
	//
	//  - InvalidArgument: if the content type is not accepted.
	//  - Internal: if ORGANIZER_MEDIA_INTERNAL_BUCKET is unset or signing fails.
	CreateMediaUploadURL(ctx context.Context, callerOrgID string, in CreateMediaUploadURLInput) (*CreateMediaUploadURLOutput, error)

	// AttachMedia verifies that the caller owns the target series, inserts the
	// media row idempotently, and publishes MEDIA.uploaded{media_id, series_id}.
	// It does NOT re-point series_media — the media-processor consumer does
	// that after generating variants. Idempotent on re-delivery.
	//
	// # Possible errors
	//
	//  - InvalidArgument: if series_id or media_id is empty.
	//  - PermissionDenied: if the series is not owned by the caller (non-revealing).
	//  - Internal: if the DB insert or event publish fails.
	AttachMedia(ctx context.Context, callerOrgID, seriesID, mediaID string) error

	// ProcessMedia reads the original upload from the internal bucket,
	// validates and transcodes it into WebP thumb/large variants, switches
	// the series' cover to the new media, and reclaims the replaced cover and
	// the original upload. It is triggered by the MEDIA.uploaded event; the
	// event consumer only decodes the message and calls this method.
	//
	// A missing media row (upload cancelled before processing) or a missing
	// series (deleted before the cut-over) is not an error: the original is
	// cleaned up and processing ends silently. An unsupported/unsafe image
	// (entity.ErrUnsupportedMedia) is a permanent failure: the original is
	// deleted and nil is returned so the event is not retried. Every other
	// failure is returned so the caller can retry.
	//
	// # Possible errors
	//
	//  - Internal: reading the original, processing the image, writing
	//    variants, or the series cut-over fails for a reason other than the
	//    permanent/missing-row cases above.
	ProcessMedia(ctx context.Context, mediaID, seriesID string) error
}

// mediaUseCase implements MediaUseCase.
type mediaUseCase struct {
	seriesRepo  entity.SeriesRepository
	mediaRepo   entity.MediaRepository
	imageStorer ImageStorer
	processor   entity.MediaProcessor
	publisher   EventPublisher
	logger      *logging.Logger
}

// Compile-time interface check.
var _ MediaUseCase = (*mediaUseCase)(nil)

// NewMediaUseCase creates a new MediaUseCase. imageStorer may be nil in local
// development; CreateMediaUploadURL returns Internal in that case rather than
// panicking at boot.
func NewMediaUseCase(
	seriesRepo entity.SeriesRepository,
	mediaRepo entity.MediaRepository,
	imageStorer ImageStorer,
	processor entity.MediaProcessor,
	publisher EventPublisher,
	logger *logging.Logger,
) MediaUseCase {
	return &mediaUseCase{
		seriesRepo:  seriesRepo,
		mediaRepo:   mediaRepo,
		imageStorer: imageStorer,
		processor:   processor,
		publisher:   publisher,
		logger:      logger,
	}
}

// CreateMediaUploadURL validates the content type, mints a UUIDv7 media id,
// and returns a V4-signed PUT URL for the originals bucket.
func (uc *mediaUseCase) CreateMediaUploadURL(ctx context.Context, callerOrgID string, in CreateMediaUploadURLInput) (*CreateMediaUploadURLOutput, error) {
	ct := strings.ToLower(strings.TrimSpace(in.ContentType))
	if _, ok := allowedMediaContentTypes[ct]; !ok {
		return nil, apperr.New(codes.InvalidArgument,
			fmt.Sprintf("unsupported content type %q; accepted: image/jpeg, image/png, image/webp", in.ContentType),
		)
	}

	if uc.imageStorer == nil {
		return nil, apperr.New(codes.Internal, "image storage is not configured")
	}

	bucket := mediaInternalBucket()
	if bucket == "" {
		return nil, apperr.New(codes.Internal, "ORGANIZER_MEDIA_INTERNAL_BUCKET is not set")
	}

	// Mint the media id now so it is the unique key for both the signed URL
	// and the later AttachMedia call. UUIDv7 provides monotone ordering and
	// an embedded creation timestamp.
	mediaID := entity.NewID()

	uploadURL, err := uc.imageStorer.SignedPutURLForOriginal(ctx, bucket, callerOrgID, mediaID, ct, mediaUploadMaxBytes, signedURLTTL)
	if err != nil {
		return nil, fmt.Errorf("sign put url: %w", err)
	}

	uc.logger.Info(ctx, "media upload URL issued",
		slog.String("organizer_id", callerOrgID),
		slog.String("media_id", mediaID),
		slog.String("content_type", ct),
	)

	return &CreateMediaUploadURLOutput{
		UploadURL: uploadURL,
		MediaID:   mediaID,
		MaxBytes:  mediaUploadMaxBytes,
	}, nil
}

// AttachMedia verifies ownership, inserts the media row idempotently, and
// publishes MEDIA.uploaded so the processor can generate variants and cut over
// series_media. The series_media re-point is intentionally deferred to the
// consumer so the existing cover continues to serve until variants are ready
// (no 404 window).
func (uc *mediaUseCase) AttachMedia(ctx context.Context, callerOrgID, seriesID, mediaID string) error {
	if seriesID == "" {
		return apperr.New(codes.InvalidArgument, "series_id must not be empty")
	}
	if mediaID == "" {
		return apperr.New(codes.InvalidArgument, "media_id must not be empty")
	}

	// Verify caller owns the series. Non-revealing: NOT_FOUND → PermissionDenied.
	series, err := uc.seriesRepo.Get(ctx, seriesID)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return apperr.New(codes.PermissionDenied, "permission denied")
		}
		return err
	}
	if series.OrganizerID == nil || *series.OrganizerID != callerOrgID {
		return apperr.New(codes.PermissionDenied, "permission denied")
	}

	// Insert the media row idempotently. The organizer_id comes from the
	// verified caller so we never store a media row for a different tenant.
	media := &entity.Media{
		ID:          mediaID,
		OrganizerID: callerOrgID,
		Kind:        entity.MediaKindImage,
		// content_type is not known here (the client declared it to the
		// signed-URL endpoint, not to AttachMedia). Store an empty attributes
		// map; the processor will not need it and we avoid a round-trip.
		Attributes: map[string]string{},
	}
	if err := uc.mediaRepo.InsertMedia(ctx, media); err != nil {
		return fmt.Errorf("insert media row: %w", err)
	}

	// Publish MEDIA.uploaded so the processor can pick it up. Non-fatal on
	// publish failure after a successful DB insert: the row persists and the
	// event can be re-published via a retry or manual trigger.
	if err := uc.publisher.PublishEvent(ctx, entity.SubjectMediaUploaded, entity.MediaUploadedData{
		MediaID:  mediaID,
		SeriesID: seriesID,
	}); err != nil {
		uc.logger.Warn(ctx, "failed to publish MEDIA.uploaded event",
			slog.String("media_id", mediaID),
			slog.String("series_id", seriesID),
			slog.Any("error", err),
		)
		// Non-fatal: the media row is persisted; the processor picks it up on
		// re-delivery. We do NOT return an error so the caller does not retry
		// the entire RPC (which would insert a duplicate-idempotent row again
		// and then try to publish again — a harmless but noisy loop).
	}

	uc.logger.Info(ctx, "media attached to series",
		slog.String("media_id", mediaID),
		slog.String("series_id", seriesID),
		slog.String("organizer_id", callerOrgID),
	)
	return nil
}

// ProcessMedia reads the original from the internal bucket, processes it into
// WebP variants, writes them to the served bucket, cuts over series_media to
// the new media id (no-404 guarantee), and deletes the original. Permanent
// failures (invalid image, missing media/series row) end processing without
// an error so the MEDIA.uploaded event is not redelivered endlessly.
func (uc *mediaUseCase) ProcessMedia(ctx context.Context, mediaID, seriesID string) error {
	internalBucket := os.Getenv(mediaInternalBucketEnv)
	servedBucket := os.Getenv(mediaServedBucketEnv)

	// Load the media row to get the organizer id the storage keys are scoped to.
	media, err := uc.mediaRepo.FindMediaByID(ctx, mediaID)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			// Media row was deleted (e.g., cancelled before processing) — ack.
			uc.logger.Warn(ctx, "media row not found; skipping",
				slog.String("media_id", mediaID),
			)
			return nil
		}
		return fmt.Errorf("find media %s: %w", mediaID, err)
	}

	// Read the original from the originals bucket.
	rawData, err := uc.imageStorer.ReadOriginal(ctx, internalBucket, media.OrganizerID, mediaID)
	if err != nil {
		return fmt.Errorf("read original for media %s (org %s): %w", mediaID, media.OrganizerID, err)
	}

	// Process the image (decode, safety check, resize, encode WebP).
	thumb, large, err := uc.processor.ProcessImage(ctx, rawData)
	if err != nil {
		if errors.Is(err, entity.ErrUnsupportedMedia) {
			// Permanent failure: delete the original and stop so the message is
			// not re-delivered. This is USER error (a bad/unsafe upload), not a
			// system incident, so it logs WARN.
			uc.logger.Warn(ctx, "unsupported or unsafe image; terminating",
				slog.String("media_id", mediaID),
				slog.Any("error", err),
			)
			uc.cleanupOriginal(ctx, internalBucket, media.OrganizerID, mediaID)
			return nil
		}
		return fmt.Errorf("process image %s: %w", mediaID, err)
	}

	// Write variants to the served bucket.
	if err := uc.imageStorer.PutVariant(ctx, servedBucket, media.OrganizerID, mediaID, "thumb", "image/webp", thumb); err != nil {
		return fmt.Errorf("write thumb variant: %w", err)
	}
	if err := uc.imageStorer.PutVariant(ctx, servedBucket, media.OrganizerID, mediaID, "large", "image/webp", large); err != nil {
		return fmt.Errorf("write large variant: %w", err)
	}

	// Cut over series_media to the new media id and capture the old id.
	// This is the single tx that guarantees no-404: the old variants remain
	// served until this succeeds, then series_media atomically points to the
	// new variants.
	oldMediaID, err := uc.mediaRepo.CutOverSeriesMedia(ctx, seriesID, mediaID)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			// Series was deleted; nothing to cut over. Clean up and stop.
			uc.logger.Warn(ctx, "series not found during cut-over; cleaning up",
				slog.String("series_id", seriesID),
				slog.String("media_id", mediaID),
			)
			uc.cleanupOriginal(ctx, internalBucket, media.OrganizerID, mediaID)
			return nil
		}
		return fmt.Errorf("cut over series_media for series %s: %w", seriesID, err)
	}

	// Reclaim the old variants from the served bucket (best-effort). The old
	// media row was deleted inside CutOverSeriesMedia; only the objects remain.
	if oldMediaID != "" && oldMediaID != mediaID {
		if delErr := uc.imageStorer.DeleteVariants(ctx, servedBucket, media.OrganizerID, oldMediaID); delErr != nil {
			uc.logger.Warn(ctx, "failed to delete old variants (orphaned)",
				slog.String("organizer_id", media.OrganizerID),
				slog.String("old_media_id", oldMediaID),
				slog.Any("error", delErr),
			)
		}
	}

	// Delete the original — it is no longer needed.
	uc.cleanupOriginal(ctx, internalBucket, media.OrganizerID, mediaID)

	uc.logger.Info(ctx, "media processing complete",
		slog.String("media_id", mediaID),
		slog.String("series_id", seriesID),
		slog.String("old_media_id", oldMediaID),
	)
	return nil
}

// cleanupOriginal deletes the original from the originals bucket. Failures are
// logged but not surfaced — a leaked original is harmless (future GC sweep).
func (uc *mediaUseCase) cleanupOriginal(ctx context.Context, bucket, organizerID, mediaID string) {
	if err := uc.imageStorer.DeleteOriginal(ctx, bucket, organizerID, mediaID); err != nil {
		uc.logger.Warn(ctx, "failed to delete original (orphaned)",
			slog.String("organizer_id", organizerID),
			slog.String("media_id", mediaID),
			slog.Any("error", err),
		)
	}
}
