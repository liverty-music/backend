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

// testOriginalsBucket is the internal bucket SetCoverImage checks uploads in;
// the Process tests set STORAGE_INTERNAL_BUCKET to the same name.
const testOriginalsBucket = "originals"

// testVariantsBucket is the bucket the Process tests set STORAGE_BUCKET to.
const testVariantsBucket = "served"

// seriesImageDeps wires up a SeriesImageUseCase with mocks for every dependency.
type seriesImageDeps struct {
	seriesRepo      *entitymocks.MockSeriesRepository
	seriesImageRepo *entitymocks.MockSeriesImageRepository
	storer          *ucmocks.MockImageStorer
	processor       *entitymocks.MockSeriesImageProcessor
	uc              usecase.SeriesImageUseCase
	series          usecase.SeriesUseCase
}

func newSeriesImageDeps(t *testing.T) *seriesImageDeps {
	t.Helper()
	logger := newTestLogger(t)
	d := &seriesImageDeps{
		seriesRepo:      entitymocks.NewMockSeriesRepository(t),
		seriesImageRepo: entitymocks.NewMockSeriesImageRepository(t),
		storer:          ucmocks.NewMockImageStorer(t),
		processor:       entitymocks.NewMockSeriesImageProcessor(t),
	}
	d.uc = usecase.NewSeriesImageUseCase(
		d.seriesRepo, d.seriesImageRepo, d.storer, d.processor, logger,
	)
	d.series = usecase.NewSeriesUseCase(d.seriesRepo, d.seriesImageRepo, nil, nil, nil, nil,
		d.storer, usecase.StorageBuckets{Internal: testOriginalsBucket, Served: testVariantsBucket}, logger, time.Now)
	return d
}

// uploaded makes the storer report whether the caller orgID uploaded an
// original for seriesImageID.
func (d *seriesImageDeps) uploaded(orgID, seriesImageID string, exists bool) {
	d.storer.EXPECT().OriginalExists(mock.Anything, testOriginalsBucket, orgID, seriesImageID).Return(exists, nil).Once()
}

// readsOriginal makes the storer return data as the original of seriesImageID
// once.
func (d *seriesImageDeps) readsOriginal(orgID, seriesImageID string, data []byte) *mock.Call {
	return d.storer.EXPECT().ReadOriginal(mock.Anything, testOriginalsBucket, orgID, seriesImageID).Return(data, nil).Once()
}

// writesVariants makes the storer expect the thumb and the large variant of
// seriesImageID once each.
func (d *seriesImageDeps) writesVariants(orgID, seriesImageID string, thumb, large any) {
	d.storer.EXPECT().PutVariant(mock.Anything, testVariantsBucket, orgID, seriesImageID, "thumb", "image/webp", thumb).Return(nil).Once()
	d.storer.EXPECT().PutVariant(mock.Anything, testVariantsBucket, orgID, seriesImageID, "large", "image/webp", large).Return(nil).Once()
}

// deletesOriginal makes the storer expect the original of seriesImageID to
// be deleted once.
func (d *seriesImageDeps) deletesOriginal(orgID, seriesImageID string) *mock.Call {
	return d.storer.EXPECT().DeleteOriginal(mock.Anything, testOriginalsBucket, orgID, seriesImageID).Return(nil).Once()
}

// --- CreateUploadURL / SetCoverImage tests ---

// TestSeriesImageUseCase_CreateUploadURL_IssuesSignedURL verifies that a valid
// content type triggers a signed PUT URL and returns the series image id + max bytes.
func TestSeriesImageUseCase_CreateUploadURL_IssuesSignedURL(t *testing.T) {
	// t.Setenv requires sequential execution.
	t.Setenv("STORAGE_INTERNAL_BUCKET", "originals-bucket")
	ctx := context.Background()
	d := newSeriesImageDeps(t)

	const orgID = "org-1"
	var signedFor string
	d.storer.EXPECT().SignedPutURLForOriginal(mock.Anything, "originals-bucket", orgID, mock.Anything, "image/jpeg", int64(10*1024*1024), mock.Anything).
		RunAndReturn(func(_ context.Context, _, _, seriesImageID, _ string, _ int64, _ time.Duration) (string, error) {
			signedFor = seriesImageID
			return "https://signed", nil
		}).Once()

	out, err := d.uc.CreateUploadURL(ctx, orgID, usecase.CreateSeriesImageUploadURLInput{ContentType: "image/jpeg"})
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(out.UploadURL, "https://signed"))
	assert.NotEmpty(t, out.SeriesImageID)
	assert.Equal(t, out.SeriesImageID, signedFor, "the URL is signed for the returned series image id")
	assert.Equal(t, int64(10*1024*1024), out.MaxBytes)
}

