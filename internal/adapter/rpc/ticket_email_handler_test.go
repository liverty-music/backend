package rpc_test

import (
	"context"
	"testing"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	ticketemailv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/ticket_email/v1"
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

func ticketEmailAuthedCtx(sub string) context.Context {
	return auth.WithClaims(context.Background(), &auth.Claims{Sub: sub})
}

func TestTicketEmailHandler_CreateTicketEmail(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		ctx      context.Context
		req      *ticketemailv1.CreateTicketEmailRequest
		setup    func(uc *ucmocks.MockTicketEmailUseCase, ur *entitymocks.MockUserRepository)
		wantCode connect.Code
		wantLen  int
		wantErr  error
	}{
		{
			name: "return 2 ticket emails when request contains 2 event IDs",
			ctx:  ticketEmailAuthedCtx("user-sub-1"),
			req: &ticketemailv1.CreateTicketEmailRequest{
				RawBody:   "lottery info body",
				EmailType: entityv1.TicketEmailType_TICKET_EMAIL_TYPE_LOTTERY_INFO,
				EventIds: []*entityv1.EventId{
					{Value: "event-1"},
					{Value: "event-2"},
				},
			},
			setup: func(uc *ucmocks.MockTicketEmailUseCase, ur *entitymocks.MockUserRepository) {
				ur.EXPECT().GetByExternalID(mock.Anything, "user-sub-1").Return(&entity.User{
					ID:         "user-uuid-1",
					ExternalID: "user-sub-1",
				}, nil)
				uc.EXPECT().Create(
					mock.Anything,
					"user-uuid-1",
					[]string{"event-1", "event-2"},
					entity.TicketEmailTypeLotteryInfo,
					"lottery info body",
				).Return([]*entity.TicketEmail{
					{ID: "te-1", UserID: "user-uuid-1", EventID: "event-1", EmailType: entity.TicketEmailTypeLotteryInfo},
					{ID: "te-2", UserID: "user-uuid-1", EventID: "event-2", EmailType: entity.TicketEmailTypeLotteryInfo},
				}, nil)
			},
			wantLen: 2,
			wantErr: nil,
		},
		{
			name: "return unauthenticated error when no user ID in context",
			ctx:  context.Background(),
			req: &ticketemailv1.CreateTicketEmailRequest{
				RawBody:   "body",
				EmailType: entityv1.TicketEmailType_TICKET_EMAIL_TYPE_LOTTERY_INFO,
				EventIds:  []*entityv1.EventId{{Value: "event-1"}},
			},
			setup:    func(_ *ucmocks.MockTicketEmailUseCase, _ *entitymocks.MockUserRepository) {},
			wantCode: connect.CodeUnauthenticated,
			wantErr:  assert.AnError,
		},
		{
			name: "return not found error when user does not exist",
			ctx:  ticketEmailAuthedCtx("user-sub-1"),
			req: &ticketemailv1.CreateTicketEmailRequest{
				RawBody:   "body",
				EmailType: entityv1.TicketEmailType_TICKET_EMAIL_TYPE_LOTTERY_INFO,
				EventIds:  []*entityv1.EventId{{Value: "event-1"}},
			},
			setup: func(_ *ucmocks.MockTicketEmailUseCase, ur *entitymocks.MockUserRepository) {
				ur.EXPECT().GetByExternalID(mock.Anything, "user-sub-1").Return(nil, connect.NewError(connect.CodeNotFound, nil))
			},
			wantCode: connect.CodeNotFound,
			wantErr:  assert.AnError,
		},
		{
			name: "return error when usecase returns error",
			ctx:  ticketEmailAuthedCtx("user-sub-1"),
			req: &ticketemailv1.CreateTicketEmailRequest{
				RawBody:   "body",
				EmailType: entityv1.TicketEmailType_TICKET_EMAIL_TYPE_LOTTERY_INFO,
				EventIds:  []*entityv1.EventId{{Value: "event-1"}},
			},
			setup: func(uc *ucmocks.MockTicketEmailUseCase, ur *entitymocks.MockUserRepository) {
				ur.EXPECT().GetByExternalID(mock.Anything, "user-sub-1").Return(&entity.User{
					ID:         "user-uuid-1",
					ExternalID: "user-sub-1",
				}, nil)
				uc.EXPECT().Create(
					mock.Anything,
					"user-uuid-1",
					[]string{"event-1"},
					entity.TicketEmailTypeLotteryInfo,
					"body",
				).Return(nil, assert.AnError)
			},
			wantErr: assert.AnError,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			logger, err := logging.New()
			require.NoError(t, err)

			ticketEmailUC := ucmocks.NewMockTicketEmailUseCase(t)
			ur := entitymocks.NewMockUserRepository(t)
			tc.setup(ticketEmailUC, ur)

			h := handler.NewTicketEmailHandler(ticketEmailUC, ur, logger)
			req := connect.NewRequest(tc.req)

			resp, err := h.CreateTicketEmail(tc.ctx, req)

			if tc.wantErr != nil {
				require.Error(t, err)
				if tc.wantCode != 0 {
					assert.Equal(t, tc.wantCode, connect.CodeOf(err))
				}
				return
			}

			require.NoError(t, err)
			assert.NotNil(t, resp)
			assert.Len(t, resp.Msg.TicketEmails, tc.wantLen)
		})
	}
}

