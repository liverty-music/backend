package rpc_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	organizerseriesv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/organizer/series/v1/seriesv1connect"
	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	organizerseriesv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/organizer/series/v1"
	"connectrpc.com/connect"
	"connectrpc.com/validate"
	"github.com/liverty-music/backend/internal/adapter/rpc"
	"github.com/liverty-music/backend/internal/adapter/rpc/mapper"
	"github.com/liverty-music/backend/internal/entity"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/type/date"
)

// organizerSeriesHandlerDeps bundles the mocked collaborators of
// OrganizerSeriesHandler. None has an expectation unless a test sets one, so
// an unexpected call fails the test.
type organizerSeriesHandlerDeps struct {
	authoringUC *ucmocks.MockConcertAuthoringUseCase
	organizerUC *ucmocks.MockOrganizerUseCase
	mediaUC     *ucmocks.MockMediaUseCase
	handler     *rpc.OrganizerSeriesHandler
}

func newOrganizerSeriesHandlerDeps(t *testing.T) *organizerSeriesHandlerDeps {
	t.Helper()
	logger, err := logging.New()
	require.NoError(t, err)

	d := &organizerSeriesHandlerDeps{
		authoringUC: ucmocks.NewMockConcertAuthoringUseCase(t),
		organizerUC: ucmocks.NewMockOrganizerUseCase(t),
		mediaUC:     ucmocks.NewMockMediaUseCase(t),
	}
	d.handler = rpc.NewOrganizerSeriesHandler(
		d.authoringUC,
		d.organizerUC,
		d.mediaUC,
		mapper.NewMediaURLBuilder("https://cdn.example.com"),
		logger,
	)
	return d
}

// authoredEvent builds a date of an authored Series as the authoring usecase
// returns it.
func authoredEvent(id, seriesID string, day int) *entity.Event {
	return &entity.Event{ID: id, SeriesID: seriesID, LocalDate: time.Date(2026, 12, day, 0, 0, 0, 0, time.UTC)}
}

func TestOrganizerSeriesHandler_List(t *testing.T) {
	t.Parallel()

	// @spec components/adapter/organizer/api/rpc/series "Operator of an active Organizer lists concerts"
	t.Run("return the series authored by the caller's own organizer with their concerts", func(t *testing.T) {
		t.Parallel()
		d := newOrganizerSeriesHandlerDeps(t)

		d.organizerUC.EXPECT().
			ResolveCaller(mock.Anything, testZitadelOrgID).
			Return(activeOrganizer(), nil).
			Once()
		events := []*entity.Event{authoredEvent("event-1", "series-1", 1)}
		artists := []*entity.Artist{{ID: "artist-1", Name: "Artist One"}}
		d.authoringUC.EXPECT().
			ListOwn(mock.Anything, testOrganizerID).
			Return(
				[]*entity.Series{{ID: "series-1", Title: "Tour"}, {ID: "series-2", Title: "Festival"}},
				[]*[]*entity.Event{&events, nil},
				[]*[]*entity.Artist{&artists, nil},
				nil,
			).
			Once()

		resp, err := d.handler.List(orgCtx(testZitadelOrgID), connect.NewRequest(&organizerseriesv1.ListRequest{}))

		require.NoError(t, err)
		require.Len(t, resp.Msg.Series, 2)
		assert.Equal(t, "series-1", resp.Msg.Series[0].GetId().GetValue())
		assert.Equal(t, "series-2", resp.Msg.Series[1].GetId().GetValue())
		require.Len(t, resp.Msg.Concerts, 1)
		assert.Equal(t, "event-1", resp.Msg.Concerts[0].GetEvent().GetId().GetValue())
		assert.Equal(t, "series-1", resp.Msg.Concerts[0].GetEvent().GetSeriesId().GetValue())
	})

	// @spec components/adapter/organizer/api/rpc/series "Tour with one performer"
	t.Run("return a three-date tour once with three concerts and its artist once", func(t *testing.T) {
		t.Parallel()
		d := newOrganizerSeriesHandlerDeps(t)

		d.organizerUC.EXPECT().
			ResolveCaller(mock.Anything, testZitadelOrgID).
			Return(activeOrganizer(), nil).
			Once()
		events := []*entity.Event{
			authoredEvent("event-1", "series-1", 1),
			authoredEvent("event-2", "series-1", 2),
			authoredEvent("event-3", "series-1", 3),
		}
		artists := []*entity.Artist{{ID: "artist-1", Name: "Artist One"}}
		d.authoringUC.EXPECT().
			ListOwn(mock.Anything, testOrganizerID).
			Return(
				[]*entity.Series{{ID: "series-1", Title: "ARENA TOUR 2026"}},
				[]*[]*entity.Event{&events},
				[]*[]*entity.Artist{&artists},
				nil,
			).
			Once()

		resp, err := d.handler.List(orgCtx(testZitadelOrgID), connect.NewRequest(&organizerseriesv1.ListRequest{}))

		require.NoError(t, err)
		require.Len(t, resp.Msg.Series, 1)
		require.Len(t, resp.Msg.Concerts, 3)
		require.Len(t, resp.Msg.Artists, 1)
		assert.Equal(t, "artist-1", resp.Msg.Artists[0].GetId().GetValue())
		for _, c := range resp.Msg.Concerts {
			assert.Equal(t, "series-1", c.GetEvent().GetSeriesId().GetValue())
			require.Len(t, c.GetArtistIds(), 1)
			assert.Equal(t, "artist-1", c.GetArtistIds()[0].GetValue())
		}
	})
}

