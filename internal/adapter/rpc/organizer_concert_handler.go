package rpc

import (
	"context"
	"errors"

	organizerv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/organizer/v1/organizerv1connect"
	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	organizerv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/organizer/v1"
	"connectrpc.com/connect"
	"github.com/liverty-music/backend/internal/adapter/rpc/mapper"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/auth"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-logging/logging"
)

// Compile-time assertion that OrganizerConcertHandler satisfies the generated interface.
var _ organizerv1connect.ConcertServiceHandler = (*OrganizerConcertHandler)(nil)

// OrganizerConcertHandler implements the organizer-facing ConcertService Connect
// interface. Org-scoped authorization is enforced structurally by the
// OrgScopedInterceptor before this handler runs; this handler only resolves the
// caller's organizer, enforces ownership, and delegates to the authoring or
// media use case. No business logic lives here.
type OrganizerConcertHandler struct {
	authoringUC usecase.ConcertAuthoringUseCase
	organizerUC usecase.OrganizerUseCase
	mediaUC     usecase.MediaUseCase
	logger      *logging.Logger
}

// NewOrganizerConcertHandler creates a new OrganizerConcertHandler.
func NewOrganizerConcertHandler(
	authoringUC usecase.ConcertAuthoringUseCase,
	organizerUC usecase.OrganizerUseCase,
	mediaUC usecase.MediaUseCase,
	logger *logging.Logger,
) *OrganizerConcertHandler {
	return &OrganizerConcertHandler{
		authoringUC: authoringUC,
		organizerUC: organizerUC,
		mediaUC:     mediaUC,
		logger:      logger,
	}
}

// resolveCallerOrganizer mirrors OrganizerHandler.resolveCallerOrganizer:
// reads the Zitadel org id from context, looks up the Organizer, and enforces
// its lifecycle status. Returns the active Organizer or a Connect error.
func (h *OrganizerConcertHandler) resolveCallerOrganizer(ctx context.Context) (*entity.Organizer, error) {
	callerOrgID, ok := auth.GetCallerOrgID(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("permission denied"))
	}

	organizer, err := h.organizerUC.GetByZitadelOrgID(ctx, callerOrgID)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return nil, connect.NewError(connect.CodePermissionDenied, errors.New("permission denied"))
		}
		return nil, err
	}

	switch organizer.Status {
	case entity.OrganizerStatusActive:
		return organizer, nil
	case entity.OrganizerStatusDeactivated:
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("organizer is deactivated"))
	default:
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("permission denied"))
	}
}

// seriesDraftToInputs converts the proto SeriesDraft payload into the domain
// types expected by the authoring use case.
func seriesDraftToInputs(draft *organizerv1.SeriesDraft) (*entity.Series, []*usecase.DraftEventInput, []string) {
	seriesType := mapper.SeriesTypeFromProto(draft.GetType())
	visibility := mapper.VisibilityFromProto(draft.GetVisibility())

	s := &entity.Series{
		Title:      draft.GetTitle().GetValue(),
		Type:       seriesType,
		Visibility: &visibility,
	}
	if src := draft.GetSourceUrl(); src != nil {
		s.SourceURL = src.GetValue()
	}
	if desc := draft.GetDescription(); desc != nil {
		v := desc.GetValue()
		s.Description = &v
	}

	artistIDs := make([]string, 0, len(draft.GetArtistIds()))
	for _, aid := range draft.GetArtistIds() {
		artistIDs = append(artistIDs, aid.GetValue())
	}

	eventInputs := make([]*usecase.DraftEventInput, 0, len(draft.GetEvents()))
	for _, ev := range draft.GetEvents() {
		inp := &usecase.DraftEventInput{
			VenueName: ev.GetVenueName().GetValue(),
			LocalDate: mapper.DateToTime(ev.GetLocalDate().GetValue()),
		}
		if pid := ev.GetPlaceId(); pid != nil {
			v := pid.GetValue()
			inp.PlaceID = &v
		}
		if st := ev.GetStartTime(); st != nil && st.GetValue() != nil {
			t := st.GetValue().AsTime()
			inp.StartTime = &t
		}
		if ot := ev.GetOpenTime(); ot != nil && ot.GetValue() != nil {
			t := ot.GetValue().AsTime()
			inp.OpenTime = &t
		}
		eventInputs = append(eventInputs, inp)
	}
	return s, eventInputs, artistIDs
}

// Create authors a new first-party concert draft.
func (h *OrganizerConcertHandler) Create(
	ctx context.Context,
	req *connect.Request[organizerv1.CreateRequest],
) (*connect.Response[organizerv1.CreateResponse], error) {
	organizer, err := h.resolveCallerOrganizer(ctx)
	if err != nil {
		return nil, err
	}

	s, eventInputs, artistIDs := seriesDraftToInputs(req.Msg.GetDraft())
	series, events, artists, err := h.authoringUC.CreateDraft(ctx, organizer.ID, s, eventInputs, artistIDs)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&organizerv1.CreateResponse{
		Concert: mapper.AuthoredConcertToProto(series, events, artists),
	}), nil
}

