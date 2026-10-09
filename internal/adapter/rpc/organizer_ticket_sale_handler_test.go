package rpc_test

import (
	"context"
	"testing"
	"time"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	ticketsalev1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/organizer/ticket_sale/v1"
	"connectrpc.com/connect"
	handler "github.com/liverty-music/backend/internal/adapter/rpc"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/auth"
	"github.com/liverty-music/backend/internal/usecase"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func newOrganizerTicketSaleHandler(t *testing.T) (*handler.OrganizerTicketSaleHandler, *ucmocks.MockTicketSaleUseCase, *ucmocks.MockOrganizerUseCase) {
	t.Helper()
	sales := ucmocks.NewMockTicketSaleUseCase(t)
	orgs := ucmocks.NewMockOrganizerUseCase(t)
	logger, err := logging.New()
	require.NoError(t, err)
	return handler.NewOrganizerTicketSaleHandler(sales, orgs, logger), sales, orgs
}

func TestOrganizerTicketSaleHandler(t *testing.T) {
	t.Parallel()
	ctx := auth.WithCallerOrgID(context.Background(), "zorg-1")
	start := time.Date(2026, 11, 1, 1, 0, 0, 0, time.UTC)

	configureReq := func() *ticketsalev1.ConfigureRequest {
		return &ticketsalev1.ConfigureRequest{
			EventId:       &entityv1.EventId{Value: testEventID},
			SaleStartTime: timestamppb.New(start),
			Price:         3000,
			Quantity:      150,
		}
	}

	t.Run("operator configures a sale", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/organizer/api/rpc/ticket-sale "Operator configures a sale"
		h, sales, orgs := newOrganizerTicketSaleHandler(t)
		orgs.EXPECT().ResolveCaller(ctx, "zorg-1").Return(&entity.Organizer{ID: "org-1"}, nil)
		sales.EXPECT().Configure(ctx, "org-1", usecase.ConfigureTicketSaleInput{
			EventID: testEventID, SaleStart: start, Price: 3000, Quantity: 150,
		}).Return(&entity.TicketSale{ID: "019a0000-0000-7000-8000-0000000000f4", EventID: testEventID, Method: entity.TicketSaleMethodFirstCome,
			SaleStartTime: start, SaleEndTime: start.Add(19 * 24 * time.Hour), Price: 3000, Quantity: 150, PerAccountLimit: 4}, nil)

		resp, err := h.Configure(ctx, connect.NewRequest(configureReq()))

		require.NoError(t, err)
		assert.Equal(t, int32(150), resp.Msg.TicketSale.GetQuantity())
		assert.Equal(t, int32(4), resp.Msg.TicketSale.PerAccountLimit)
	})

	t.Run("optional sale end and limit are passed on", func(t *testing.T) {
		t.Parallel()
		h, sales, orgs := newOrganizerTicketSaleHandler(t)
		orgs.EXPECT().ResolveCaller(ctx, "zorg-1").Return(&entity.Organizer{ID: "org-1"}, nil)
		end := start.Add(48 * time.Hour)
		limit := int32(2)
		sales.EXPECT().Configure(ctx, "org-1", mock.MatchedBy(func(in usecase.ConfigureTicketSaleInput) bool {
			return in.SaleEnd != nil && in.SaleEnd.Equal(end) && in.PerAccountLimit != nil && *in.PerAccountLimit == 2
		})).Return(&entity.TicketSale{ID: "019a0000-0000-7000-8000-0000000000f4"}, nil)
		req := configureReq()
		req.SaleEndTime, req.PerAccountLimit = timestamppb.New(end), &limit

		_, err := h.Configure(ctx, connect.NewRequest(req))

		require.NoError(t, err)
	})

	t.Run("operator sees how many sold", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/organizer/api/rpc/ticket-sale "Operator sees how many sold"
		h, sales, orgs := newOrganizerTicketSaleHandler(t)
		orgs.EXPECT().ResolveCaller(ctx, "zorg-1").Return(&entity.Organizer{ID: "org-1"}, nil)
		sales.EXPECT().GetOwn(ctx, "org-1", testEventID).Return(&usecase.TicketSaleView{
			Sale:  &entity.TicketSale{ID: "019a0000-0000-7000-8000-0000000000f4", EventID: testEventID, Quantity: 150, SoldCount: 100, HeldCount: 6, HasReservations: true},
			State: entity.TicketSaleStateOnSale,
		}, nil)

		resp, err := h.Get(ctx, connect.NewRequest(&ticketsalev1.GetRequest{EventId: &entityv1.EventId{Value: testEventID}}))

		require.NoError(t, err)
		assert.Equal(t, int32(150), resp.Msg.TicketSale.GetQuantity())
		assert.Equal(t, int32(100), resp.Msg.TicketSale.GetSoldCount())
		assert.Equal(t, int32(6), resp.Msg.HeldCount)
		assert.True(t, resp.Msg.PriceLocked)
		assert.Equal(t, entityv1.TicketSaleState_TICKET_SALE_STATE_ON_SALE, resp.Msg.State)
	})

	t.Run("deactivated organizer", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/organizer/api/rpc/ticket-sale "Deactivated Organizer"
		h, _, orgs := newOrganizerTicketSaleHandler(t)
		deactivated := apperr.New(apperr.ErrFailedPrecondition.Code, "organizer is deactivated")
		orgs.EXPECT().ResolveCaller(ctx, "zorg-1").Return(nil, deactivated).Twice()

		_, err := h.Configure(ctx, connect.NewRequest(configureReq()))
		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		_, err = h.Get(ctx, connect.NewRequest(&ticketsalev1.GetRequest{EventId: &entityv1.EventId{Value: testEventID}}))
		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("missing price", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/organizer/api/rpc/ticket-sale "Missing price"
		h, _, _ := newOrganizerTicketSaleHandler(t)
		req := configureReq()
		req.Price = 0

		_, err := h.Configure(ctx, connect.NewRequest(req))

		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	})

	t.Run("missing event", func(t *testing.T) {
		t.Parallel()
		h, _, _ := newOrganizerTicketSaleHandler(t)

		_, err := h.Get(ctx, connect.NewRequest(&ticketsalev1.GetRequest{}))

		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	})
}
