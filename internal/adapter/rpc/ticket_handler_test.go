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
	"github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/infrastructure/auth"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func ticketAuthedCtx(sub string) context.Context {
	return auth.WithClaims(context.Background(), &auth.Claims{Sub: sub})
}

func TestTicketHandler_MintTicket(t *testing.T) {
	logger, _ := logging.New()

	tests := []struct {
		name     string
		ctx      context.Context
		req      *ticketv1.MintTicketRequest
		setup    func(uc *ucmocks.MockTicketUseCase, ur *mocks.MockUserRepository)
		wantCode connect.Code
		wantErr  bool
	}{
		{
			name: "success",
			ctx:  ticketAuthedCtx("ext-user-1"),
			req: &ticketv1.MintTicketRequest{
				EventId: &entityv1.EventId{Value: "event-123"},
			},
			setup: func(uc *ucmocks.MockTicketUseCase, ur *mocks.MockUserRepository) {
				ur.EXPECT().GetByExternalID(mock.Anything, "ext-user-1").Return(&entity.User{
					ID:          "user-uuid-1",
					ExternalID:  "ext-user-1",
					SafeAddress: "0x1234567890123456789012345678901234567890",
				}, nil)
				uc.EXPECT().MintTicket(mock.Anything, mock.Anything).Return(&entity.Ticket{
					ID:       "ticket-1",
					EventID:  "event-123",
					UserID:   "user-uuid-1",
					TokenID:  42,
					TxHash:   "0xdeadbeef",
					MintTime: time.Now(),
				}, nil)
			},
			wantErr: false,
		},
		{
			name:     "unauthenticated",
			ctx:      context.Background(),
			req:      &ticketv1.MintTicketRequest{EventId: &entityv1.EventId{Value: "event-123"}},
			setup:    func(_ *ucmocks.MockTicketUseCase, _ *mocks.MockUserRepository) {},
			wantCode: connect.CodeUnauthenticated,
			wantErr:  true,
		},
		{
			name:     "nil request",
			ctx:      ticketAuthedCtx("ext-user-1"),
			req:      nil,
			setup:    func(_ *ucmocks.MockTicketUseCase, _ *mocks.MockUserRepository) {},
			wantCode: connect.CodeInvalidArgument,
			wantErr:  true,
		},
		{
			name:     "missing event_id",
			ctx:      ticketAuthedCtx("ext-user-1"),
			req:      &ticketv1.MintTicketRequest{},
			setup:    func(_ *ucmocks.MockTicketUseCase, _ *mocks.MockUserRepository) {},
			wantCode: connect.CodeInvalidArgument,
			wantErr:  true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ticketUC := ucmocks.NewMockTicketUseCase(t)
			userRepo := mocks.NewMockUserRepository(t)
			tc.setup(ticketUC, userRepo)

			h := handler.NewTicketHandler(ticketUC, userRepo, logger)

			var req *connect.Request[ticketv1.MintTicketRequest]
			if tc.req != nil {
				req = connect.NewRequest(tc.req)
			}

			resp, err := h.MintTicket(tc.ctx, req)
			if tc.wantErr {
				require.Error(t, err)
				assert.Equal(t, tc.wantCode, connect.CodeOf(err))
				return
			}

			require.NoError(t, err)
			assert.NotNil(t, resp)
			assert.NotNil(t, resp.Msg.Ticket)
			assert.Equal(t, "ticket-1", resp.Msg.Ticket.Id.Value)
		})
	}
}

