package rpc_test

import (
	"context"
	"testing"

	ticketstockv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/organizer/ticket_stock/v1/ticket_stockv1connect"
	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	ticketstockv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/organizer/ticket_stock/v1"
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

// organizerTicketStockFixture serves an OrganizerTicketStockHandler behind
// the validation interceptor. The usecase mocks fail the test on any call not
// expected.
type organizerTicketStockFixture struct {
	stocks *ucmocks.MockTicketStockUseCase
	orgs   *ucmocks.MockOrganizerUseCase
	client ticketstockv1connect.TicketStockServiceClient
}

func newOrganizerTicketStockFixture(t *testing.T, decorate func(context.Context) context.Context) *organizerTicketStockFixture {
	t.Helper()
	f := &organizerTicketStockFixture{stocks: ucmocks.NewMockTicketStockUseCase(t), orgs: ucmocks.NewMockOrganizerUseCase(t)}
	logger, err := logging.New()
	require.NoError(t, err)
	h := handler.NewOrganizerTicketStockHandler(f.stocks, f.orgs, logger)
	srv := serveValidated(t, func(s *connect.Server) {
		ticketstockv1connect.RegisterTicketStockServiceHandler(s, h)
	}, decorate)
	f.client = ticketstockv1connect.NewTicketStockServiceClient(connect.NewClient(connecthttp.NewTransport(srv.Client(), srv.URL)))
	return f
}

func (f *organizerTicketStockFixture) resolvesTo(organizerID string) {
	f.orgs.EXPECT().ResolveCaller(mock.Anything, testProviderOrganizerID).Return(&entity.Organizer{ID: organizerID}, nil)
}

func TestOrganizerTicketStockHandler(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	createReq := func() *ticketstockv1.CreateRequest {
		return &ticketstockv1.CreateRequest{
			EventId:    &entityv1.EventId{Value: testEventID},
			SeatTypeId: &entityv1.SeatTypeId{Value: tkSeatTypeID},
			Quantity:   150,
		}
	}
	sSeat := &entity.SeatType{ID: tkSeatTypeID, SeriesID: tkSeriesID, Name: "S席", Price: 8000}
	listByEventReq := &ticketstockv1.ListByEventRequest{EventId: &entityv1.EventId{Value: testEventID}}

	t.Run("member stocks an event", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/organizer/api/rpc/ticket-stock "Member stocks an event"
		f := newOrganizerTicketStockFixture(t, asMember(testProviderOrganizerID))
		f.resolvesTo("org-1")
		f.stocks.EXPECT().Create(mock.Anything, "org-1", testEventID, entity.SeatTypeID(tkSeatTypeID), 150).Return(&entity.TicketStock{
			ID: tkStockID, EventID: testEventID, SeatTypeID: tkSeatTypeID, Quantity: 150, SeatType: sSeat,
		}, nil)

		resp, err := f.client.Create(ctx, createReq())

		require.NoError(t, err)
		got := resp.GetTicketStock()
		assert.Equal(t, tkStockID, got.GetId().GetValue())
		assert.Equal(t, testEventID, got.GetEventId().GetValue())
		assert.Equal(t, tkSeatTypeID, got.GetSeatTypeId().GetValue())
		assert.Equal(t, int32(150), got.GetQuantity())
		assert.Equal(t, int32(0), got.GetSoldCount())
		assert.Equal(t, entityv1.TicketStockState_TICKET_STOCK_STATE_AVAILABLE, got.GetState())
	})

	t.Run("member sees the stocks of an event with their counts", func(t *testing.T) {
		t.Parallel()
		f := newOrganizerTicketStockFixture(t, asMember(testProviderOrganizerID))
		f.resolvesTo("org-1")
		f.stocks.EXPECT().ListByEvent(mock.Anything, "org-1", testEventID).Return([]*entity.TicketStock{{
			ID: tkStockID, EventID: testEventID, SeatTypeID: tkSeatTypeID, Quantity: 150, SoldCount: 130, HeldCount: 5, SeatType: sSeat,
		}}, nil)

		resp, err := f.client.ListByEvent(ctx, listByEventReq)

		require.NoError(t, err)
		require.Len(t, resp.GetTicketStocks(), 1)
		got := resp.GetTicketStocks()[0]
		assert.Equal(t, int32(150), got.GetQuantity())
		assert.Equal(t, int32(130), got.GetSoldCount())
		assert.Equal(t, int32(5), got.GetHeldCount())
		assert.True(t, got.GetLowStock(), "15 of 150 remaining is LowStock")
		require.Len(t, resp.GetSeatTypes(), 1)
		assert.Equal(t, "S席", resp.GetSeatTypes()[0].GetName().GetValue())
	})

	t.Run("member deletes a stock", func(t *testing.T) {
		t.Parallel()
		f := newOrganizerTicketStockFixture(t, asMember(testProviderOrganizerID))
		f.resolvesTo("org-1")
		f.stocks.EXPECT().Delete(mock.Anything, "org-1", entity.TicketStockID(tkStockID)).Return(nil)

		_, err := f.client.Delete(ctx, &ticketstockv1.DeleteRequest{TicketStockId: &entityv1.TicketStockId{Value: tkStockID}})

		require.NoError(t, err)
	})

	t.Run("usecase errors are returned unchanged", func(t *testing.T) {
		t.Parallel()
		f := newOrganizerTicketStockFixture(t, asMember(testProviderOrganizerID))
		f.resolvesTo("org-1")
		f.stocks.EXPECT().Create(mock.Anything, "org-1", testEventID, entity.SeatTypeID(tkSeatTypeID), 150).
			Return(nil, apperr.New(codes.AlreadyExists, "event already has the seat type"))

		_, err := f.client.Create(ctx, createReq())

		assert.Equal(t, connect.CodeAlreadyExists, connect.CodeOf(err))
	})

	t.Run("caller without an organization", func(t *testing.T) {
		t.Parallel()
		f := newOrganizerTicketStockFixture(t, signedInAs("op-sub"))

		_, err := f.client.Create(ctx, createReq())

		assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	})

	t.Run("deactivated organizer", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/organizer/api/rpc/ticket-stock "Deactivated Organizer"
		f := newOrganizerTicketStockFixture(t, asMember(testProviderOrganizerID))
		f.orgs.EXPECT().ResolveCaller(mock.Anything, testProviderOrganizerID).
			Return(nil, apperr.New(codes.FailedPrecondition, "organizer is deactivated")).Times(3)

		_, err := f.client.Create(ctx, createReq())
		assert.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
		_, err = f.client.Delete(ctx, &ticketstockv1.DeleteRequest{TicketStockId: &entityv1.TicketStockId{Value: tkStockID}})
		assert.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
		_, err = f.client.ListByEvent(ctx, listByEventReq)
		assert.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
	})

	// The usecase mocks carry no expectations in the cases below: a call that
	// reached them would fail the test, so nothing is stored or changed.
	invalid := []struct {
		name string
		call func(c ticketstockv1connect.TicketStockServiceClient) error
	}{
		{
			// @spec components/adapter/organizer/api/rpc/ticket-stock "Missing quantity"
			name: "missing quantity",
			call: func(c ticketstockv1connect.TicketStockServiceClient) error {
				req := createReq()
				req.Quantity = 0
				_, err := c.Create(ctx, req)
				return err
			},
		},
		{
			name: "create without an event",
			call: func(c ticketstockv1connect.TicketStockServiceClient) error {
				req := createReq()
				req.EventId = nil
				_, err := c.Create(ctx, req)
				return err
			},
		},
		{
			name: "create without a seat type",
			call: func(c ticketstockv1connect.TicketStockServiceClient) error {
				req := createReq()
				req.SeatTypeId = nil
				_, err := c.Create(ctx, req)
				return err
			},
		},
		{
			name: "delete without a ticket stock",
			call: func(c ticketstockv1connect.TicketStockServiceClient) error {
				_, err := c.Delete(ctx, &ticketstockv1.DeleteRequest{})
				return err
			},
		},
		{
			// @spec components/adapter/organizer/api/rpc/ticket-stock "ListByEvent without an event"
			name: "list by event without an event",
			call: func(c ticketstockv1connect.TicketStockServiceClient) error {
				_, err := c.ListByEvent(ctx, &ticketstockv1.ListByEventRequest{})
				return err
			},
		},
	}
	for _, tt := range invalid {
		t.Run(tt.name+" fails with InvalidArgument", func(t *testing.T) {
			t.Parallel()
			f := newOrganizerTicketStockFixture(t, asMember(testProviderOrganizerID))

			err := tt.call(f.client)

			assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
		})
	}
}
