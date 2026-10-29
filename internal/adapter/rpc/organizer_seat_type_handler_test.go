package rpc_test

import (
	"context"
	"testing"

	seattypev1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/organizer/seat_type/v1/seat_typev1connect"
	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	seattypev1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/organizer/seat_type/v1"
	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	handler "github.com/liverty-music/backend/internal/adapter/rpc"
	"github.com/liverty-music/backend/internal/entity"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// organizerSeatTypeFixture serves an OrganizerSeatTypeHandler behind the
// validation interceptor. The usecase mocks fail the test on any call not
// expected.
type organizerSeatTypeFixture struct {
	seatTypes *ucmocks.MockSeatTypeUseCase
	orgs      *ucmocks.MockOrganizerUseCase
	client    seattypev1connect.SeatTypeServiceClient
}

func newOrganizerSeatTypeFixture(t *testing.T, decorate func(context.Context) context.Context) *organizerSeatTypeFixture {
	t.Helper()
	f := &organizerSeatTypeFixture{seatTypes: ucmocks.NewMockSeatTypeUseCase(t), orgs: ucmocks.NewMockOrganizerUseCase(t)}
	logger, err := logging.New()
	require.NoError(t, err)
	h := handler.NewOrganizerSeatTypeHandler(f.seatTypes, f.orgs, logger)
	srv := serveValidated(t, func(s *connect.Server) {
		seattypev1connect.RegisterSeatTypeServiceHandler(s, h)
	}, decorate)
	f.client = seattypev1connect.NewSeatTypeServiceClient(connect.NewClient(connecthttp.NewTransport(srv.Client(), srv.URL)))
	return f
}

func (f *organizerSeatTypeFixture) resolvesTo(organizerID string) {
	f.orgs.EXPECT().ResolveCaller(mock.Anything, testProviderOrganizerID).Return(&entity.Organizer{ID: organizerID}, nil)
}

func TestOrganizerSeatTypeHandler(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	createReq := func() *seattypev1.CreateRequest {
		return &seattypev1.CreateRequest{
			SeriesId: &entityv1.SeriesId{Value: tkSeriesID},
			Name:     &entityv1.SeatTypeName{Value: "S席"},
			Price:    &entityv1.SeatTypePrice{Value: 8000},
		}
	}
	sSeat := &entity.SeatType{ID: tkSeatTypeID, SeriesID: tkSeriesID, Name: "S席", Price: 8000}

	t.Run("member adds a seat type", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/organizer/api/rpc/seat-type "Member adds a seat type"
		f := newOrganizerSeatTypeFixture(t, asMember(testProviderOrganizerID))
		f.resolvesTo("org-1")
		f.seatTypes.EXPECT().Create(mock.Anything, "org-1", tkSeriesID, "S席", int64(8000)).Return(sSeat, nil)

		resp, err := f.client.Create(ctx, createReq())

		require.NoError(t, err)
		got := resp.GetSeatType()
		assert.Equal(t, tkSeatTypeID, got.GetId().GetValue())
		assert.Equal(t, tkSeriesID, got.GetSeriesId().GetValue())
		assert.Equal(t, "S席", got.GetName().GetValue())
		assert.Equal(t, int64(8000), got.GetPrice().GetValue())
	})

	t.Run("member deletes a seat type", func(t *testing.T) {
		t.Parallel()
		f := newOrganizerSeatTypeFixture(t, asMember(testProviderOrganizerID))
		f.resolvesTo("org-1")
		f.seatTypes.EXPECT().Delete(mock.Anything, "org-1", entity.SeatTypeID(tkSeatTypeID)).Return(nil)

		_, err := f.client.Delete(ctx, &seattypev1.DeleteRequest{SeatTypeId: &entityv1.SeatTypeId{Value: tkSeatTypeID}})

		require.NoError(t, err)
	})

	t.Run("member lists a series' seat types", func(t *testing.T) {
		t.Parallel()
		f := newOrganizerSeatTypeFixture(t, asMember(testProviderOrganizerID))
		f.resolvesTo("org-1")
		f.seatTypes.EXPECT().ListBySeries(mock.Anything, "org-1", tkSeriesID).Return([]*entity.SeatType{sSeat}, nil)

		resp, err := f.client.ListBySeries(ctx, &seattypev1.ListBySeriesRequest{SeriesId: &entityv1.SeriesId{Value: tkSeriesID}})

		require.NoError(t, err)
		require.Len(t, resp.GetSeatTypes(), 1)
		assert.Equal(t, tkSeatTypeID, resp.GetSeatTypes()[0].GetId().GetValue())
	})

	t.Run("usecase errors are returned unchanged", func(t *testing.T) {
		t.Parallel()
		f := newOrganizerSeatTypeFixture(t, asMember(testProviderOrganizerID))
		f.resolvesTo("org-1")
		f.seatTypes.EXPECT().Delete(mock.Anything, "org-1", entity.SeatTypeID(tkSeatTypeID)).
			Return(apperr.New(codes.FailedPrecondition, "seat type is stocked"))

		_, err := f.client.Delete(ctx, &seattypev1.DeleteRequest{SeatTypeId: &entityv1.SeatTypeId{Value: tkSeatTypeID}})

		assert.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
	})

	t.Run("caller without an organization", func(t *testing.T) {
		t.Parallel()
		f := newOrganizerSeatTypeFixture(t, signedInAs("op-sub"))

		_, err := f.client.Create(ctx, createReq())

		assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	})

	t.Run("deactivated organizer", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/organizer/api/rpc/seat-type "Deactivated Organizer"
		f := newOrganizerSeatTypeFixture(t, asMember(testProviderOrganizerID))
		f.orgs.EXPECT().ResolveCaller(mock.Anything, testProviderOrganizerID).
			Return(nil, apperr.New(codes.FailedPrecondition, "organizer is deactivated")).Times(3)

		_, err := f.client.Create(ctx, createReq())
		assert.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
		_, err = f.client.Delete(ctx, &seattypev1.DeleteRequest{SeatTypeId: &entityv1.SeatTypeId{Value: tkSeatTypeID}})
		assert.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
		_, err = f.client.ListBySeries(ctx, &seattypev1.ListBySeriesRequest{SeriesId: &entityv1.SeriesId{Value: tkSeriesID}})
		assert.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
	})

	// The usecase mocks carry no expectations in the cases below: a call that
	// reached them would fail the test, so nothing is stored or changed.
	invalid := []struct {
		name string
		call func(c seattypev1connect.SeatTypeServiceClient) error
	}{
		{
			// @spec components/adapter/organizer/api/rpc/seat-type "Missing price"
			name: "missing price",
			call: func(c seattypev1connect.SeatTypeServiceClient) error {
				req := createReq()
				req.Price = nil
				_, err := c.Create(ctx, req)
				return err
			},
		},
		{
			name: "zero price",
			call: func(c seattypev1connect.SeatTypeServiceClient) error {
				req := createReq()
				req.Price = &entityv1.SeatTypePrice{Value: 0}
				_, err := c.Create(ctx, req)
				return err
			},
		},
		{
			name: "create without a name",
			call: func(c seattypev1connect.SeatTypeServiceClient) error {
				req := createReq()
				req.Name = nil
				_, err := c.Create(ctx, req)
				return err
			},
		},
		{
			name: "create without a series",
			call: func(c seattypev1connect.SeatTypeServiceClient) error {
				req := createReq()
				req.SeriesId = nil
				_, err := c.Create(ctx, req)
				return err
			},
		},
		{
			// @spec components/adapter/organizer/api/rpc/seat-type "Delete without a seat type"
			name: "delete without a seat type",
			call: func(c seattypev1connect.SeatTypeServiceClient) error {
				_, err := c.Delete(ctx, &seattypev1.DeleteRequest{})
				return err
			},
		},
		{
			name: "list by series without a series",
			call: func(c seattypev1connect.SeatTypeServiceClient) error {
				_, err := c.ListBySeries(ctx, &seattypev1.ListBySeriesRequest{})
				return err
			},
		},
	}
	for _, tt := range invalid {
		t.Run(tt.name+" fails with InvalidArgument", func(t *testing.T) {
			t.Parallel()
			f := newOrganizerSeatTypeFixture(t, asMember(testProviderOrganizerID))

			err := tt.call(f.client)

			assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
		})
	}
}