func TestTicketEmailHandler_UpdateTicketEmail(t *testing.T) {
	t.Parallel()

	appURL := "https://example.com/apply"
	journeyStatus := entityv1.TicketJourneyStatus_TICKET_JOURNEY_STATUS_TRACKING

	tests := []struct {
		name     string
		ctx      context.Context
		req      *ticketemailv1.UpdateTicketEmailRequest
		setup    func(uc *ucmocks.MockTicketEmailUseCase, ur *entitymocks.MockUserRepository)
		wantCode connect.Code
		wantErr  error
	}{
		{
			name: "return updated ticket email when request contains application_url and journey_status corrections",
			ctx:  ticketEmailAuthedCtx("user-sub-1"),
			req: &ticketemailv1.UpdateTicketEmailRequest{
				TicketEmailId:  &entityv1.TicketEmailId{Value: "te-123"},
				ApplicationUrl: &appURL,
				JourneyStatus:  &journeyStatus,
			},
			setup: func(uc *ucmocks.MockTicketEmailUseCase, ur *entitymocks.MockUserRepository) {
				ur.EXPECT().GetByExternalID(mock.Anything, "user-sub-1").Return(&entity.User{
					ID:         "user-uuid-1",
					ExternalID: "user-sub-1",
				}, nil)
				uc.EXPECT().Update(
					mock.Anything,
					"user-uuid-1",
					"te-123",
					mock.AnythingOfType("*entity.UpdateTicketEmail"),
				).Return(&entity.TicketEmail{
					ID:             "te-123",
					UserID:         "user-uuid-1",
					EventID:        "event-1",
					ApplicationURL: appURL,
				}, nil)
			},
			wantErr: nil,
		},
		{
			name: "return unauthenticated error when no user ID in context",
			ctx:  context.Background(),
			req: &ticketemailv1.UpdateTicketEmailRequest{
				TicketEmailId: &entityv1.TicketEmailId{Value: "te-123"},
			},
			setup:    func(_ *ucmocks.MockTicketEmailUseCase, _ *entitymocks.MockUserRepository) {},
			wantCode: connect.CodeUnauthenticated,
			wantErr:  assert.AnError,
		},
		{
			name: "return not found error when user does not exist",
			ctx:  ticketEmailAuthedCtx("user-sub-1"),
			req: &ticketemailv1.UpdateTicketEmailRequest{
				TicketEmailId: &entityv1.TicketEmailId{Value: "te-123"},
			},
			setup: func(_ *ucmocks.MockTicketEmailUseCase, ur *entitymocks.MockUserRepository) {
				ur.EXPECT().GetByExternalID(mock.Anything, "user-sub-1").Return(nil, connect.NewError(connect.CodeNotFound, nil))
			},
			wantCode: connect.CodeNotFound,
			wantErr:  assert.AnError,
		},
		{
			name: "return error when usecase returns error",
			ctx:  ticketEmailAuthedCtx("user-sub-1"),
			req: &ticketemailv1.UpdateTicketEmailRequest{
				TicketEmailId: &entityv1.TicketEmailId{Value: "te-123"},
			},
			setup: func(uc *ucmocks.MockTicketEmailUseCase, ur *entitymocks.MockUserRepository) {
				ur.EXPECT().GetByExternalID(mock.Anything, "user-sub-1").Return(&entity.User{
					ID:         "user-uuid-1",
					ExternalID: "user-sub-1",
				}, nil)
				uc.EXPECT().Update(
					mock.Anything,
					"user-uuid-1",
					"te-123",
					mock.AnythingOfType("*entity.UpdateTicketEmail"),
				).Return(nil, assert.AnError)
			},
			wantErr: assert.AnError,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			logger, err := logging.New()
			require.NoError(t, err)

			ticketEmailUC := ucmocks.NewMockTicketEmailUseCase(t)
			ur := entitymocks.NewMockUserRepository(t)
			tc.setup(ticketEmailUC, ur)

			h := handler.NewTicketEmailHandler(ticketEmailUC, ur, logger)
			req := connect.NewRequest(tc.req)

			resp, err := h.UpdateTicketEmail(tc.ctx, req)

			if tc.wantErr != nil {
				require.Error(t, err)
				if tc.wantCode != 0 {
					assert.Equal(t, tc.wantCode, connect.CodeOf(err))
				}
				return
			}

			require.NoError(t, err)
			assert.NotNil(t, resp)
			assert.NotNil(t, resp.Msg.TicketEmail)
			assert.Equal(t, "te-123", resp.Msg.TicketEmail.Id.Value)
		})
	}
}