func TestOrganizerSeriesHandler_Create(t *testing.T) {
	t.Parallel()

	// @spec components/adapter/organizer/api/rpc/series "Draft series"
	t.Run("return the DRAFT series with one concert per date, each referring to the performer", func(t *testing.T) {
		t.Parallel()
		d := newOrganizerSeriesHandlerDeps(t)

		d.organizerUC.EXPECT().
			ResolveCaller(mock.Anything, testZitadelOrgID).
			Return(activeOrganizer(), nil).
			Once()
		draft := entity.SeriesPublishStateDraft
		series := &entity.Series{ID: "series-1", Title: "ONE MAN LIVE", Type: entity.SeriesTypeSingle, PublishState: &draft}
		events := []*entity.Event{authoredEvent("draft-event-1", "series-1", 1), authoredEvent("draft-event-2", "series-1", 2)}
		artists := []*entity.Artist{{ID: "artist-1", Name: "Artist One"}}
		d.authoringUC.EXPECT().
			CreateDraft(mock.Anything, testOrganizerID, mock.Anything, mock.Anything, []string{"artist-1"}).
			Return(series, events, artists, nil).
			Once()

		resp, err := d.handler.Create(orgCtx(testZitadelOrgID), connect.NewRequest(&organizerseriesv1.CreateRequest{
			Draft: &organizerseriesv1.SeriesDraft{
				Title:      &entityv1.Title{Value: "ONE MAN LIVE"},
				Type:       entityv1.SeriesType_SERIES_TYPE_SINGLE,
				Visibility: entityv1.Visibility_VISIBILITY_PUBLIC,
				ArtistIds:  []*entityv1.ArtistId{{Value: "artist-1"}},
				Events: []*organizerseriesv1.EventDraft{
					{VenueName: &entityv1.VenueName{Value: "WWW"}, LocalDate: &entityv1.LocalDate{Value: &date.Date{Year: 2026, Month: 12, Day: 1}}},
					{VenueName: &entityv1.VenueName{Value: "WWW"}, LocalDate: &entityv1.LocalDate{Value: &date.Date{Year: 2026, Month: 12, Day: 2}}},
				},
			},
		}))

		require.NoError(t, err)
		assert.Equal(t, entityv1.PublishState_PUBLISH_STATE_DRAFT, resp.Msg.GetSeries().GetPublishState())
		require.Len(t, resp.Msg.Concerts, 2)
		assert.Equal(t, "draft-event-1", resp.Msg.Concerts[0].GetEvent().GetId().GetValue())
		assert.Equal(t, "draft-event-2", resp.Msg.Concerts[1].GetEvent().GetId().GetValue())
		for _, c := range resp.Msg.Concerts {
			require.Len(t, c.GetArtistIds(), 1)
			assert.Equal(t, "artist-1", c.GetArtistIds()[0].GetValue())
		}
		require.Len(t, resp.Msg.Artists, 1)
		assert.Equal(t, "artist-1", resp.Msg.Artists[0].GetId().GetValue())
	})

	// @spec components/adapter/organizer/api/rpc/series "Draft without events"
	t.Run("return INVALID_ARGUMENT before any usecase runs when the draft has no event", func(t *testing.T) {
		t.Parallel()
		d := newOrganizerSeriesHandlerDeps(t)
		// No usecase has an expectation: reaching one fails the test.
		path, svc := organizerseriesv1connect.NewSeriesServiceHandler(d.handler, connect.WithInterceptors(validate.NewInterceptor()))
		mux := http.NewServeMux()
		mux.Handle(path, svc)
		srv := httptest.NewServer(mux)
		t.Cleanup(srv.Close)
		client := organizerseriesv1connect.NewSeriesServiceClient(srv.Client(), srv.URL)

		_, err := client.Create(context.Background(), connect.NewRequest(&organizerseriesv1.CreateRequest{
			Draft: &organizerseriesv1.SeriesDraft{
				Title:      &entityv1.Title{Value: "ONE MAN LIVE"},
				Type:       entityv1.SeriesType_SERIES_TYPE_SINGLE,
				Visibility: entityv1.Visibility_VISIBILITY_PUBLIC,
				ArtistIds:  []*entityv1.ArtistId{{Value: "019a0000-0000-7000-8000-0000000000b1"}},
			},
		}))

		require.Error(t, err)
		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	})
}

