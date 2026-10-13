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
// caller's organizer, enforces ownership, and delegates to the Series use case.
// No business logic lives here.
type OrganizerSeriesHandler struct {
	seriesUC              usecase.SeriesUseCase
	organizerUC           usecase.OrganizerUseCase
	seriesImageURLBuilder *mapper.SeriesImageURLBuilder
	logger                *logging.Logger
}

// NewOrganizerSeriesHandler creates a new OrganizerSeriesHandler.
func NewOrganizerSeriesHandler(
	seriesUC usecase.SeriesUseCase,
	organizerUC usecase.OrganizerUseCase,
	seriesImageURLBuilder *mapper.SeriesImageURLBuilder,
	logger *logging.Logger,
) *OrganizerSeriesHandler {
	return &OrganizerSeriesHandler{
		seriesUC:              seriesUC,
		organizerUC:           organizerUC,
		seriesImageURLBuilder: seriesImageURLBuilder,
		logger:                logger,
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
		if eid := ev.GetEventId(); eid != nil {
			v := eid.GetValue()
			inp.EventID = &v
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

// eventIDValues unwraps the event ids of a request; nil when none is named.
func eventIDValues(ids []*entityv1.EventId) []string {
	if len(ids) == 0 {
		return nil
	}
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = id.GetValue()
	}
	return out
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
	series, concerts, err := h.seriesUC.Create(ctx, organizer.ID, s, eventInputs, artistIDs)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&organizerseriesv1.CreateResponse{
		Series:   h.seriesImageURLBuilder.SeriesToProto(series),
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
	series, concerts, err := h.seriesUC.Update(ctx, organizer.ID, seriesID, s, eventInputs, artistIDs)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&organizerseriesv1.UpdateResponse{
		Series:   h.seriesImageURLBuilder.SeriesToProto(series),
		Concerts: mapper.ConcertsToProto(concerts),
		Artists:  mapper.ReferencedArtists(concerts),
	}), nil
}

// Publish publishes the named DRAFT events of a series, or all of them.
func (h *OrganizerSeriesHandler) Publish(
	ctx context.Context,
	req *connect.Request[organizerseriesv1.PublishRequest],
) (*connect.Response[organizerseriesv1.PublishResponse], error) {
	organizer, err := h.resolveCallerOrganizer(ctx)
	if err != nil {
		return nil, err
	}

	seriesID := req.Msg.GetSeriesId().GetValue()
	series, concerts, err := h.seriesUC.Publish(ctx, organizer.ID, seriesID, eventIDValues(req.Msg.GetEventIds()))
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&organizerseriesv1.PublishResponse{
		Series:   h.seriesImageURLBuilder.SeriesToProto(series),
		Concerts: mapper.ConcertsToProto(concerts),
		Artists:  mapper.ReferencedArtists(concerts),
	}), nil
}

// Cancel cancels the named events of a series, or all its published and
// draft events.
func (h *OrganizerSeriesHandler) Cancel(
	ctx context.Context,
	req *connect.Request[organizerseriesv1.CancelRequest],
) (*connect.Response[organizerseriesv1.CancelResponse], error) {
	organizer, err := h.resolveCallerOrganizer(ctx)
	if err != nil {
		return nil, err
	}

	if err := h.seriesUC.Cancel(ctx, organizer.ID, req.Msg.GetSeriesId().GetValue(), eventIDValues(req.Msg.GetEventIds())); err != nil {
		return nil, err
	}

	return connect.NewResponse(&organizerseriesv1.CancelResponse{}), nil
}

// SetCoverImage records an uploaded Series image as the series' cover and
// publishes SERIES_IMAGE.uploaded so the processor generates variants.
func (h *OrganizerSeriesHandler) SetCoverImage(
	ctx context.Context,
	req *connect.Request[organizerseriesv1.SetCoverImageRequest],
) (*connect.Response[organizerseriesv1.SetCoverImageResponse], error) {
	organizer, err := h.resolveCallerOrganizer(ctx)
	if err != nil {
		return nil, err
	}

	if err := h.seriesUC.SetCoverImage(
		ctx, organizer.ID,
		req.Msg.GetSeriesId().GetValue(),
		req.Msg.GetSeriesImageId().GetValue(),
	); err != nil {
		return nil, err
	}

	return connect.NewResponse(&organizerseriesv1.SetCoverImageResponse{}), nil
}

// RegenerateShareUrl issues a fresh share token for an UNLISTED series; the
// previous share link stops working.
func (h *OrganizerSeriesHandler) RegenerateShareUrl(
	ctx context.Context,
	req *connect.Request[organizerseriesv1.RegenerateShareUrlRequest],
) (*connect.Response[organizerseriesv1.RegenerateShareUrlResponse], error) {
	organizer, err := h.resolveCallerOrganizer(ctx)
	if err != nil {
		return nil, err
	}

	token, err := h.seriesUC.RegenerateShareURL(ctx, organizer.ID, req.Msg.GetSeriesId().GetValue())
	if err != nil {
		return nil, err
	}

	// Return the raw token as the share URL value; the frontend appends the
	// base URL. This matches the unlisted-token design where only the token
	// component is backend-authoritative.
	return connect.NewResponse(&organizerseriesv1.RegenerateShareUrlResponse{
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

	allSeries, concerts, err := h.seriesUC.List(ctx, organizer.ID)
	if err != nil {
		return nil, err
	}

	series := make([]*entityv1.Series, 0, len(allSeries))
	for _, s := range allSeries {
		series = append(series, h.seriesImageURLBuilder.SeriesToProto(s))
	}

	return connect.NewResponse(&organizerseriesv1.ListResponse{
		Series:   series,
		Concerts: mapper.ConcertsToProto(concerts),
		Artists:  mapper.ReferencedArtists(concerts),
	}), nil
}
