package rpc_test

import (
	"testing"

	seriesimagev1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/organizer/series_image/v1"
	"connectrpc.com/connect/v2"
	"github.com/liverty-music/backend/internal/adapter/rpc"
	"github.com/liverty-music/backend/internal/usecase"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestOrganizerSeriesImageHandler_CreateUploadUrl(t *testing.T) {
	t.Parallel()

	// @spec components/adapter/organizer/api/rpc/series-image "Member of an active Organizer asks to upload"
	t.Run("return an upload URL and a new series image id for a member of an active organizer", func(t *testing.T) {
		t.Parallel()
		seriesImageUC, organizerUC := ucmocks.NewMockSeriesImageUseCase(t), ucmocks.NewMockOrganizerUseCase(t)
		organizerUC.EXPECT().ResolveCaller(mock.Anything, testProviderOrganizerID).Return(activeOrganizer(), nil).Once()
		seriesImageUC.EXPECT().
			CreateUploadURL(mock.Anything, testOrganizerID, usecase.CreateSeriesImageUploadURLInput{ContentType: "image/png"}).
			Return(&usecase.CreateSeriesImageUploadURLOutput{UploadURL: "https://storage.example.com/upload", SeriesImageID: "img-1", MaxBytes: 1024}, nil).
			Once()

		resp, err := rpc.NewOrganizerSeriesImageHandler(seriesImageUC, organizerUC).CreateUploadUrl(orgCtx(testProviderOrganizerID),
			&seriesimagev1.CreateUploadUrlRequest{ContentType: "image/png"})

		require.NoError(t, err)
		assert.Equal(t, "https://storage.example.com/upload", resp.GetUploadUrl().GetValue())
		assert.Equal(t, "img-1", resp.GetSeriesImageId().GetValue())
		assert.Equal(t, int64(1024), resp.GetMaxBytes())
	})

	// @spec components/adapter/organizer/api/rpc/series-image "Deactivated Organizer"
	t.Run("return FAILED_PRECONDITION unchanged without authorizing an upload when the organizer is deactivated", func(t *testing.T) {
		t.Parallel()
		seriesImageUC, organizerUC := ucmocks.NewMockSeriesImageUseCase(t), ucmocks.NewMockOrganizerUseCase(t)
		resolveErr := apperr.New(apperr.ErrFailedPrecondition.Code, "organizer is deactivated")
		organizerUC.EXPECT().ResolveCaller(mock.Anything, testProviderOrganizerID).Return(nil, resolveErr).Once()

		_, err := rpc.NewOrganizerSeriesImageHandler(seriesImageUC, organizerUC).CreateUploadUrl(orgCtx(testProviderOrganizerID),
			&seriesimagev1.CreateUploadUrlRequest{ContentType: "image/png"})

		require.Error(t, err)
		assert.ErrorIs(t, err, resolveErr)
		assert.Equal(t, connect.CodeFailedPrecondition, connectCodeOf(err))
	})
}
