package usecase_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	entitymocks "github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/usecase"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// fakeImageStorer is a fully controllable in-memory usecase.ImageStorer.
// The real key scheme belongs to the storage infrastructure, so the fake
// addresses objects with its own test-local keys (fakeOriginalKey,
// fakeVariantKey, fakeVariantPrefix) derived from the same identities.
type fakeImageStorer struct {
	objects      map[string][]byte
	deletedKeys  []string
	deletedPfxs  []string
	putErr       error
	deleteErr    error
	deletePfxErr error
}

func newFakeImageStorer() *fakeImageStorer {
	return &fakeImageStorer{objects: make(map[string][]byte)}
}

func fakeOriginalKey(organizerID, mediaID string) string {
	return "originals/" + organizerID + "/" + mediaID
}

func fakeVariantKey(organizerID, mediaID, variant string) string {
	return fakeVariantPrefix(organizerID, mediaID) + variant
}

func fakeVariantPrefix(organizerID, mediaID string) string {
	return "variants/" + organizerID + "/" + mediaID + "/"
}

func (f *fakeImageStorer) SignedPutURLForOriginal(_ context.Context, _, _, _, _ string, _ int64, _ time.Duration) (string, error) {
	return "https://signed", nil
}

func (f *fakeImageStorer) ReadOriginal(_ context.Context, bucket, organizerID, mediaID string) ([]byte, error) {
	key := bucket + "/" + fakeOriginalKey(organizerID, mediaID)
	data, ok := f.objects[key]
	if !ok {
		return nil, apperr.New(codes.NotFound, "object not found: "+key)
	}
	return data, nil
}

func (f *fakeImageStorer) DeleteOriginal(_ context.Context, bucket, organizerID, mediaID string) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	f.deletedKeys = append(f.deletedKeys, bucket+"/"+fakeOriginalKey(organizerID, mediaID))
	return nil
}

func (f *fakeImageStorer) PutVariant(_ context.Context, bucket, organizerID, mediaID, variant, _ string, data []byte) error {
	if f.putErr != nil {
		return f.putErr
	}
	f.objects[bucket+"/"+fakeVariantKey(organizerID, mediaID, variant)] = data
	return nil
}

func (f *fakeImageStorer) DeleteVariants(_ context.Context, bucket, organizerID, mediaID string) error {
	if f.deletePfxErr != nil {
		return f.deletePfxErr
	}
	f.deletedPfxs = append(f.deletedPfxs, bucket+"/"+fakeVariantPrefix(organizerID, mediaID))
	return nil
}

// mediaDeps wires up a MediaUseCase with mocks for every dependency.
type mediaDeps struct {
	seriesRepo *entitymocks.MockSeriesRepository
	mediaRepo  *entitymocks.MockMediaRepository
	storer     *fakeImageStorer
	processor  *entitymocks.MockMediaProcessor
	publisher  *ucmocks.MockEventPublisher
	uc         usecase.MediaUseCase
}

func newMediaDeps(t *testing.T) *mediaDeps {
	t.Helper()
	logger := newTestLogger(t)
	d := &mediaDeps{
		seriesRepo: entitymocks.NewMockSeriesRepository(t),
		mediaRepo:  entitymocks.NewMockMediaRepository(t),
		storer:     newFakeImageStorer(),
		processor:  entitymocks.NewMockMediaProcessor(t),
		publisher:  ucmocks.NewMockEventPublisher(t),
	}
	d.uc = usecase.NewMediaUseCase(
		d.seriesRepo, d.mediaRepo, d.storer, d.processor, d.publisher, logger,
	)
	return d
}

// --- CreateMediaUploadURL / AttachMedia tests ---

// TestMediaUseCase_CreateMediaUploadURL_IssuesSignedURL verifies that a valid
// content type triggers a signed PUT URL and returns the media id + max bytes.
func TestMediaUseCase_CreateMediaUploadURL_IssuesSignedURL(t *testing.T) {
	// t.Setenv requires sequential execution.
	t.Setenv("ORGANIZER_MEDIA_INTERNAL_BUCKET", "originals-bucket")
	ctx := context.Background()
	d := newMediaDeps(t)

	const orgID = "org-1"

	out, err := d.uc.CreateMediaUploadURL(ctx, orgID, usecase.CreateMediaUploadURLInput{ContentType: "image/jpeg"})
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(out.UploadURL, "https://signed"))
	assert.NotEmpty(t, out.MediaID)
	assert.Equal(t, int64(10*1024*1024), out.MaxBytes)
}

