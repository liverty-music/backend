package rpc_test

import (
	"context"
	"testing"
	"time"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	ticketv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/ticket/v1"
	"connectrpc.com/connect"
	handler "github.com/liverty-music/backend/internal/adapter/rpc"
	"github.com/liverty-music/backend/internal/entity"
	entitymocks "github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/infrastructure/auth"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func ticketAuthedCtx(sub string) context.Context {
	return auth.WithClaims(context.Background(), &auth.Claims{Sub: sub})
}

func newTicketHandler(t *testing.T) (*handler.TicketHandler, *ucmocks.MockTicketUseCase, *entitymocks.MockUserRepository) {
	t.Helper()
	uc := ucmocks.NewMockTicketUseCase(t)
	ur := entitymocks.NewMockUserRepository(t)
	logger, err := logging.New()
	require.NoError(t, err)
	return handler.NewTicketHandler(uc, ur, logger), uc, ur
}

func TestTicketHandler_GetOrder(t *testing.T) {
	t.Parallel()

	t.Run("returns the caller's own order", func(t *testing.T) {
		t.Parallel()
		ctx := ticketAuthedCtx("ext-1")
		h, uc, ur := newTicketHandler(t)
		ur.EXPECT().GetByExternalID(ctx, "ext-1").
			Return(&entity.User{ID: "user-1", ExternalID: "ext-1"}, nil)
		uc.EXPECT().GetOrder(ctx, entity.UserID("user-1"), entity.OrderID("order-1")).
			Return(&entity.Order{
				ID:       "order-1",
				BuyerID:  "user-1",
				Status:   entity.OrderStatusPaid,
				Amount:   10000,
				Currency: "JPY",
				PaidTime: time.Unix(1_700_000_000, 0),
			}, nil)

		resp, err := h.GetOrder(ctx, connect.NewRequest(&ticketv1.GetOrderRequest{
			OrderId: &entityv1.OrderId{Value: "order-1"},
		}))
		require.NoError(t, err)
		assert.Equal(t, "order-1", resp.Msg.Order.Id.Value)
		assert.Equal(t, entityv1.OrderStatus_ORDER_STATUS_PAID, resp.Msg.Order.Status)
		assert.Equal(t, int64(10000), resp.Msg.Order.Amount)
	})

	t.Run("propagates NotFound (non-revealing) when the order is not the caller's", func(t *testing.T) {
		t.Parallel()
		ctx := ticketAuthedCtx("ext-1")
		h, uc, ur := newTicketHandler(t)
		ur.EXPECT().GetByExternalID(ctx, "ext-1").
			Return(&entity.User{ID: "user-1", ExternalID: "ext-1"}, nil)
		uc.EXPECT().GetOrder(ctx, entity.UserID("user-1"), entity.OrderID("order-x")).
			Return(nil, apperr.New(codes.NotFound, "order not found"))

		_, err := h.GetOrder(ctx, connect.NewRequest(&ticketv1.GetOrderRequest{
			OrderId: &entityv1.OrderId{Value: "order-x"},
		}))
		require.Error(t, err)
		// The handler propagates the usecase's apperr unchanged; the apperr→connect
		// code mapping is the server interceptor's responsibility (tested there).
		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})
}

func TestTicketHandler_GetMyTickets(t *testing.T) {
	t.Parallel()

	ctx := ticketAuthedCtx("ext-1")
	h, uc, ur := newTicketHandler(t)
	ur.EXPECT().GetByExternalID(ctx, "ext-1").
		Return(&entity.User{ID: "user-1", ExternalID: "ext-1"}, nil)
	uc.EXPECT().GetMyTickets(ctx, entity.UserID("user-1")).
		Return([]*entity.Ticket{
			{ID: "t-1", OrderID: "order-1", HolderID: "user-1", EventID: "event-1", ResaleWithoutConsentProhibited: true, Status: entity.TicketStatusIssued},
			{ID: "t-2", OrderID: "order-1", HolderID: "user-1", EventID: "event-1", ResaleWithoutConsentProhibited: true, Status: entity.TicketStatusIssued},
		}, nil)

	resp, err := h.GetMyTickets(ctx, connect.NewRequest(&ticketv1.GetMyTicketsRequest{}))
	require.NoError(t, err)
	require.Len(t, resp.Msg.Tickets, 2)
	assert.Equal(t, "t-1", resp.Msg.Tickets[0].Id.Value)
	assert.True(t, resp.Msg.Tickets[0].ResaleWithoutConsentProhibited)
	assert.Equal(t, entityv1.TicketStatus_TICKET_STATUS_ISSUED, resp.Msg.Tickets[1].Status)
}