// TestSeriesImageUseCase_CreateUploadURL_RejectsInvalidType verifies that a
// non-allowlisted content type returns InvalidArgument before any GCS call.
func TestSeriesImageUseCase_CreateUploadURL_RejectsInvalidType(t *testing.T) {
	ctx := context.Background()
	d := newSeriesImageDeps(t)

	_, err := d.uc.CreateUploadURL(ctx, "org-1", usecase.CreateSeriesImageUploadURLInput{ContentType: "image/svg+xml"})
	require.Error(t, err)
	assert.True(t, errors.Is(err, apperr.ErrInvalidArgument), "expected InvalidArgument, got %v", err)
}

// TestSeriesImageUseCase_CreateUploadURL_MissingBucket verifies a clear Internal
// error when STORAGE_INTERNAL_BUCKET is unset.
func TestSeriesImageUseCase_CreateUploadURL_MissingBucket(t *testing.T) {
	// t.Setenv requires sequential execution.
	t.Setenv("STORAGE_INTERNAL_BUCKET", "")
	ctx := context.Background()
	d := newSeriesImageDeps(t)

	_, err := d.uc.CreateUploadURL(ctx, "org-1", usecase.CreateSeriesImageUploadURLInput{ContentType: "image/png"})
	require.Error(t, err)
	assert.True(t, errors.Is(err, apperr.ErrInternal), "expected Internal, got %v", err)
}

