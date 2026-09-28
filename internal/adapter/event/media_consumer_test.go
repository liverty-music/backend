package event_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/liverty-music/backend/internal/adapter/event"
	"github.com/liverty-music/backend/internal/entity"
	entitymocks "github.com/liverty-music/backend/internal/entity/mocks"
	gcsstorage "github.com/liverty-music/backend/internal/infrastructure/gcp/storage"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// fakeImageStorer is a fully controllable in-memory usecase.ImageStorer.
// Object keys are composed with the same scheme as the real GCSStorer
// (internal/infrastructure/gcp/storage) so tests observe the same addressing
// without MediaConsumer or the storer ever exposing raw keys to callers.
type fakeImageStorer struct {
	objects      map[string][]byte
	deletedKeys  []string
	deletedPfxs  []string
	putErr       error
	deleteErr    error
	deletePfxErr error
}

func newFakeStorer() *fakeImageStorer {
	return &fakeImageStorer{objects: make(map[string][]byte)}
}

func (f *fakeImageStorer) SignedPutURLForOriginal(_ context.Context, _, _, _, _ string, _ int64, _ time.Duration) (string, error) {
	return "https://signed", nil
}

func (f *fakeImageStorer) ReadOriginal(_ context.Context, bucket, organizerID, mediaID string) ([]byte, error) {
	key := bucket + "/" + gcsstorage.OriginalObjectKey(organizerID, mediaID)
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
	f.deletedKeys = append(f.deletedKeys, bucket+"/"+gcsstorage.OriginalObjectKey(organizerID, mediaID))
	return nil
}

func (f *fakeImageStorer) PutVariant(_ context.Context, bucket, organizerID, mediaID, variant, _ string, data []byte) error {
	if f.putErr != nil {
		return f.putErr
	}
	f.objects[bucket+"/"+gcsstorage.VariantObjectKey(organizerID, mediaID, variant)] = data
	return nil
}

func (f *fakeImageStorer) DeleteVariants(_ context.Context, bucket, organizerID, mediaID string) error {
	if f.deletePfxErr != nil {
		return f.deletePfxErr
	}
	f.deletedPfxs = append(f.deletedPfxs, bucket+"/"+gcsstorage.VariantObjectPrefix(organizerID, mediaID))
	return nil
}

// putOriginal seeds the fake originals bucket for a test, using the same key
// scheme ReadOriginal/DeleteOriginal compute internally.
func (f *fakeImageStorer) putOriginal(bucket, organizerID, mediaID string, data []byte) {
	f.objects[bucket+"/"+gcsstorage.OriginalObjectKey(organizerID, mediaID)] = data
}

// fakeProcessor is a controllable MediaProcessor stub for unit tests.
type fakeProcessor struct {
	thumb []byte
	large []byte
	err   error
}

func (f *fakeProcessor) ProcessImage(_ context.Context, _ []byte) ([]byte, []byte, error) {
	return f.thumb, f.large, f.err
}

// makeMsg builds a Watermill message carrying a MEDIA.uploaded payload.
func makeMsg(t *testing.T, mediaID, seriesID string) *message.Message {
	t.Helper()
	payload, err := json.Marshal(entity.MediaUploadedData{
		MediaID:  mediaID,
		SeriesID: seriesID,
	})
	require.NoError(t, err)
	msg := message.NewMessage("test-uuid", payload)
	msg.SetContext(context.Background())
	return msg
}

// mediaConsumerDeps groups dependencies for building a MediaConsumer in tests.
type mediaConsumerDeps struct {
	mediaRepo *entitymocks.MockMediaRepository
	storer    *fakeImageStorer
	processor *fakeProcessor
	consumer  *event.MediaConsumer
}

func newMediaConsumerDeps(t *testing.T) *mediaConsumerDeps {
	t.Helper()
	logger := newTestLogger(t)
	d := &mediaConsumerDeps{
		mediaRepo: entitymocks.NewMockMediaRepository(t),
		storer:    newFakeStorer(),
		processor: &fakeProcessor{thumb: []byte("thumb-webp"), large: []byte("large-webp")},
	}
	d.consumer = event.NewMediaConsumer(d.mediaRepo, d.storer, d.processor, logger)
	return d
}

