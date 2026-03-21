package rpc_test

import (
	"context"
	"testing"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	ticketjourneyv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/ticket_journey/v1"
	"connectrpc.com/connect"
	handler "github.com/liverty-music/backend/internal/adapter/rpc"
	"github.com/liverty-music/backend/internal/entity"
	entitymocks "github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/infrastructure/auth"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func ticketJourneyAuthedCtx(sub string) context.Context {
	return auth.WithClaims(context.Background(), &auth.Claims{Sub: sub})
}

func TestTicketJourneyHandler_SetStatus(t *testing.T) {
	t.Parallel()

	eventID := "event-uuid-1"

	tests := []struct {
		name     string
		ctx      context.Context
		req      *ticketjourneyv1.SetStatusRequest
		setup    func(uc *ucmocks.MockTicketJourneyUseCase, ur *entitymocks.MockUserRepository)
		wantCode connect.Code
		wantErr  bool
	}{
		{
			name: "success",
			ctx:  ticketJourneyAuthedCtx("ext-user-1"),
			req: &ticketjourneyv1.SetStatusRequest{
				EventId: &entityv1.EventId{Value: eventID},
				Status:  entityv1.TicketJourneyStatus_TICKET_JOURNEY_STATUS_TRACKING,
			},
			setup: func(uc *ucmocks.MockTicketJourneyUseCase, ur *entitymocks.MockUserRepository) {
				ur.EXPECT().GetByExternalID(mock.Anything, "ext-user-1").Return(&entity.User{
					ID:         "user-uuid-1",
					ExternalID: "ext-user-1",
				}, nil)
				uc.EXPECT().SetStatus(mock.Anything, "user-uuid-1", eventID, mock.Anything).Return(nil).Once()
			},
			wantErr: false,
		},
		{
			name: "error - unauthenticated",
			ctx:  context.Background(),
			req: &ticketjourneyv1.SetStatusRequest{
				EventId: &entityv1.EventId{Value: eventID},
				Status:  entityv1.TicketJourneyStatus_TICKET_JOURNEY_STATUS_TRACKING,
			},
			setup:    func(_ *ucmocks.MockTicketJourneyUseCase, _ *entitymocks.MockUserRepository) {},
			wantCode: connect.CodeUnauthenticated,
			wantErr:  true,
		},
		{
			name: "error - missing event_id",
			ctx:  ticketJourneyAuthedCtx("ext-user-1"),
			req: &ticketjourneyv1.SetStatusRequest{
				EventId: nil,
				Status:  entityv1.TicketJourneyStatus_TICKET_JOURNEY_STATUS_TRACKING,
			},
			setup:    func(_ *ucmocks.MockTicketJourneyUseCase, _ *entitymocks.MockUserRepository) {},
			wantCode: connect.CodeInvalidArgument,
			wantErr:  true,
		},
		{
			name: "error - user not found",
			ctx:  ticketJourneyAuthedCtx("ext-user-1"),
			req: &ticketjourneyv1.SetStatusRequest{
				EventId: &entityv1.EventId{Value: eventID},
				Status:  entityv1.TicketJourneyStatus_TICKET_JOURNEY_STATUS_TRACKING,
			},
			setup: func(_ *ucmocks.MockTicketJourneyUseCase, ur *entitymocks.MockUserRepository) {
				ur.EXPECT().GetByExternalID(mock.Anything, "ext-user-1").Return(nil, nil)
			},
			wantCode: connect.CodeNotFound,
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			logger, err := logging.New()
			require.NoError(t, err)

			uc := ucmocks.NewMockTicketJourneyUseCase(t)
			ur := entitymocks.NewMockUserRepository(t)
			tt.setup(uc, ur)
			h := handler.NewTicketJourneyHandler(uc, ur, logger)

			resp, err := h.SetStatus(tt.ctx, connect.NewRequest(tt.req))

			if tt.wantErr {
				assert.Error(t, err)
				if tt.wantCode != 0 {
					assert.Equal(t, tt.wantCode, connect.CodeOf(err))
				}
				return
			}
			assert.NoError(t, err)
			assert.NotNil(t, resp)
		})
	}
}