func TestSeriesUseCase_SetCoverImage(t *testing.T) {
	t.Parallel()

	const (
		orgID         = "org-1"
		otherOrgID    = "org-2"
		seriesID      = "series-1"
		seriesImageID = "image-1"
	)
	ownSeries := &entity.Series{ID: seriesID, OrganizerID: new(orgID)}

	type args struct {
		seriesImageID string
	}
	tests := []struct {
		name    string
		args    args
		setup   func(d *seriesImageDeps)
		wantErr error
	}{
		{
			// @spec components/usecase/series/set-cover-image "Missing series image id"
			name:    "rejects an empty series image id",
			args:    args{seriesImageID: ""},
			setup:   func(*seriesImageDeps) {},
			wantErr: apperr.ErrInvalidArgument,
		},
		{
			// @spec components/usecase/series/set-cover-image "Another Organizer's series"
			name: "denies a series owned by another organizer and records nothing",
			args: args{seriesImageID: seriesImageID},
			setup: func(d *seriesImageDeps) {
				// The storer is not asked: ownership is checked first.
				d.seriesRepo.EXPECT().Get(mock.Anything, seriesID).
					Return(&entity.Series{ID: seriesID, OrganizerID: new(otherOrgID)}, nil)
			},
			wantErr: apperr.ErrPermissionDenied,
		},
		{
			// @spec components/usecase/series/set-cover-image "Cover replaced later"
			name: "records the upload with its processing request without switching the cover",
			args: args{seriesImageID: seriesImageID},
			setup: func(d *seriesImageDeps) {
				d.seriesRepo.EXPECT().Get(mock.Anything, seriesID).Return(ownSeries, nil)
				d.uploaded(orgID, seriesImageID, true)
				d.seriesImageRepo.EXPECT().Create(mock.Anything, mock.MatchedBy(func(m *entity.SeriesImage) bool {
					return m.ID == seriesImageID && m.OrganizerID == orgID
				}), seriesID).Return(nil)
				// ReplaceCover is not expected: the cover switches only in Process.
			},
		},
		{
			// @spec components/usecase/series/set-cover-image "Another Organizer's series image id"
			name: "denies a series image recorded for another organizer and records nothing",
			args: args{seriesImageID: seriesImageID},
			setup: func(d *seriesImageDeps) {
				// The original was uploaded under the other Organizer, so none
				// exists under the caller's.
				d.seriesRepo.EXPECT().Get(mock.Anything, seriesID).Return(ownSeries, nil)
				d.uploaded(orgID, seriesImageID, false)
				d.seriesImageRepo.EXPECT().Get(mock.Anything, seriesImageID).
					Return(&entity.SeriesImage{ID: seriesImageID, OrganizerID: otherOrgID}, nil)
			},
			wantErr: apperr.ErrPermissionDenied,
		},
		{
			// @spec components/usecase/series/set-cover-image "Nothing uploaded"
			name: "fails when no original was uploaded and records nothing",
			args: args{seriesImageID: seriesImageID},
			setup: func(d *seriesImageDeps) {
				d.seriesRepo.EXPECT().Get(mock.Anything, seriesID).Return(ownSeries, nil)
				d.uploaded(orgID, seriesImageID, false)
				d.seriesImageRepo.EXPECT().Get(mock.Anything, seriesImageID).
					Return(nil, apperr.New(codes.NotFound, "series image not found"))
			},
			wantErr: apperr.ErrFailedPrecondition,
		},
		{
			// @spec components/usecase/series/set-cover-image "Repeated attach after processing"
			name: "succeeds without announcing once the original was processed",
			args: args{seriesImageID: seriesImageID},
			setup: func(d *seriesImageDeps) {
				d.seriesRepo.EXPECT().Get(mock.Anything, seriesID).Return(ownSeries, nil)
				d.uploaded(orgID, seriesImageID, false)
				d.seriesImageRepo.EXPECT().Get(mock.Anything, seriesImageID).
					Return(&entity.SeriesImage{ID: seriesImageID, OrganizerID: orgID}, nil)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d := newSeriesImageDeps(t)
			tt.setup(d)

			err := d.series.SetCoverImage(context.Background(), orgID, seriesID, tt.args.seriesImageID)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}
			assert.NoError(t, err)
		})
	}
}

// @spec components/usecase/series/set-cover-image "Repeated attach"
func TestSeriesUseCase_SetCoverImage_RepeatedAttach(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := newSeriesImageDeps(t)

	const (
		orgID         = "org-1"
		seriesID      = "series-1"
		seriesImageID = "image-dup"
	)
	d.seriesRepo.EXPECT().Get(mock.Anything, seriesID).Return(&entity.Series{ID: seriesID, OrganizerID: new(orgID)}, nil).Times(2)
	d.storer.EXPECT().OriginalExists(mock.Anything, testOriginalsBucket, orgID, seriesImageID).Return(true, nil).Times(2)
	// Both calls record the same id; SeriesImage.Create stores and requests
	// processing only the first time.
	d.seriesImageRepo.EXPECT().Create(mock.Anything, mock.MatchedBy(func(m *entity.SeriesImage) bool {
		return m.ID == seriesImageID
	}), seriesID).Return(nil).Times(2)

	require.NoError(t, d.series.SetCoverImage(ctx, orgID, seriesID, seriesImageID))
	require.NoError(t, d.series.SetCoverImage(ctx, orgID, seriesID, seriesImageID))
}

// @spec components/usecase/series/set-cover-image "Announcement fails"
func TestSeriesUseCase_SetCoverImage_AnnouncementFails(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := newSeriesImageDeps(t)

	const (
		orgID         = "org-1"
		seriesID      = "series-1"
		seriesImageID = "image-1"
	)
	d.seriesRepo.EXPECT().Get(mock.Anything, seriesID).Return(&entity.Series{ID: seriesID, OrganizerID: new(orgID)}, nil).Times(2)
	d.storer.EXPECT().OriginalExists(mock.Anything, testOriginalsBucket, orgID, seriesImageID).Return(true, nil).Times(2)
	d.seriesImageRepo.EXPECT().Create(mock.Anything, mock.Anything, seriesID).
		Return(apperr.New(codes.Unavailable, "failed to record the processing request")).Once()
	d.seriesImageRepo.EXPECT().Create(mock.Anything, mock.Anything, seriesID).Return(nil).Once()

	err := d.series.SetCoverImage(ctx, orgID, seriesID, seriesImageID)
	assert.ErrorIs(t, err, apperr.ErrUnavailable)

	// The repeat after the failure records the image and its request.
	require.NoError(t, d.series.SetCoverImage(ctx, orgID, seriesID, seriesImageID))
}

