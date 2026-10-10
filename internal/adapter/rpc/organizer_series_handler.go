package rpc

import (
	"context"
	"errors"

	organizerseriesv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/organizer/series/v1/seriesv1connect"
	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	organizerseriesv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/organizer/series/v1"
	"connectrpc.com/connect"
	"github.com/liverty-music/backend/internal/adapter/rpc/mapper"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/auth"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-logging/logging"
)

// Compile-time assertion that OrganizerSeriesHandler satisfies the generated interface.
var _ organizerseriesv1connect.SeriesServiceHandler = (*OrganizerSeriesHandler)(nil)

// OrganizerSeriesHandler implements the organizer-facing SeriesService Connect
// interface. Org-scoped authorization is enforced structurally by the
// OrgScopedInterceptor before this handler runs; this handler only resolves the
// caller's organizer, enforces ownership, and delegates to the authoring or
// media use case. No business logic lives here.
type OrganizerSeriesHandler struct {
	authoringUC     usecase.ConcertAuthoringUseCase
	organizerUC     usecase.OrganizerUseCase
	mediaUC         usecase.MediaUseCase
	mediaURLBuilder *mapper.MediaURLBuilder
	logger          *logging.Logger
}

// NewOrganizerSeriesHandler creates a new OrganizerSeriesHandler.
func NewOrganizerSeriesHandler(
	authoringUC usecase.ConcertAuthoringUseCase,
	organizerUC usecase.OrganizerUseCase,
	mediaUC usecase.MediaUseCase,
	mediaURLBuilder *mapper.MediaURLBuilder,
	logger *logging.Logger,
) *OrganizerSeriesHandler {
	return &OrganizerSeriesHandler{
		authoringUC:     authoringUC,
		organizerUC:     organizerUC,
		mediaUC:         mediaUC,
		mediaURLBuilder: mediaURLBuilder,
		logger:          logger,
	}
}

// resolveCallerOrganizer mirrors OrganizerHandler.resolveCallerOrganizer:
// reads the Zitadel org id from context and delegates to the usecase, which
// looks up the Organizer and enforces its lifecycle status. Returns the
// active Organizer or the usecase's error.
func (h *OrganizerSeriesHandler) resolveCallerOrganizer(ctx context.Context) (*entity.Organizer, error) {
	callerOrgID, ok := auth.GetCallerOrgID(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("permission denied"))
	}

	return h.organizerUC.ResolveCaller(ctx, callerOrgID)
}

