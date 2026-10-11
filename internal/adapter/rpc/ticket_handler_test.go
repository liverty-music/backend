package rpc_test

import (
	"context"
	"testing"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	ticketv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/ticket/v1"
	"connectrpc.com/connect"
	handler "github.com/liverty-music/backend/internal/adapter/rpc"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/auth"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// ticketAuthedCtx returns a context signed in as sub.
func ticketAuthedCtx(sub string) context.Context {
	return auth.WithClaims(context.Background(), &auth.Claims{Sub: sub})
}

func newTicketHandler(t *testing.T) (*handler.TicketHandler, *ucmocks.MockTicketUseCase, *ucmocks.MockUserUseCase) {
	t.Helper()
	uc := ucmocks.NewMockTicketUseCase(t)
	users := ucmocks.NewMockUserUseCase(t)
	logger, err := logging.New()
	require.NoError(t, err)
	return handler.NewTicketHandler(uc, users, logger), uc, users
}

func TestTicketHandler_List(t *testing.T) {
	t.Parallel()

	t.Run("fan lists their tickets", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/fan/api/rpc/ticket "Fan lists their tickets"
		h, uc, users := newTicketHandler(t)
		expectFan(users)
		uc.EXPECT().List(mock.Anything, entity.UserID(tkFanUserID)).Return([]*entity.Ticket{
			{ID: "t-1", OrderID: tkOrderID, UserID: tkFanUserID, EventID: testEventID, IdentityVerificationID: "kyc-1", ResaleWithoutConsentProhibited: true, Status: entity.TicketStatusIssued},
			{ID: "t-2", OrderID: tkOrderID, UserID: tkFanUserID, EventID: testEventID, ResaleWithoutConsentProhibited: true, Status: entity.TicketStatusIssued},
		}, nil)

		resp, err := h.List(fanCtx(tkFanSub), connect.NewRequest(&ticketv1.ListRequest{}))

		require.NoError(t, err)
		require.Len(t, resp.Msg.GetTickets(), 2)
		assert.Equal(t, tkFanUserID, resp.Msg.GetTickets()[0].GetUserId().GetValue())
		assert.Equal(t, "kyc-1", resp.Msg.GetTickets()[0].GetIdentityVerificationId().GetValue())
		assert.Nil(t, resp.Msg.GetTickets()[1].GetIdentityVerificationId(), "no Kyc binding without a verification requirement")
		assert.Equal(t, entityv1.TicketStatus_TICKET_STATUS_ISSUED, resp.Msg.GetTickets()[1].GetStatus())
	})

	t.Run("not signed in lists tickets", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/fan/api/rpc/ticket "Not signed in lists tickets"
		h, _, _ := newTicketHandler(t)

		_, err := h.List(context.Background(), connect.NewRequest(&ticketv1.ListRequest{}))

		assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	})

	t.Run("caller without an account", func(t *testing.T) {
		t.Parallel()
		h, _, users := newTicketHandler(t)
		expectNoAccount(users, "ext-new")

		_, err := h.List(fanCtx("ext-new"), connect.NewRequest(&ticketv1.ListRequest{}))

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})
}