// --- Tests ---
// None of these tests call t.Parallel() because they all use t.Setenv, which
// requires sequential execution.

// TestMediaConsumer_Handle_HappyPath verifies the full happy-path flow:
// original read → variants written → series_media cut over (no old media) →
// original deleted.
func TestMediaConsumer_Handle_HappyPath(t *testing.T) {
	t.Setenv("ORGANIZER_MEDIA_INTERNAL_BUCKET", "originals")
	t.Setenv("ORGANIZER_MEDIA_BUCKET", "served")

	const (
		mediaID  = "media-new"
		seriesID = "series-1"
		orgID    = "org-1"
	)
	d := newMediaConsumerDeps(t)
	d.storer.putOriginal("originals", orgID, mediaID, []byte("raw-image-bytes"))

	media := &entity.Media{ID: mediaID, OrganizerID: orgID, Kind: entity.MediaKindImage}
	d.mediaRepo.EXPECT().FindMediaByID(mock.Anything, mediaID).Return(media, nil)
	d.mediaRepo.EXPECT().CutOverSeriesMedia(mock.Anything, seriesID, mediaID).Return("", nil)

	require.NoError(t, d.consumer.Handle(makeMsg(t, mediaID, seriesID)))

	thumbKey := "served/" + gcsstorage.VariantObjectKey(orgID, mediaID, "thumb")
	largeKey := "served/" + gcsstorage.VariantObjectKey(orgID, mediaID, "large")
	assert.Equal(t, []byte("thumb-webp"), d.storer.objects[thumbKey], "thumb must be written")
	assert.Equal(t, []byte("large-webp"), d.storer.objects[largeKey], "large must be written")
	assert.Contains(t, d.storer.deletedKeys, "originals/"+gcsstorage.OriginalObjectKey(orgID, mediaID), "original must be deleted")
}

// TestMediaConsumer_Handle_ReplacesOldMedia verifies that when cut-over returns
// an old media id, the old variant prefix is deleted from the served bucket.
func TestMediaConsumer_Handle_ReplacesOldMedia(t *testing.T) {
	t.Setenv("ORGANIZER_MEDIA_INTERNAL_BUCKET", "originals")
	t.Setenv("ORGANIZER_MEDIA_BUCKET", "served")

	const (
		mediaID    = "media-new"
		oldMediaID = "media-old"
		seriesID   = "series-1"
		orgID      = "org-1"
	)
	d := newMediaConsumerDeps(t)
	d.storer.putOriginal("originals", orgID, mediaID, []byte("raw"))

	media := &entity.Media{ID: mediaID, OrganizerID: orgID, Kind: entity.MediaKindImage}
	d.mediaRepo.EXPECT().FindMediaByID(mock.Anything, mediaID).Return(media, nil)
	d.mediaRepo.EXPECT().CutOverSeriesMedia(mock.Anything, seriesID, mediaID).Return(oldMediaID, nil)

	require.NoError(t, d.consumer.Handle(makeMsg(t, mediaID, seriesID)))

	wantPrefix := "served/" + gcsstorage.VariantObjectPrefix(orgID, oldMediaID)
	assert.Contains(t, d.storer.deletedPfxs, wantPrefix, "old variant prefix must be deleted")
}