// Update edits an existing series.
func (h *OrganizerConcertHandler) Update(
	ctx context.Context,
	req *connect.Request[organizerv1.UpdateRequest],
) (*connect.Response[organizerv1.UpdateResponse], error) {
	organizer, err := h.resolveCallerOrganizer(ctx)
	if err != nil {
		return nil, err
	}

	seriesID := req.Msg.GetSeriesId().GetValue()
	s, eventInputs, artistIDs := seriesDraftToInputs(req.Msg.GetDraft())
	series, events, artists, err := h.authoringUC.UpdateDraft(ctx, organizer.ID, seriesID, s, eventInputs, artistIDs)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&organizerv1.UpdateResponse{
		Concert: mapper.AuthoredConcertToProto(series, events, artists),
	}), nil
}

// Publish transitions a DRAFT series to PUBLISHED.
func (h *OrganizerConcertHandler) Publish(
	ctx context.Context,
	req *connect.Request[organizerv1.PublishRequest],
) (*connect.Response[organizerv1.PublishResponse], error) {
	organizer, err := h.resolveCallerOrganizer(ctx)
	if err != nil {
		return nil, err
	}

	seriesID := req.Msg.GetSeriesId().GetValue()
	series, events, artists, err := h.authoringUC.Publish(ctx, organizer.ID, seriesID)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&organizerv1.PublishResponse{
		Concert: mapper.AuthoredConcertToProto(series, events, artists),
	}), nil
}

// Cancel marks a series CANCELLED.
func (h *OrganizerConcertHandler) Cancel(
	ctx context.Context,
	req *connect.Request[organizerv1.CancelRequest],
) (*connect.Response[organizerv1.CancelResponse], error) {
	organizer, err := h.resolveCallerOrganizer(ctx)
	if err != nil {
		return nil, err
	}

	if err := h.authoringUC.Cancel(ctx, organizer.ID, req.Msg.GetSeriesId().GetValue()); err != nil {
		return nil, err
	}

	return connect.NewResponse(&organizerv1.CancelResponse{}), nil
}

// CreateMediaUploadURL mints a signed GCS PUT URL for a direct-to-storage upload.
func (h *OrganizerConcertHandler) CreateMediaUploadURL(
	ctx context.Context,
	req *connect.Request[organizerv1.CreateMediaUploadURLRequest],
) (*connect.Response[organizerv1.CreateMediaUploadURLResponse], error) {
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

	return connect.NewResponse(&organizerv1.CreateMediaUploadURLResponse{
		UploadUrl: &entityv1.Url{Value: out.UploadURL},
		MediaId:   &entityv1.MediaId{Value: out.MediaID},
		MaxBytes:  out.MaxBytes,
	}), nil
}

// AttachMedia records an uploaded media object as belonging to a series and
// publishes MEDIA.uploaded so the processor generates variants.
func (h *OrganizerConcertHandler) AttachMedia(
	ctx context.Context,
	req *connect.Request[organizerv1.AttachMediaRequest],
) (*connect.Response[organizerv1.AttachMediaResponse], error) {
	organizer, err := h.resolveCallerOrganizer(ctx)
	if err != nil {
		return nil, err
	}

	if err := h.mediaUC.AttachMedia(
		ctx, organizer.ID,
		req.Msg.GetSeriesId().GetValue(),
		req.Msg.GetMediaId().GetValue(),
	); err != nil {
		return nil, err
	}

	return connect.NewResponse(&organizerv1.AttachMediaResponse{}), nil
}

// RegenerateToken issues a fresh share token for an UNLISTED series.
func (h *OrganizerConcertHandler) RegenerateToken(
	ctx context.Context,
	req *connect.Request[organizerv1.RegenerateTokenRequest],
) (*connect.Response[organizerv1.RegenerateTokenResponse], error) {
	organizer, err := h.resolveCallerOrganizer(ctx)
	if err != nil {
		return nil, err
	}

	token, err := h.authoringUC.RegenerateToken(ctx, organizer.ID, req.Msg.GetSeriesId().GetValue())
	if err != nil {
		return nil, err
	}

	// Return the raw token as the share URL value; the frontend appends the
	// base URL. This matches the unlisted-token design where only the token
	// component is backend-authoritative.
	return connect.NewResponse(&organizerv1.RegenerateTokenResponse{
		ShareUrl: &entityv1.Url{Value: token},
	}), nil
}

// List returns the concerts authored by the caller's own Organizer.
func (h *OrganizerConcertHandler) List(
	ctx context.Context,
	_ *connect.Request[organizerv1.ListRequest],
) (*connect.Response[organizerv1.ListResponse], error) {
	organizer, err := h.resolveCallerOrganizer(ctx)
	if err != nil {
		return nil, err
	}

	allSeries, allEvents, allArtists, err := h.authoringUC.ListOwn(ctx, organizer.ID)
	if err != nil {
		return nil, err
	}

	concerts := make([]*organizerv1.AuthoredConcert, 0, len(allSeries))
	for i, s := range allSeries {
		var evs []*entity.Event
		var arts []*entity.Artist
		if allEvents[i] != nil {
			evs = *allEvents[i]
		}
		if allArtists[i] != nil {
			arts = *allArtists[i]
		}
		concerts = append(concerts, mapper.AuthoredConcertToProto(s, evs, arts))
	}

	return connect.NewResponse(&organizerv1.ListResponse{Concerts: concerts}), nil
}
