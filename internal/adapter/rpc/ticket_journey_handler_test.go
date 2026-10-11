package rpc_test

import (
	"context"
	"net/http"
	"testing"

	ticketjourneyv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/ticket_journey/v1/ticket_journeyv1connect"
	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	ticketjourneyv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/ticket_journey/v1"
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
			// @spec components/adapter/fan/api/rpc/ticket-journey "The journey belongs to the caller"
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
				uc.EXPECT().SetStatus(mock.Anything, "user-uuid-1", eventID, entity.TicketJourneyStatusTracking).Return(nil).Once()
			},
			wantErr: false,
		},
		{
			// @spec components/adapter/fan/api/rpc/ticket-journey "Not signed in"
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
			// @spec components/adapter/fan/api/rpc/ticket-journey "Caller has no account"
			name: "error - user not found",
			ctx:  ticketJourneyAuthedCtx("ext-user-1"),
			req: &ticketjourneyv1.SetStatusRequest{
				EventId: &entityv1.EventId{Value: eventID},
				Status:  entityv1.TicketJourneyStatus_TICKET_JOURNEY_STATUS_TRACKING,
			},
			setup: func(_ *ucmocks.MockTicketJourneyUseCase, ur *entitymocks.MockUserRepository) {
				ur.EXPECT().GetByExternalID(mock.Anything, "ext-user-1").Return(nil, connect.NewError(connect.CodeNotFound, nil))
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
			name: "error - user not found",
			ctx:  ticketJourneyAuthedCtx("ext-user-1"),
			req:  &ticketjourneyv1.DeleteRequest{EventId: &entityv1.EventId{Value: eventID}},
			setup: func(_ *ucmocks.MockTicketJourneyUseCase, ur *entitymocks.MockUserRepository) {
				ur.EXPECT().GetByExternalID(mock.Anything, "ext-user-1").Return(nil, connect.NewError(connect.CodeNotFound, nil))
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

func TestTicketJourneyHandler_List(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		ctx      context.Context
		setup    func(uc *ucmocks.MockTicketJourneyUseCase, ur *entitymocks.MockUserRepository)
		wantCode connect.Code
		wantErr  bool
	}{
		{
			// @spec components/adapter/fan/api/rpc/ticket-journey "The caller has no journeys"
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
				ur.EXPECT().GetByExternalID(mock.Anything, "ext-user-1").Return(nil, connect.NewError(connect.CodeNotFound, nil))
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

			resp, err := h.List(tt.ctx, connect.NewRequest(&ticketjourneyv1.ListRequest{}))

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

func TestTicketJourneyHandler_Boundary(t *testing.T) {
	t.Parallel()

	const eventID = "019a0000-0000-7000-8000-0000000000e1"

	newClient := func(t *testing.T, uc *ucmocks.MockTicketJourneyUseCase, ur *entitymocks.MockUserRepository) ticketjourneyv1connect.TicketJourneyServiceClient {
		t.Helper()
		logger, err := logging.New()
		require.NoError(t, err)
		h := handler.NewTicketJourneyHandler(uc, ur, logger)
		srv := newFanBoundaryServer(t, func(opts ...connect.HandlerOption) (string, http.Handler) {
			return ticketjourneyv1connect.NewTicketJourneyServiceHandler(h, opts...)
		})
		return ticketjourneyv1connect.NewTicketJourneyServiceClient(srv.Client(), srv.URL)
	}

	t.Run("SetStatus with a missing or malformed event id", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/fan/api/rpc/ticket-journey "Missing or malformed event id"
		// The mocks fail the test if anything is recorded.
		client := newClient(t, ucmocks.NewMockTicketJourneyUseCase(t), entitymocks.NewMockUserRepository(t))
		for _, id := range []*entityv1.EventId{nil, {Value: "not-a-uuid"}} {
			_, err := client.SetStatus(context.Background(), withFanToken(connect.NewRequest(&ticketjourneyv1.SetStatusRequest{
				EventId: id,
				Status:  entityv1.TicketJourneyStatus_TICKET_JOURNEY_STATUS_TRACKING,
			})))
			assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
		}
	})

	t.Run("SetStatus with a missing or unknown status", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/fan/api/rpc/ticket-journey "Missing or unknown status"
		client := newClient(t, ucmocks.NewMockTicketJourneyUseCase(t), entitymocks.NewMockUserRepository(t))
		for _, status := range []entityv1.TicketJourneyStatus{entityv1.TicketJourneyStatus_TICKET_JOURNEY_STATUS_UNSPECIFIED, 99} {
			_, err := client.SetStatus(context.Background(), withFanToken(connect.NewRequest(&ticketjourneyv1.SetStatusRequest{
				EventId: &entityv1.EventId{Value: eventID},
				Status:  status,
			})))
			assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err), "status %d", status)
		}
	})

	t.Run("SetStatus for an event that does not exist", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/fan/api/rpc/ticket-journey "Event does not exist"
		uc := ucmocks.NewMockTicketJourneyUseCase(t)
		ur := entitymocks.NewMockUserRepository(t)
		ur.EXPECT().GetByExternalID(mock.Anything, testCallerExtID).Return(&entity.User{ID: testCallerUserID}, nil).Once()
		uc.EXPECT().SetStatus(mock.Anything, testCallerUserID, eventID, entity.TicketJourneyStatusTracking).
			Return(apperr.New(codes.FailedPrecondition, "event does not exist")).Once()
		logger, err := logging.New()
		require.NoError(t, err)

		_, err = handler.NewTicketJourneyHandler(uc, ur, logger).SetStatus(ticketJourneyAuthedCtx(testCallerExtID), connect.NewRequest(&ticketjourneyv1.SetStatusRequest{
			EventId: &entityv1.EventId{Value: eventID},
			Status:  entityv1.TicketJourneyStatus_TICKET_JOURNEY_STATUS_TRACKING,
		}))

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("Delete with a missing or malformed event id", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/fan/api/rpc/ticket-journey "Missing or malformed event id"
		client := newClient(t, ucmocks.NewMockTicketJourneyUseCase(t), entitymocks.NewMockUserRepository(t))
		for _, id := range []*entityv1.EventId{nil, {Value: "not-a-uuid"}} {
			_, err := client.Delete(context.Background(), withFanToken(connect.NewRequest(&ticketjourneyv1.DeleteRequest{EventId: id})))
			assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
		}
	})
}
