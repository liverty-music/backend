package rpc_test

import (
	"context"
	"testing"

	rpcv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/push_notification/v1"
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

func authedCtx(userID string) context.Context {
	return auth.WithClaims(context.Background(), &auth.Claims{Sub: userID})
}

func TestPushNotificationHandler_Subscribe(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		ctx      context.Context
		req      *rpcv1.SubscribeRequest
		setup    func(uc *ucmocks.MockPushNotificationUseCase, ur *entitymocks.MockUserRepository)
		wantCode connect.Code
		wantErr  bool
	}{
		{
			name: "success",
			ctx:  authedCtx("ext-user-1"),
			req: &rpcv1.SubscribeRequest{
				Endpoint: "https://push.example.com/sub",
				P256Dh:   "key",
				Auth:     "authSecret",
			},
			setup: func(uc *ucmocks.MockPushNotificationUseCase, ur *entitymocks.MockUserRepository) {
				ur.EXPECT().GetByExternalID(mock.Anything, "ext-user-1").Return(&entity.User{
					ID:         "user-uuid-1",
					ExternalID: "ext-user-1",
				}, nil)
				uc.EXPECT().Subscribe(mock.Anything, "user-uuid-1", "https://push.example.com/sub", "key", "authSecret").
					Return(nil).Once()
			},
			wantErr: false,
		},
		{
			name:     "error - unauthenticated",
			ctx:      context.Background(),
			req:      &rpcv1.SubscribeRequest{Endpoint: "https://push.example.com/sub", P256Dh: "k", Auth: "a"},
			setup:    func(_ *ucmocks.MockPushNotificationUseCase, _ *entitymocks.MockUserRepository) {},
			wantCode: connect.CodeUnauthenticated,
			wantErr:  true,
		},
		{
			name:     "error - missing endpoint",
			ctx:      authedCtx("ext-user-1"),
			req:      &rpcv1.SubscribeRequest{P256Dh: "k", Auth: "a"},
			setup:    func(_ *ucmocks.MockPushNotificationUseCase, _ *entitymocks.MockUserRepository) {},
			wantCode: connect.CodeInvalidArgument,
			wantErr:  true,
		},
		{
			name:     "error - missing p256dh",
			ctx:      authedCtx("ext-user-1"),
			req:      &rpcv1.SubscribeRequest{Endpoint: "https://push.example.com/sub", Auth: "a"},
			setup:    func(_ *ucmocks.MockPushNotificationUseCase, _ *entitymocks.MockUserRepository) {},
			wantCode: connect.CodeInvalidArgument,
			wantErr:  true,
		},
		{
			name:     "error - missing auth",
			ctx:      authedCtx("ext-user-1"),
			req:      &rpcv1.SubscribeRequest{Endpoint: "https://push.example.com/sub", P256Dh: "k"},
			setup:    func(_ *ucmocks.MockPushNotificationUseCase, _ *entitymocks.MockUserRepository) {},
			wantCode: connect.CodeInvalidArgument,
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			logger, err := logging.New()
			require.NoError(t, err)

			uc := ucmocks.NewMockPushNotificationUseCase(t)
			ur := entitymocks.NewMockUserRepository(t)
			tt.setup(uc, ur)
			h := handler.NewPushNotificationHandler(uc, ur, logger)

			resp, err := h.Subscribe(tt.ctx, connect.NewRequest(tt.req))

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

func TestPushNotificationHandler_Unsubscribe(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		ctx      context.Context
		setup    func(uc *ucmocks.MockPushNotificationUseCase, ur *entitymocks.MockUserRepository)
		wantCode connect.Code
		wantErr  bool
	}{
		{
			name: "success",
			ctx:  authedCtx("ext-user-1"),
			setup: func(uc *ucmocks.MockPushNotificationUseCase, ur *entitymocks.MockUserRepository) {
				ur.EXPECT().GetByExternalID(mock.Anything, "ext-user-1").Return(&entity.User{
					ID:         "user-uuid-1",
					ExternalID: "ext-user-1",
				}, nil)
				uc.EXPECT().Unsubscribe(mock.Anything, "user-uuid-1").Return(nil).Once()
			},
			wantErr: false,
		},
		{
			name:     "error - unauthenticated",
			ctx:      context.Background(),
			setup:    func(_ *ucmocks.MockPushNotificationUseCase, _ *entitymocks.MockUserRepository) {},
			wantCode: connect.CodeUnauthenticated,
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			logger, err := logging.New()
			require.NoError(t, err)

			uc := ucmocks.NewMockPushNotificationUseCase(t)
			ur := entitymocks.NewMockUserRepository(t)
			tt.setup(uc, ur)
			h := handler.NewPushNotificationHandler(uc, ur, logger)

			resp, err := h.Unsubscribe(tt.ctx, connect.NewRequest(&rpcv1.UnsubscribeRequest{}))

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