// --- SeriesImageUseCase.Process tests ---
// None of these use t.Parallel() because they all use t.Setenv, which
// requires sequential execution.

// @spec components/usecase/series-image/process "Replacing a published cover"
func TestSeriesImageUseCase_Process_HappyPath(t *testing.T) {
	t.Setenv("STORAGE_INTERNAL_BUCKET", "originals")
	t.Setenv("STORAGE_BUCKET", "served")

	const (
		seriesImageID = "image-new"
		seriesID      = "series-1"
		orgID         = "org-1"
	)
	d := newSeriesImageDeps(t)

	d.readsOriginal(orgID, seriesImageID, []byte("raw-image-bytes"))

	seriesImage := &entity.SeriesImage{ID: seriesImageID, OrganizerID: orgID}
	d.seriesImageRepo.EXPECT().Get(mock.Anything, seriesImageID).Return(seriesImage, nil)
	d.processor.EXPECT().ProcessImage(mock.Anything, []byte("raw-image-bytes")).
		Return([]byte("thumb-webp"), []byte("large-webp"), nil)
	// Both variants must be written to the served bucket.
	d.writesVariants(orgID, seriesImageID, []byte("thumb-webp"), []byte("large-webp"))
	d.seriesImageRepo.EXPECT().ReplaceCover(mock.Anything, seriesID, seriesImageID).Return("", nil)
	// The original must be deleted.
	d.deletesOriginal(orgID, seriesImageID)

	require.NoError(t, d.uc.Process(context.Background(), seriesImageID, seriesID))
}

// TestSeriesImageUseCase_Process_ReplacesOldImage verifies that when cut-over
// returns an old series image id, the old variant prefix is deleted from the served
// bucket.
func TestSeriesImageUseCase_Process_ReplacesOldImage(t *testing.T) {
	t.Setenv("STORAGE_INTERNAL_BUCKET", "originals")
	t.Setenv("STORAGE_BUCKET", "served")

	const (
		seriesImageID    = "image-new"
		oldSeriesImageID = "image-old"
		seriesID         = "series-1"
		orgID            = "org-1"
	)
	d := newSeriesImageDeps(t)

	d.readsOriginal(orgID, seriesImageID, []byte("raw"))

	seriesImage := &entity.SeriesImage{ID: seriesImageID, OrganizerID: orgID}
	d.seriesImageRepo.EXPECT().Get(mock.Anything, seriesImageID).Return(seriesImage, nil)
	d.processor.EXPECT().ProcessImage(mock.Anything, mock.Anything).
		Return([]byte("thumb"), []byte("large"), nil)
	d.writesVariants(orgID, seriesImageID, []byte("thumb"), []byte("large"))
	d.seriesImageRepo.EXPECT().ReplaceCover(mock.Anything, seriesID, seriesImageID).Return(oldSeriesImageID, nil)
	// The old variants must be deleted from the served bucket.
	d.storer.EXPECT().DeleteVariants(mock.Anything, testVariantsBucket, orgID, oldSeriesImageID).Return(nil).Once()
	d.deletesOriginal(orgID, seriesImageID)

	require.NoError(t, d.uc.Process(context.Background(), seriesImageID, seriesID))
}