// TestMediaUseCase_CreateMediaUploadURL_RejectsInvalidType verifies that a
// non-allowlisted content type returns InvalidArgument before any GCS call.
func TestMediaUseCase_CreateMediaUploadURL_RejectsInvalidType(t *testing.T) {
	ctx := context.Background()
	d := newMediaDeps(t)

	_, err := d.uc.CreateMediaUploadURL(ctx, "org-1", usecase.CreateMediaUploadURLInput{ContentType: "image/svg+xml"})
	require.Error(t, err)
	assert.True(t, errors.Is(err, apperr.ErrInvalidArgument), "expected InvalidArgument, got %v", err)
}

// TestMediaUseCase_CreateMediaUploadURL_MissingBucket verifies a clear Internal
// error when ORGANIZER_MEDIA_INTERNAL_BUCKET is unset.
func TestMediaUseCase_CreateMediaUploadURL_MissingBucket(t *testing.T) {
	// t.Setenv requires sequential execution.
	t.Setenv("ORGANIZER_MEDIA_INTERNAL_BUCKET", "")
	ctx := context.Background()
	d := newMediaDeps(t)

	_, err := d.uc.CreateMediaUploadURL(ctx, "org-1", usecase.CreateMediaUploadURLInput{ContentType: "image/png"})
	require.Error(t, err)
	assert.True(t, errors.Is(err, apperr.ErrInternal), "expected Internal, got %v", err)
}

// TestMediaUseCase_AttachMedia_InsertsAndPublishes verifies the happy path:
// ownership check passes, media row is inserted, event is published.
func TestMediaUseCase_AttachMedia_InsertsAndPublishes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := newMediaDeps(t)

	const (
		orgID    = "org-1"
		seriesID = "series-1"
		mediaID  = "media-1"
	)
	s := &entity.Series{ID: seriesID, OrganizerID: new(orgID)}
	d.seriesRepo.EXPECT().Get(mock.Anything, seriesID).Return(s, nil)
	d.mediaRepo.EXPECT().InsertMedia(mock.Anything, mock.MatchedBy(func(m *entity.Media) bool {
		return m.ID == mediaID && m.OrganizerID == orgID && m.Kind == entity.MediaKindImage
	})).Return(nil)
	d.publisher.EXPECT().PublishEvent(mock.Anything, entity.SubjectMediaUploaded, mock.Anything).Return(nil)

	err := d.uc.AttachMedia(ctx, orgID, seriesID, mediaID)
	require.NoError(t, err)
}

// TestMediaUseCase_AttachMedia_NonOwnerDenied verifies that a caller who does
// not own the series receives PermissionDenied (non-revealing).
func TestMediaUseCase_AttachMedia_NonOwnerDenied(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := newMediaDeps(t)

	const (
		orgID    = "org-other"
		seriesID = "series-1"
		mediaID  = "media-1"
	)
	// Series is owned by a different organizer.
	s := &entity.Series{ID: seriesID, OrganizerID: new("org-owner")}
	d.seriesRepo.EXPECT().Get(mock.Anything, seriesID).Return(s, nil)

	err := d.uc.AttachMedia(ctx, orgID, seriesID, mediaID)
	require.Error(t, err)
	assert.True(t, errors.Is(err, apperr.ErrPermissionDenied), "expected PermissionDenied, got %v", err)
}

// TestMediaUseCase_AttachMedia_Idempotent verifies that a second AttachMedia
// for the same media_id succeeds (InsertMedia is ON CONFLICT DO NOTHING).
func TestMediaUseCase_AttachMedia_Idempotent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := newMediaDeps(t)

	const (
		orgID    = "org-1"
		seriesID = "series-1"
		mediaID  = "media-dup"
	)
	s := &entity.Series{ID: seriesID, OrganizerID: new(orgID)}
	// Both calls use the same mock stubs — idempotent at the DB layer.
	d.seriesRepo.EXPECT().Get(mock.Anything, seriesID).Return(s, nil).Times(2)
	d.mediaRepo.EXPECT().InsertMedia(mock.Anything, mock.Anything).Return(nil).Times(2)
	d.publisher.EXPECT().PublishEvent(mock.Anything, entity.SubjectMediaUploaded, mock.Anything).Return(nil).Times(2)

	require.NoError(t, d.uc.AttachMedia(ctx, orgID, seriesID, mediaID))
	require.NoError(t, d.uc.AttachMedia(ctx, orgID, seriesID, mediaID))
}

// --- ProcessMedia tests ---
// None of these use t.Parallel() because they all use t.Setenv, which
// requires sequential execution.

