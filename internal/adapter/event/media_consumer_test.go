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
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// fakeImageStorer is a fully controllable in-memory ImageStorer + ReadObject.
// It implements the optional objectReader interface so MediaConsumer.readOriginal
// can fetch bytes without a real GCS client.
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

func (f *fakeImageStorer) Put(_ context.Context, bucket, key, _ string, data []byte) error {
	if f.putErr != nil {
		return f.putErr
	}
	f.objects[bucket+"/"+key] = data
	return nil
}

func (f *fakeImageStorer) Delete(_ context.Context, bucket, key string) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	f.deletedKeys = append(f.deletedKeys, bucket+"/"+key)
	return nil
}

func (f *fakeImageStorer) DeletePrefix(_ context.Context, bucket, prefix string) error {
	if f.deletePfxErr != nil {
		return f.deletePfxErr
	}
	f.deletedPfxs = append(f.deletedPfxs, bucket+"/"+prefix)
	return nil
}

func (f *fakeImageStorer) SignedPutURL(_ context.Context, _, _, _ string, _ int64, _ time.Duration) (string, error) {
	return "https://signed", nil
}

// ReadObject satisfies the optional objectReader interface consumed by
// MediaConsumer.readOriginal.
func (f *fakeImageStorer) ReadObject(_ context.Context, bucket, key string) ([]byte, error) {
	data, ok := f.objects[bucket+"/"+key]
	if !ok {
		return nil, apperr.New(codes.NotFound, "object not found: "+bucket+"/"+key)
	}
	return data, nil
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

	origKey := entity.OriginalObjectKey(orgID, mediaID)
	d.storer.objects["originals/"+origKey] = []byte("raw-image-bytes")

	media := &entity.Media{ID: mediaID, OrganizerID: orgID, Kind: entity.MediaKindImage}
	d.mediaRepo.EXPECT().FindMediaByID(mock.Anything, mediaID).Return(media, nil)
	d.mediaRepo.EXPECT().CutOverSeriesMedia(mock.Anything, seriesID, mediaID).Return("", nil)

	require.NoError(t, d.consumer.Handle(makeMsg(t, mediaID, seriesID)))

	thumbKey := "served/" + entity.VariantObjectKey(orgID, mediaID, "thumb")
	largeKey := "served/" + entity.VariantObjectKey(orgID, mediaID, "large")
	assert.Equal(t, []byte("thumb-webp"), d.storer.objects[thumbKey], "thumb must be written")
	assert.Equal(t, []byte("large-webp"), d.storer.objects[largeKey], "large must be written")
	assert.Contains(t, d.storer.deletedKeys, "originals/"+origKey, "original must be deleted")
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

	origKey := entity.OriginalObjectKey(orgID, mediaID)
	d.storer.objects["originals/"+origKey] = []byte("raw")

	media := &entity.Media{ID: mediaID, OrganizerID: orgID, Kind: entity.MediaKindImage}
	d.mediaRepo.EXPECT().FindMediaByID(mock.Anything, mediaID).Return(media, nil)
	d.mediaRepo.EXPECT().CutOverSeriesMedia(mock.Anything, seriesID, mediaID).Return(oldMediaID, nil)

	require.NoError(t, d.consumer.Handle(makeMsg(t, mediaID, seriesID)))

	wantPrefix := "served/" + entity.VariantObjectPrefix(orgID, oldMediaID)
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

	origKey := entity.OriginalObjectKey(orgID, mediaID)
	d.storer.objects["originals/"+origKey] = []byte("corrupt")

	media := &entity.Media{ID: mediaID, OrganizerID: orgID, Kind: entity.MediaKindImage}
	d.mediaRepo.EXPECT().FindMediaByID(mock.Anything, mediaID).Return(media, nil)

	err := d.consumer.Handle(makeMsg(t, mediaID, seriesID))
	assert.NoError(t, err, "permanent failure must be acked (not retried)")
	assert.Contains(t, d.storer.deletedKeys, "originals/"+origKey, "original must be cleaned up")
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

	origKey := entity.OriginalObjectKey(orgID, mediaID)
	d.storer.objects["originals/"+origKey] = []byte("raw")

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

	origKey := entity.OriginalObjectKey(orgID, mediaID)
	d.storer.objects["originals/"+origKey] = []byte("raw")

	media := &entity.Media{ID: mediaID, OrganizerID: orgID, Kind: entity.MediaKindImage}
	d.mediaRepo.EXPECT().FindMediaByID(mock.Anything, mediaID).Return(media, nil).Times(2)
	// CutOverSeriesMedia returns "" on both calls: already applied on first, idempotent on second.
	d.mediaRepo.EXPECT().CutOverSeriesMedia(mock.Anything, seriesID, mediaID).Return("", nil).Times(2)

	require.NoError(t, d.consumer.Handle(makeMsg(t, mediaID, seriesID)))
	// Restore original for second delivery.
	d.storer.objects["originals/"+origKey] = []byte("raw")
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

// TestMediaConsumer_VariantURLComposition verifies that variant object keys and
// prefix follow the required scheme independently of any consumer logic.
func TestMediaConsumer_VariantURLComposition(t *testing.T) {
	t.Parallel()
	const orgID = "org-test"
	const mediaID = "media-abc"

	assert.Equal(t, "cdn/org-test/media-abc/thumb.webp", entity.VariantObjectKey(orgID, mediaID, "thumb"))
	assert.Equal(t, "cdn/org-test/media-abc/large.webp", entity.VariantObjectKey(orgID, mediaID, "large"))
	assert.Equal(t, "cdn/org-test/media-abc/", entity.VariantObjectPrefix(orgID, mediaID))
}

// TestMediaConsumer_MapperVariantURLs verifies that VariantURL composes correct
// CDN URLs when the env var is set.
func TestMediaConsumer_MapperVariantURLs(t *testing.T) {
	t.Setenv("ORGANIZER_MEDIA_CDN_BASE", "https://cdn.example.com")
	const orgID = "org-test"
	const mediaID = "media-abc"

	assert.Equal(t, "https://cdn.example.com/cdn/org-test/media-abc/thumb.webp",
		entity.VariantURL(orgID, mediaID, "thumb"))
	assert.Equal(t, "https://cdn.example.com/cdn/org-test/media-abc/large.webp",
		entity.VariantURL(orgID, mediaID, "large"))
}