// TestMediaConsumer_Handle_UnsupportedImage verifies that a permanently-invalid
// image causes the handler to ack (return nil) and delete the original.
func TestMediaConsumer_Handle_UnsupportedImage(t *testing.T) {
	t.Setenv("ORGANIZER_MEDIA_INTERNAL_BUCKET", "originals")
	t.Setenv("ORGANIZER_MEDIA_BUCKET", "served")

	const (
		mediaID  = "media-bad"
		seriesID = "series-1"
		orgID    = "org-1"
	)
	d := newMediaConsumerDeps(t)
	d.processor.err = event.ErrUnsupportedMedia

	d.storer.putOriginal("originals", orgID, mediaID, []byte("corrupt"))

	media := &entity.Media{ID: mediaID, OrganizerID: orgID, Kind: entity.MediaKindImage}
	d.mediaRepo.EXPECT().FindMediaByID(mock.Anything, mediaID).Return(media, nil)

	err := d.consumer.Handle(makeMsg(t, mediaID, seriesID))
	assert.NoError(t, err, "permanent failure must be acked (not retried)")
	assert.Contains(t, d.storer.deletedKeys, "originals/"+gcsstorage.OriginalObjectKey(orgID, mediaID), "original must be cleaned up")
}

// TestMediaConsumer_Handle_TransientError verifies that a transient processor
// error causes Handle to return a non-nil error so Watermill naks the message.
func TestMediaConsumer_Handle_TransientError(t *testing.T) {
	t.Setenv("ORGANIZER_MEDIA_INTERNAL_BUCKET", "originals")
	t.Setenv("ORGANIZER_MEDIA_BUCKET", "served")

	const (
		mediaID  = "media-transient"
		seriesID = "series-1"
		orgID    = "org-1"
	)
	d := newMediaConsumerDeps(t)
	d.processor.err = errors.New("vips OOM")

	d.storer.putOriginal("originals", orgID, mediaID, []byte("raw"))

	media := &entity.Media{ID: mediaID, OrganizerID: orgID, Kind: entity.MediaKindImage}
	d.mediaRepo.EXPECT().FindMediaByID(mock.Anything, mediaID).Return(media, nil)

	err := d.consumer.Handle(makeMsg(t, mediaID, seriesID))
	require.Error(t, err, "transient error must propagate for nak/retry")
}

// TestMediaConsumer_Handle_Idempotent verifies that re-delivery of the same
// MEDIA.uploaded event is a safe no-op: cut-over returns "" (already applied),
// variants are re-written (idempotent PUT), original is cleaned up again.
func TestMediaConsumer_Handle_Idempotent(t *testing.T) {
	t.Setenv("ORGANIZER_MEDIA_INTERNAL_BUCKET", "originals")
	t.Setenv("ORGANIZER_MEDIA_BUCKET", "served")

	const (
		mediaID  = "media-idem"
		seriesID = "series-1"
		orgID    = "org-1"
	)
	d := newMediaConsumerDeps(t)
	d.storer.putOriginal("originals", orgID, mediaID, []byte("raw"))

	media := &entity.Media{ID: mediaID, OrganizerID: orgID, Kind: entity.MediaKindImage}
	d.mediaRepo.EXPECT().FindMediaByID(mock.Anything, mediaID).Return(media, nil).Times(2)
	// CutOverSeriesMedia returns "" on both calls: already applied on first, idempotent on second.
	d.mediaRepo.EXPECT().CutOverSeriesMedia(mock.Anything, seriesID, mediaID).Return("", nil).Times(2)

	require.NoError(t, d.consumer.Handle(makeMsg(t, mediaID, seriesID)))
	// Restore original for second delivery.
	d.storer.putOriginal("originals", orgID, mediaID, []byte("raw"))
	require.NoError(t, d.consumer.Handle(makeMsg(t, mediaID, seriesID)))
}

// TestMediaConsumer_Handle_MissingMediaRow verifies that a missing media row
// (cancelled upload) is acked without error.
func TestMediaConsumer_Handle_MissingMediaRow(t *testing.T) {
	t.Setenv("ORGANIZER_MEDIA_INTERNAL_BUCKET", "originals")
	t.Setenv("ORGANIZER_MEDIA_BUCKET", "served")

	d := newMediaConsumerDeps(t)
	d.mediaRepo.EXPECT().FindMediaByID(mock.Anything, "media-gone").
		Return(nil, apperr.New(codes.NotFound, "not found"))

	err := d.consumer.Handle(makeMsg(t, "media-gone", "series-1"))
	assert.NoError(t, err, "missing media row must be acked/skipped")
}