// @spec components/usecase/media/process-media "Replacing a published cover"
func TestMediaUseCase_ProcessMedia_HappyPath(t *testing.T) {
	t.Setenv("ORGANIZER_MEDIA_INTERNAL_BUCKET", "originals")
	t.Setenv("ORGANIZER_MEDIA_BUCKET", "served")

	const (
		mediaID  = "media-new"
		seriesID = "series-1"
		orgID    = "org-1"
	)
	d := newMediaDeps(t)

	origKey := fakeOriginalKey(orgID, mediaID)
	d.storer.objects["originals/"+origKey] = []byte("raw-image-bytes")

	media := &entity.Media{ID: mediaID, OrganizerID: orgID, Kind: entity.MediaKindImage}
	d.mediaRepo.EXPECT().FindMediaByID(mock.Anything, mediaID).Return(media, nil)
	d.processor.EXPECT().ProcessImage(mock.Anything, []byte("raw-image-bytes")).
		Return([]byte("thumb-webp"), []byte("large-webp"), nil)
	d.mediaRepo.EXPECT().CutOverSeriesMedia(mock.Anything, seriesID, mediaID).Return("", nil)

	require.NoError(t, d.uc.ProcessMedia(context.Background(), mediaID, seriesID))

	thumbKey := "served/" + fakeVariantKey(orgID, mediaID, "thumb")
	largeKey := "served/" + fakeVariantKey(orgID, mediaID, "large")
	assert.Equal(t, []byte("thumb-webp"), d.storer.objects[thumbKey], "thumb must be written")
	assert.Equal(t, []byte("large-webp"), d.storer.objects[largeKey], "large must be written")
	assert.Contains(t, d.storer.deletedKeys, "originals/"+origKey, "original must be deleted")
}

// TestMediaUseCase_ProcessMedia_ReplacesOldMedia verifies that when cut-over
// returns an old media id, the old variant prefix is deleted from the served
// bucket.
func TestMediaUseCase_ProcessMedia_ReplacesOldMedia(t *testing.T) {
	t.Setenv("ORGANIZER_MEDIA_INTERNAL_BUCKET", "originals")
	t.Setenv("ORGANIZER_MEDIA_BUCKET", "served")

	const (
		mediaID    = "media-new"
		oldMediaID = "media-old"
		seriesID   = "series-1"
		orgID      = "org-1"
	)
	d := newMediaDeps(t)

	origKey := fakeOriginalKey(orgID, mediaID)
	d.storer.objects["originals/"+origKey] = []byte("raw")

	media := &entity.Media{ID: mediaID, OrganizerID: orgID, Kind: entity.MediaKindImage}
	d.mediaRepo.EXPECT().FindMediaByID(mock.Anything, mediaID).Return(media, nil)
	d.processor.EXPECT().ProcessImage(mock.Anything, mock.Anything).
		Return([]byte("thumb"), []byte("large"), nil)
	d.mediaRepo.EXPECT().CutOverSeriesMedia(mock.Anything, seriesID, mediaID).Return(oldMediaID, nil)

	require.NoError(t, d.uc.ProcessMedia(context.Background(), mediaID, seriesID))

	wantPrefix := "served/" + fakeVariantPrefix(orgID, oldMediaID)
	assert.Contains(t, d.storer.deletedPfxs, wantPrefix, "old variant prefix must be deleted")
}

// @spec components/usecase/media/process-media "Decompression bomb"
// TestMediaUseCase_ProcessMedia_UnsupportedImage verifies that a
// permanently-invalid image ends processing without an error (so the event is
// not retried) and deletes the original.
func TestMediaUseCase_ProcessMedia_UnsupportedImage(t *testing.T) {
	t.Setenv("ORGANIZER_MEDIA_INTERNAL_BUCKET", "originals")
	t.Setenv("ORGANIZER_MEDIA_BUCKET", "served")

	const (
		mediaID  = "media-bad"
		seriesID = "series-1"
		orgID    = "org-1"
	)
	d := newMediaDeps(t)

	origKey := fakeOriginalKey(orgID, mediaID)
	d.storer.objects["originals/"+origKey] = []byte("corrupt")

	media := &entity.Media{ID: mediaID, OrganizerID: orgID, Kind: entity.MediaKindImage}
	d.mediaRepo.EXPECT().FindMediaByID(mock.Anything, mediaID).Return(media, nil)
	d.processor.EXPECT().ProcessImage(mock.Anything, mock.Anything).
		Return(nil, nil, entity.ErrUnsupportedMedia)

	err := d.uc.ProcessMedia(context.Background(), mediaID, seriesID)
	assert.NoError(t, err, "permanent failure must not be retried")
	assert.Contains(t, d.storer.deletedKeys, "originals/"+origKey, "original must be cleaned up")
}