func TestTicketHandler_GetTicket(t *testing.T) {
	logger, _ := logging.New()

	tests := []struct {
		name     string
		req      *ticketv1.GetTicketRequest
		setup    func(uc *ucmocks.MockTicketUseCase)
		wantCode connect.Code
		wantErr  bool
	}{
		{
			name: "success",
			req:  &ticketv1.GetTicketRequest{TicketId: &entityv1.TicketId{Value: "ticket-1"}},
			setup: func(uc *ucmocks.MockTicketUseCase) {
				uc.EXPECT().GetTicket(mock.Anything, "ticket-1").Return(&entity.Ticket{
					ID:       "ticket-1",
					EventID:  "event-1",
					UserID:   "user-1",
					TokenID:  42,
					TxHash:   "0xabc",
					MintTime: time.Now(),
				}, nil)
			},
			wantErr: false,
		},
		{
			name:     "nil request",
			req:      nil,
			setup:    func(_ *ucmocks.MockTicketUseCase) {},
			wantCode: connect.CodeInvalidArgument,
			wantErr:  true,
		},
		{
			name:     "missing ticket_id",
			req:      &ticketv1.GetTicketRequest{},
			setup:    func(_ *ucmocks.MockTicketUseCase) {},
			wantCode: connect.CodeInvalidArgument,
			wantErr:  true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ticketUC := ucmocks.NewMockTicketUseCase(t)
			userRepo := mocks.NewMockUserRepository(t)
			tc.setup(ticketUC)

			h := handler.NewTicketHandler(ticketUC, userRepo, logger)

			var req *connect.Request[ticketv1.GetTicketRequest]
			if tc.req != nil {
				req = connect.NewRequest(tc.req)
			}

			resp, err := h.GetTicket(context.Background(), req)
			if tc.wantErr {
				require.Error(t, err)
				assert.Equal(t, tc.wantCode, connect.CodeOf(err))
				return
			}

			require.NoError(t, err)
			assert.NotNil(t, resp)
			assert.Equal(t, "ticket-1", resp.Msg.Ticket.Id.Value)
		})
	}
}

func TestTicketHandler_ListTickets(t *testing.T) {
	logger, _ := logging.New()

	tests := []struct {
		name     string
		ctx      context.Context
		req      *ticketv1.ListTicketsRequest
		setup    func(uc *ucmocks.MockTicketUseCase, ur *mocks.MockUserRepository)
		wantCode connect.Code
		wantErr  bool
		wantLen  int
	}{
		{
			name: "success with tickets",
			ctx:  ticketAuthedCtx("ext-user-1"),
			req:  &ticketv1.ListTicketsRequest{},
			setup: func(uc *ucmocks.MockTicketUseCase, ur *mocks.MockUserRepository) {
				ur.EXPECT().GetByExternalID(mock.Anything, "ext-user-1").Return(&entity.User{
					ID:         "user-uuid-1",
					ExternalID: "ext-user-1",
				}, nil)
				uc.EXPECT().ListTicketsForUser(mock.Anything, "user-uuid-1").Return([]*entity.Ticket{
					{ID: "t1", EventID: "e1", UserID: "user-uuid-1", TokenID: 1, TxHash: "0x1", MintTime: time.Now()},
					{ID: "t2", EventID: "e2", UserID: "user-uuid-1", TokenID: 2, TxHash: "0x2", MintTime: time.Now()},
				}, nil)
			},
			wantErr: false,
			wantLen: 2,
		},
		{
			name:     "unauthenticated",
			ctx:      context.Background(),
			req:      &ticketv1.ListTicketsRequest{},
			setup:    func(_ *ucmocks.MockTicketUseCase, _ *mocks.MockUserRepository) {},
			wantCode: connect.CodeUnauthenticated,
			wantErr:  true,
		},
		{
			name:     "nil request",
			ctx:      ticketAuthedCtx("ext-user-1"),
			req:      nil,
			setup:    func(_ *ucmocks.MockTicketUseCase, _ *mocks.MockUserRepository) {},
			wantCode: connect.CodeInvalidArgument,
			wantErr:  true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ticketUC := ucmocks.NewMockTicketUseCase(t)
			userRepo := mocks.NewMockUserRepository(t)
			tc.setup(ticketUC, userRepo)

			h := handler.NewTicketHandler(ticketUC, userRepo, logger)

			var req *connect.Request[ticketv1.ListTicketsRequest]
			if tc.req != nil {
				req = connect.NewRequest(tc.req)
			}

			resp, err := h.ListTickets(tc.ctx, req)
			if tc.wantErr {
				require.Error(t, err)
				assert.Equal(t, tc.wantCode, connect.CodeOf(err))
				return
			}

			require.NoError(t, err)
			assert.NotNil(t, resp)
			assert.Len(t, resp.Msg.Tickets, tc.wantLen)
		})
	}
}