func TestTicketJourneyHandler_Delete(t *testing.T) {
	t.Parallel()

	eventID := "event-uuid-1"

	tests := []struct {
		name     string
		ctx      context.Context
		req      *ticketjourneyv1.DeleteRequest
		setup    func(uc *ucmocks.MockTicketJourneyUseCase, ur *entitymocks.MockUserRepository)
		wantCode connect.Code
		wantErr  bool
	}{
		{
			name: "success",
			ctx:  ticketJourneyAuthedCtx("ext-user-1"),
			req:  &ticketjourneyv1.DeleteRequest{EventId: &entityv1.EventId{Value: eventID}},
			setup: func(uc *ucmocks.MockTicketJourneyUseCase, ur *entitymocks.MockUserRepository) {
				ur.EXPECT().GetByExternalID(mock.Anything, "ext-user-1").Return(&entity.User{
					ID:         "user-uuid-1",
					ExternalID: "ext-user-1",
				}, nil)
				uc.EXPECT().Delete(mock.Anything, "user-uuid-1", eventID).Return(nil).Once()
			},
			wantErr: false,
		},
		{
			name:     "error - unauthenticated",
			ctx:      context.Background(),
			req:      &ticketjourneyv1.DeleteRequest{EventId: &entityv1.EventId{Value: eventID}},
			setup:    func(_ *ucmocks.MockTicketJourneyUseCase, _ *entitymocks.MockUserRepository) {},
			wantCode: connect.CodeUnauthenticated,
			wantErr:  true,
		},
		{
			name:     "error - missing event_id",
			ctx:      ticketJourneyAuthedCtx("ext-user-1"),
			req:      &ticketjourneyv1.DeleteRequest{EventId: nil},
			setup:    func(_ *ucmocks.MockTicketJourneyUseCase, _ *entitymocks.MockUserRepository) {},
			wantCode: connect.CodeInvalidArgument,
			wantErr:  true,
		},
		{
			name: "error - user not found",
			ctx:  ticketJourneyAuthedCtx("ext-user-1"),
			req:  &ticketjourneyv1.DeleteRequest{EventId: &entityv1.EventId{Value: eventID}},
			setup: func(_ *ucmocks.MockTicketJourneyUseCase, ur *entitymocks.MockUserRepository) {
				ur.EXPECT().GetByExternalID(mock.Anything, "ext-user-1").Return(nil, nil)
			},
			wantCode: connect.CodeNotFound,
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			logger, err := logging.New()
			require.NoError(t, err)

			uc := ucmocks.NewMockTicketJourneyUseCase(t)
			ur := entitymocks.NewMockUserRepository(t)
			tt.setup(uc, ur)
			h := handler.NewTicketJourneyHandler(uc, ur, logger)

			resp, err := h.Delete(tt.ctx, connect.NewRequest(tt.req))

			if tt.wantErr {
				assert.Error(t, err)
				if tt.wantCode != 0 {
					assert.Equal(t, tt.wantCode, connect.CodeOf(err))
				}
				return
			}
			assert.NoError(t, err)
			assert.NotNil(t, resp)
		})
	}
}

func TestTicketJourneyHandler_ListByUser(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		ctx      context.Context
		setup    func(uc *ucmocks.MockTicketJourneyUseCase, ur *entitymocks.MockUserRepository)
		wantCode connect.Code
		wantErr  bool
	}{
		{
			name: "success - returns empty list when no journeys exist",
			ctx:  ticketJourneyAuthedCtx("ext-user-1"),
			setup: func(uc *ucmocks.MockTicketJourneyUseCase, ur *entitymocks.MockUserRepository) {
				ur.EXPECT().GetByExternalID(mock.Anything, "ext-user-1").Return(&entity.User{
					ID:         "user-uuid-1",
					ExternalID: "ext-user-1",
				}, nil)
				uc.EXPECT().ListByUser(mock.Anything, "user-uuid-1").Return([]*entity.TicketJourney{}, nil).Once()
			},
			wantErr: false,
		},
		{
			name:     "error - unauthenticated",
			ctx:      context.Background(),
			setup:    func(_ *ucmocks.MockTicketJourneyUseCase, _ *entitymocks.MockUserRepository) {},
			wantCode: connect.CodeUnauthenticated,
			wantErr:  true,
		},
		{
			name: "error - user not found",
			ctx:  ticketJourneyAuthedCtx("ext-user-1"),
			setup: func(_ *ucmocks.MockTicketJourneyUseCase, ur *entitymocks.MockUserRepository) {
				ur.EXPECT().GetByExternalID(mock.Anything, "ext-user-1").Return(nil, nil)
			},
			wantCode: connect.CodeNotFound,
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			logger, err := logging.New()
			require.NoError(t, err)

			uc := ucmocks.NewMockTicketJourneyUseCase(t)
			ur := entitymocks.NewMockUserRepository(t)
			tt.setup(uc, ur)
			h := handler.NewTicketJourneyHandler(uc, ur, logger)

			resp, err := h.ListByUser(tt.ctx, connect.NewRequest(&ticketjourneyv1.ListByUserRequest{}))

			if tt.wantErr {
				assert.Error(t, err)
				if tt.wantCode != 0 {
					assert.Equal(t, tt.wantCode, connect.CodeOf(err))
				}
				return
			}
			assert.NoError(t, err)
			assert.NotNil(t, resp)
		})
	}
}