// TestMediaUseCase_ProcessMedia_TransientError verifies that a transient
// processor error causes ProcessMedia to return a non-nil error so the
// consumer naks (retries) the message.
func TestMediaUseCase_ProcessMedia_TransientError(t *testing.T) {
	t.Setenv("ORGANIZER_MEDIA_INTERNAL_BUCKET", "originals")
	t.Setenv("ORGANIZER_MEDIA_BUCKET", "served")

	const (
		mediaID  = "media-transient"
		seriesID = "series-1"
		orgID    = "org-1"
	)
	d := newMediaDeps(t)

	origKey := fakeOriginalKey(orgID, mediaID)
	d.storer.objects["originals/"+origKey] = []byte("raw")

	media := &entity.Media{ID: mediaID, OrganizerID: orgID, Kind: entity.MediaKindImage}
	d.mediaRepo.EXPECT().FindMediaByID(mock.Anything, mediaID).Return(media, nil)
	d.processor.EXPECT().ProcessImage(mock.Anything, mock.Anything).
		Return(nil, nil, errors.New("vips OOM"))

	err := d.uc.ProcessMedia(context.Background(), mediaID, seriesID)
	require.Error(t, err, "transient error must propagate for retry")
}

// TestMediaUseCase_ProcessMedia_Idempotent verifies that re-delivery of the
// same MEDIA.uploaded event is a safe no-op: cut-over returns "" (already
// applied), variants are re-written (idempotent PUT), original is cleaned up
// again.
func TestMediaUseCase_ProcessMedia_Idempotent(t *testing.T) {
	t.Setenv("ORGANIZER_MEDIA_INTERNAL_BUCKET", "originals")
	t.Setenv("ORGANIZER_MEDIA_BUCKET", "served")

	const (
		mediaID  = "media-idem"
		seriesID = "series-1"
		orgID    = "org-1"
	)
	d := newMediaDeps(t)

	origKey := fakeOriginalKey(orgID, mediaID)
	d.storer.objects["originals/"+origKey] = []byte("raw")

	media := &entity.Media{ID: mediaID, OrganizerID: orgID, Kind: entity.MediaKindImage}
	d.mediaRepo.EXPECT().FindMediaByID(mock.Anything, mediaID).Return(media, nil).Times(2)
	d.processor.EXPECT().ProcessImage(mock.Anything, mock.Anything).
		Return([]byte("thumb"), []byte("large"), nil).Times(2)
	// CutOverSeriesMedia returns "" on both calls: already applied on first, idempotent on second.
	d.mediaRepo.EXPECT().CutOverSeriesMedia(mock.Anything, seriesID, mediaID).Return("", nil).Times(2)

	require.NoError(t, d.uc.ProcessMedia(context.Background(), mediaID, seriesID))
	// Restore original for second delivery.
	d.storer.objects["originals/"+origKey] = []byte("raw")
	require.NoError(t, d.uc.ProcessMedia(context.Background(), mediaID, seriesID))
}

// @spec components/usecase/media/process-media "Media gone"
// TestMediaUseCase_ProcessMedia_MissingMediaRow verifies that a missing media
// row (cancelled upload) ends processing without error.
func TestMediaUseCase_ProcessMedia_MissingMediaRow(t *testing.T) {
	t.Setenv("ORGANIZER_MEDIA_INTERNAL_BUCKET", "originals")
	t.Setenv("ORGANIZER_MEDIA_BUCKET", "served")

	d := newMediaDeps(t)
	d.mediaRepo.EXPECT().FindMediaByID(mock.Anything, "media-gone").
		Return(nil, apperr.New(codes.NotFound, "not found"))

	err := d.uc.ProcessMedia(context.Background(), "media-gone", "series-1")
	assert.NoError(t, err, "missing media row must not be retried")
}

// @spec components/usecase/media/process-media "Series deleted meanwhile"
// TestMediaUseCase_ProcessMedia_MissingSeries verifies that a series deleted
// before the cut-over ends processing without error and cleans up the
// original.
func TestMediaUseCase_ProcessMedia_MissingSeries(t *testing.T) {
	t.Setenv("ORGANIZER_MEDIA_INTERNAL_BUCKET", "originals")
	t.Setenv("ORGANIZER_MEDIA_BUCKET", "served")

	const (
		mediaID  = "media-1"
		seriesID = "series-gone"
		orgID    = "org-1"
	)
	d := newMediaDeps(t)

	origKey := fakeOriginalKey(orgID, mediaID)
	d.storer.objects["originals/"+origKey] = []byte("raw")

	media := &entity.Media{ID: mediaID, OrganizerID: orgID, Kind: entity.MediaKindImage}
	d.mediaRepo.EXPECT().FindMediaByID(mock.Anything, mediaID).Return(media, nil)
	d.processor.EXPECT().ProcessImage(mock.Anything, mock.Anything).
		Return([]byte("thumb"), []byte("large"), nil)
	d.mediaRepo.EXPECT().CutOverSeriesMedia(mock.Anything, seriesID, mediaID).
		Return("", apperr.New(codes.NotFound, "series not found"))

	err := d.uc.ProcessMedia(context.Background(), mediaID, seriesID)
	assert.NoError(t, err, "missing series must not be retried")
	assert.Contains(t, d.storer.deletedKeys, "originals/"+origKey, "original must be cleaned up")
}