// seriesDraftToInputs converts the proto SeriesDraft payload into the domain
// types expected by the authoring use case.
func seriesDraftToInputs(draft *organizerseriesv1.SeriesDraft) (*entity.Series, []*usecase.DraftEventInput, []string) {
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

// Create authors a new first-party series draft and returns it with its
// dates as Concerts and its artists.
func (h *OrganizerSeriesHandler) Create(
	ctx context.Context,
	req *connect.Request[organizerseriesv1.CreateRequest],
) (*connect.Response[organizerseriesv1.CreateResponse], error) {
	organizer, err := h.resolveCallerOrganizer(ctx)
	if err != nil {
		return nil, err
	}

	s, eventInputs, artistIDs := seriesDraftToInputs(req.Msg.GetDraft())
	series, events, artists, err := h.authoringUC.CreateDraft(ctx, organizer.ID, s, eventInputs, artistIDs)
	if err != nil {
		return nil, err
	}

	concerts := mapper.AuthoredConcerts(series, events, artists)
	return connect.NewResponse(&organizerseriesv1.CreateResponse{
		Series:   h.mediaURLBuilder.SeriesToProto(series),
		Concerts: mapper.ConcertsToProto(concerts),
		Artists:  mapper.ReferencedArtists(concerts),
	}), nil
}

// Update edits an existing series.
func (h *OrganizerSeriesHandler) Update(
	ctx context.Context,
	req *connect.Request[organizerseriesv1.UpdateRequest],
) (*connect.Response[organizerseriesv1.UpdateResponse], error) {
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

	concerts := mapper.AuthoredConcerts(series, events, artists)
	return connect.NewResponse(&organizerseriesv1.UpdateResponse{
		Series:   h.mediaURLBuilder.SeriesToProto(series),
		Concerts: mapper.ConcertsToProto(concerts),
		Artists:  mapper.ReferencedArtists(concerts),
	}), nil
}

// Publish transitions a DRAFT series to PUBLISHED.
func (h *OrganizerSeriesHandler) Publish(
	ctx context.Context,
	req *connect.Request[organizerseriesv1.PublishRequest],
) (*connect.Response[organizerseriesv1.PublishResponse], error) {
	organizer, err := h.resolveCallerOrganizer(ctx)
	if err != nil {
		return nil, err
	}

	seriesID := req.Msg.GetSeriesId().GetValue()
	series, events, artists, err := h.authoringUC.Publish(ctx, organizer.ID, seriesID)
	if err != nil {
		return nil, err
	}

	concerts := mapper.AuthoredConcerts(series, events, artists)
	return connect.NewResponse(&organizerseriesv1.PublishResponse{
		Series:   h.mediaURLBuilder.SeriesToProto(series),
		Concerts: mapper.ConcertsToProto(concerts),
		Artists:  mapper.ReferencedArtists(concerts),
	}), nil
}

// Cancel marks a series CANCELLED.
func (h *OrganizerSeriesHandler) Cancel(
	ctx context.Context,
	req *connect.Request[organizerseriesv1.CancelRequest],
) (*connect.Response[organizerseriesv1.CancelResponse], error) {
	organizer, err := h.resolveCallerOrganizer(ctx)
	if err != nil {
		return nil, err
	}

	if err := h.authoringUC.Cancel(ctx, organizer.ID, req.Msg.GetSeriesId().GetValue()); err != nil {
		return nil, err
	}

	return connect.NewResponse(&organizerseriesv1.CancelResponse{}), nil
}

// CreateMediaUploadURL mints a signed GCS PUT URL for a direct-to-storage upload.
func (h *OrganizerSeriesHandler) CreateMediaUploadURL(
	ctx context.Context,
	req *connect.Request[organizerseriesv1.CreateMediaUploadURLRequest],
) (*connect.Response[organizerseriesv1.CreateMediaUploadURLResponse], error) {
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

	return connect.NewResponse(&organizerseriesv1.CreateMediaUploadURLResponse{
		UploadUrl: &entityv1.Url{Value: out.UploadURL},
		MediaId:   &entityv1.MediaId{Value: out.MediaID},
		MaxBytes:  out.MaxBytes,
	}), nil
}

// AttachMedia records an uploaded media object as belonging to a series and
// publishes MEDIA.uploaded so the processor generates variants.
func (h *OrganizerSeriesHandler) AttachMedia(
	ctx context.Context,
	req *connect.Request[organizerseriesv1.AttachMediaRequest],
) (*connect.Response[organizerseriesv1.AttachMediaResponse], error) {
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

	return connect.NewResponse(&organizerseriesv1.AttachMediaResponse{}), nil
}

// RegenerateToken issues a fresh share token for an UNLISTED series.
func (h *OrganizerSeriesHandler) RegenerateToken(
	ctx context.Context,
	req *connect.Request[organizerseriesv1.RegenerateTokenRequest],
) (*connect.Response[organizerseriesv1.RegenerateTokenResponse], error) {
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
	return connect.NewResponse(&organizerseriesv1.RegenerateTokenResponse{
		ShareUrl: &entityv1.Url{Value: token},
	}), nil
}

// List returns the series authored by the caller's own Organizer, with their
// dates as Concerts and each artist once.
func (h *OrganizerSeriesHandler) List(
	ctx context.Context,
	_ *connect.Request[organizerseriesv1.ListRequest],
) (*connect.Response[organizerseriesv1.ListResponse], error) {
	organizer, err := h.resolveCallerOrganizer(ctx)
	if err != nil {
		return nil, err
	}

	allSeries, allEvents, allArtists, err := h.authoringUC.ListOwn(ctx, organizer.ID)
	if err != nil {
		return nil, err
	}

	series := make([]*entityv1.Series, 0, len(allSeries))
	var concerts []*entity.Concert
	for i, s := range allSeries {
		var evs []*entity.Event
		var arts []*entity.Artist
		if allEvents[i] != nil {
			evs = *allEvents[i]
		}
		if allArtists[i] != nil {
			arts = *allArtists[i]
		}
		series = append(series, h.mediaURLBuilder.SeriesToProto(s))
		concerts = append(concerts, mapper.AuthoredConcerts(s, evs, arts)...)
	}

	return connect.NewResponse(&organizerseriesv1.ListResponse{
		Series:   series,
		Concerts: mapper.ConcertsToProto(concerts),
		Artists:  mapper.ReferencedArtists(concerts),
	}), nil
}