// @spec components/usecase/series-image/process "Decompression bomb"
// TestSeriesImageUseCase_Process_UnsupportedImage verifies that a
// permanently-invalid image ends processing without an error (so the event is
// not retried) and deletes the original.
func TestSeriesImageUseCase_Process_UnsupportedImage(t *testing.T) {
	t.Setenv("STORAGE_INTERNAL_BUCKET", "originals")
	t.Setenv("STORAGE_BUCKET", "served")

	const (
		seriesImageID = "image-bad"
		seriesID      = "series-1"
		orgID         = "org-1"
	)
	d := newSeriesImageDeps(t)

	d.readsOriginal(orgID, seriesImageID, []byte("corrupt"))

	seriesImage := &entity.SeriesImage{ID: seriesImageID, OrganizerID: orgID}
	d.seriesImageRepo.EXPECT().Get(mock.Anything, seriesImageID).Return(seriesImage, nil)
	d.processor.EXPECT().ProcessImage(mock.Anything, mock.Anything).
		Return(nil, nil, apperr.New(codes.InvalidArgument, "unsupported image"))
	// The original must be cleaned up.
	d.deletesOriginal(orgID, seriesImageID)

	err := d.uc.Process(context.Background(), seriesImageID, seriesID)
	assert.NoError(t, err, "permanent failure must not be retried")
}

// TestSeriesImageUseCase_Process_TransientError verifies that a transient
// processor error causes SeriesImageUseCase.Process to return a non-nil error so the
// consumer naks (retries) the message.
func TestSeriesImageUseCase_Process_TransientError(t *testing.T) {
	t.Setenv("STORAGE_INTERNAL_BUCKET", "originals")
	t.Setenv("STORAGE_BUCKET", "served")

	const (
		seriesImageID = "image-transient"
		seriesID      = "series-1"
		orgID         = "org-1"
	)
	d := newSeriesImageDeps(t)

	d.readsOriginal(orgID, seriesImageID, []byte("raw"))

	seriesImage := &entity.SeriesImage{ID: seriesImageID, OrganizerID: orgID}
	d.seriesImageRepo.EXPECT().Get(mock.Anything, seriesImageID).Return(seriesImage, nil)
	d.processor.EXPECT().ProcessImage(mock.Anything, mock.Anything).
		Return(nil, nil, errors.New("vips OOM"))

	err := d.uc.Process(context.Background(), seriesImageID, seriesID)
	require.Error(t, err, "transient error must propagate for retry")
}

// TestSeriesImageUseCase_Process_Idempotent verifies that a repeated
// SERIES_IMAGE.uploaded request is a safe no-op: when it arrives while the
// first run has not deleted the original, cut-over returns "" (already
// applied), variants are re-written (idempotent PUT) and the original is
// cleaned up again; once the original is deleted it ends without processing.
//
// @spec components/usecase/series-image/process "Delivered twice"
func TestSeriesImageUseCase_Process_Idempotent(t *testing.T) {
	t.Setenv("STORAGE_INTERNAL_BUCKET", "originals")
	t.Setenv("STORAGE_BUCKET", "served")

	const (
		seriesImageID = "image-idem"
		seriesID      = "series-1"
		orgID         = "org-1"
	)
	d := newSeriesImageDeps(t)

	// The first two deliveries find the original (the second arrives before
	// the first deleted it); the third finds it deleted.
	d.readsOriginal(orgID, seriesImageID, []byte("raw")).Times(2)
	d.storer.EXPECT().ReadOriginal(mock.Anything, testOriginalsBucket, orgID, seriesImageID).
		Return(nil, apperr.New(codes.NotFound, "original not found")).Once()

	seriesImage := &entity.SeriesImage{ID: seriesImageID, OrganizerID: orgID}
	d.seriesImageRepo.EXPECT().Get(mock.Anything, seriesImageID).Return(seriesImage, nil).Times(2)
	d.processor.EXPECT().ProcessImage(mock.Anything, mock.Anything).
		Return([]byte("thumb"), []byte("large"), nil).Times(2)
	// Variants are re-written on the second run (idempotent PUT).
	d.storer.EXPECT().PutVariant(mock.Anything, testVariantsBucket, orgID, seriesImageID, "thumb", "image/webp", []byte("thumb")).Return(nil).Times(2)
	d.storer.EXPECT().PutVariant(mock.Anything, testVariantsBucket, orgID, seriesImageID, "large", "image/webp", []byte("large")).Return(nil).Times(2)
	// ReplaceCover returns "" on both calls: already applied on first, idempotent on second.
	d.seriesImageRepo.EXPECT().ReplaceCover(mock.Anything, seriesID, seriesImageID).Return("", nil).Times(2)
	d.deletesOriginal(orgID, seriesImageID).Times(2)

	require.NoError(t, d.uc.Process(context.Background(), seriesImageID, seriesID))
	require.NoError(t, d.uc.Process(context.Background(), seriesImageID, seriesID))

	// A third delivery finds the original deleted and processes nothing (the
	// strict mocks allow two runs).
	d.seriesImageRepo.EXPECT().Get(mock.Anything, seriesImageID).Return(seriesImage, nil).Once()
	require.NoError(t, d.uc.Process(context.Background(), seriesImageID, seriesID))
}

