package rpc

import (
	"context"
	"errors"

	seriesimagev1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/organizer/series_image/v1/series_imagev1connect"
	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	seriesimagev1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/organizer/series_image/v1"
	"connectrpc.com/connect"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/auth"
	"github.com/liverty-music/backend/internal/usecase"
)

// Compile-time assertion that OrganizerSeriesImageHandler satisfies the generated interface.
var _ seriesimagev1connect.SeriesImageServiceHandler = (*OrganizerSeriesImageHandler)(nil)

// OrganizerSeriesImageHandler implements the organizer-facing
// SeriesImageService Connect interface. It resolves the caller's organizer and
// delegates to the use case.
type OrganizerSeriesImageHandler struct {
	mediaUC     usecase.MediaUseCase
	organizerUC usecase.OrganizerUseCase
}

// NewOrganizerSeriesImageHandler creates a new OrganizerSeriesImageHandler.
func NewOrganizerSeriesImageHandler(
	mediaUC usecase.MediaUseCase,
	organizerUC usecase.OrganizerUseCase,
) *OrganizerSeriesImageHandler {
	return &OrganizerSeriesImageHandler{mediaUC: mediaUC, organizerUC: organizerUC}
}

// resolveCallerOrganizer reads the identity provider's organization id from
// context and returns the caller's active Organizer.
func (h *OrganizerSeriesImageHandler) resolveCallerOrganizer(ctx context.Context) (*entity.Organizer, error) {
	callerOrgID, ok := auth.GetCallerOrgID(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("permission denied"))
	}
	return h.organizerUC.ResolveCaller(ctx, callerOrgID)
}

// CreateUploadUrl mints a signed storage PUT URL for a direct upload of a
// Series image.
func (h *OrganizerSeriesImageHandler) CreateUploadUrl(
	ctx context.Context,
	req *connect.Request[seriesimagev1.CreateUploadUrlRequest],
) (*connect.Response[seriesimagev1.CreateUploadUrlResponse], error) {
	organizer, err := h.resolveCallerOrganizer(ctx)
	if err != nil {
		return nil, err
	}

	out, err := h.mediaUC.CreateMediaUploadURL(ctx, organizer.ID, usecase.CreateMediaUploadURLInput{
		ContentType: req.Msg.GetContentType(),
	})
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&seriesimagev1.CreateUploadUrlResponse{
		UploadUrl:     &entityv1.Url{Value: out.UploadURL},
		SeriesImageId: &entityv1.SeriesImageId{Value: out.MediaID},
		MaxBytes:      out.MaxBytes,
	}), nil
}