func TestOrganizerSeriesHandler_RegenerateToken(t *testing.T) {
	t.Parallel()

	// @spec components/adapter/organizer/api/rpc/series "New token"
	t.Run("return the new token the usecase issued", func(t *testing.T) {
		t.Parallel()
		d := newOrganizerSeriesHandlerDeps(t)

		d.organizerUC.EXPECT().
			ResolveCaller(mock.Anything, testZitadelOrgID).
			Return(activeOrganizer(), nil).
			Once()
		d.authoringUC.EXPECT().
			RegenerateToken(mock.Anything, testOrganizerID, "series-1").
			Return("new-token", nil).
			Once()

		resp, err := d.handler.RegenerateToken(orgCtx(testZitadelOrgID), connect.NewRequest(&organizerseriesv1.RegenerateTokenRequest{
			SeriesId: &entityv1.SeriesId{Value: "series-1"},
		}))

		require.NoError(t, err)
		assert.Equal(t, "new-token", resp.Msg.GetShareUrl().GetValue())
	})
}

// TestOrganizerSeriesHandler_ResolveCallerFailure verifies that every
// authoring call returns a failure of OrganizerUseCase.ResolveCaller unchanged
// and reaches neither the authoring nor the media usecase, so nothing is
// stored.
func TestOrganizerSeriesHandler_ResolveCallerFailure(t *testing.T) {
	t.Parallel()

	calls := []struct {
		name string
		call func(ctx context.Context, h *rpc.OrganizerSeriesHandler) error
	}{
		{"Create", func(ctx context.Context, h *rpc.OrganizerSeriesHandler) error {
			_, err := h.Create(ctx, connect.NewRequest(&organizerseriesv1.CreateRequest{}))
			return err
		}},
		{"Update", func(ctx context.Context, h *rpc.OrganizerSeriesHandler) error {
			_, err := h.Update(ctx, connect.NewRequest(&organizerseriesv1.UpdateRequest{}))
			return err
		}},
		{"Publish", func(ctx context.Context, h *rpc.OrganizerSeriesHandler) error {
			_, err := h.Publish(ctx, connect.NewRequest(&organizerseriesv1.PublishRequest{}))
			return err
		}},
		{"Cancel", func(ctx context.Context, h *rpc.OrganizerSeriesHandler) error {
			_, err := h.Cancel(ctx, connect.NewRequest(&organizerseriesv1.CancelRequest{}))
			return err
		}},
		{"List", func(ctx context.Context, h *rpc.OrganizerSeriesHandler) error {
			_, err := h.List(ctx, connect.NewRequest(&organizerseriesv1.ListRequest{}))
			return err
		}},
		{"RegenerateToken", func(ctx context.Context, h *rpc.OrganizerSeriesHandler) error {
			_, err := h.RegenerateToken(ctx, connect.NewRequest(&organizerseriesv1.RegenerateTokenRequest{}))
			return err
		}},
		{"CreateMediaUploadURL", func(ctx context.Context, h *rpc.OrganizerSeriesHandler) error {
			_, err := h.CreateMediaUploadURL(ctx, connect.NewRequest(&organizerseriesv1.CreateMediaUploadURLRequest{}))
			return err
		}},
		{"AttachMedia", func(ctx context.Context, h *rpc.OrganizerSeriesHandler) error {
			_, err := h.AttachMedia(ctx, connect.NewRequest(&organizerseriesv1.AttachMediaRequest{}))
			return err
		}},
	}

	type dep struct {
		resolveCallerErr error
	}
	tests := []struct {
		name     string
		dep      dep
		wantCode connect.Code
	}{
		{
			// @spec components/adapter/organizer/api/rpc/series "Provisioning Organizer"
			name:     "return PERMISSION_DENIED unchanged when the organizer is provisioning",
			dep:      dep{resolveCallerErr: apperr.New(apperr.ErrPermissionDenied.Code, "permission denied")},
			wantCode: connect.CodePermissionDenied,
		},
		{
			// @spec components/adapter/organizer/api/rpc/series "Deactivated Organizer"
			name:     "return FAILED_PRECONDITION unchanged when the organizer is deactivated",
			dep:      dep{resolveCallerErr: apperr.New(apperr.ErrFailedPrecondition.Code, "organizer is deactivated")},
			wantCode: connect.CodeFailedPrecondition,
		},
	}

	for _, tt := range tests {
		for _, c := range calls {
			t.Run(tt.name+"/"+c.name, func(t *testing.T) {
				t.Parallel()
				d := newOrganizerSeriesHandlerDeps(t)

				// The authoring and media usecases have no expectations: any
				// call to them fails the test.
				d.organizerUC.EXPECT().
					ResolveCaller(mock.Anything, testZitadelOrgID).
					Return(nil, tt.dep.resolveCallerErr).
					Once()

				err := c.call(orgCtx(testZitadelOrgID), d.handler)

				require.Error(t, err)
				assert.ErrorIs(t, err, tt.dep.resolveCallerErr)
				assert.Equal(t, tt.wantCode, connectCodeOf(err))
			})
		}
	}
}