// TestSeriesImageUseCase_Process_StorageUnavailable verifies that a failed
// read of the original fails Process, so the request is processed again.
//
// @spec components/usecase/series-image/process "Storage briefly unavailable"
func TestSeriesImageUseCase_Process_StorageUnavailable(t *testing.T) {
	t.Setenv("STORAGE_INTERNAL_BUCKET", "originals")
	t.Setenv("STORAGE_BUCKET", "served")

	d := newSeriesImageDeps(t)
	d.storer.EXPECT().ReadOriginal(mock.Anything, testOriginalsBucket, "org-1", "image-1").
		Return(nil, apperr.New(codes.Unavailable, "gcs unavailable")).Once()
	d.seriesImageRepo.EXPECT().Get(mock.Anything, "image-1").
		Return(&entity.SeriesImage{ID: "image-1", OrganizerID: "org-1"}, nil).Once()

	err := d.uc.Process(context.Background(), "image-1", "series-1")

	assert.ErrorIs(t, err, apperr.ErrUnavailable)
}

// @spec components/usecase/series-image/process "SeriesImage gone"
// TestSeriesImageUseCase_Process_MissingSeriesImageRow verifies that a missing series image
// row (cancelled upload) ends processing without error.
func TestSeriesImageUseCase_Process_MissingSeriesImageRow(t *testing.T) {
	t.Setenv("STORAGE_INTERNAL_BUCKET", "originals")
	t.Setenv("STORAGE_BUCKET", "served")

	d := newSeriesImageDeps(t)
	d.seriesImageRepo.EXPECT().Get(mock.Anything, "image-gone").
		Return(nil, apperr.New(codes.NotFound, "not found"))

	err := d.uc.Process(context.Background(), "image-gone", "series-1")
	assert.NoError(t, err, "missing series image row must not be retried")
}

// @spec components/usecase/series-image/process "Series deleted meanwhile"
// TestSeriesImageUseCase_Process_MissingSeries verifies that a series deleted
// before the cut-over ends processing without error and cleans up the
// original.
func TestSeriesImageUseCase_Process_MissingSeries(t *testing.T) {
	t.Setenv("STORAGE_INTERNAL_BUCKET", "originals")
	t.Setenv("STORAGE_BUCKET", "served")

	const (
		seriesImageID = "image-1"
		seriesID      = "series-gone"
		orgID         = "org-1"
	)
	d := newSeriesImageDeps(t)

	d.readsOriginal(orgID, seriesImageID, []byte("raw"))

	seriesImage := &entity.SeriesImage{ID: seriesImageID, OrganizerID: orgID}
	d.seriesImageRepo.EXPECT().Get(mock.Anything, seriesImageID).Return(seriesImage, nil)
	d.processor.EXPECT().ProcessImage(mock.Anything, mock.Anything).
		Return([]byte("thumb"), []byte("large"), nil)
	d.writesVariants(orgID, seriesImageID, []byte("thumb"), []byte("large"))
	d.seriesImageRepo.EXPECT().ReplaceCover(mock.Anything, seriesID, seriesImageID).
		Return("", apperr.New(codes.NotFound, "series not found"))
	// The original must be cleaned up.
	d.deletesOriginal(orgID, seriesImageID)

	err := d.uc.Process(context.Background(), seriesImageID, seriesID)
	assert.NoError(t, err, "missing series must not be retried")
}
