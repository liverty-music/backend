package rpc_test

import (
	"context"
	"testing"

	entitypb "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	rpcv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/push_notification/v1"
	"connectrpc.com/connect"
	handler "github.com/liverty-music/backend/internal/adapter/rpc"
	"github.com/liverty-music/backend/internal/entity"
	entitymocks "github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/infrastructure/auth"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func authedCtx(externalID string) context.Context {
	return auth.WithClaims(context.Background(), &auth.Claims{Sub: externalID})
}

func TestPushNotificationHandler_Create(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		ctx      context.Context
		req      *rpcv1.CreateRequest
		setup    func(uc *ucmocks.MockPushNotificationUseCase, ur *entitymocks.MockUserRepository)
		wantCode connect.Code
		wantErr  bool
	}{
		{
			name: "success",
			ctx:  authedCtx("ext-user-1"),
			req: &rpcv1.CreateRequest{
				Endpoint: &entitypb.PushEndpoint{Value: "https://push.example.com/sub"},
				Keys: &entitypb.PushKeys{
					P256Dh: "key",
					Auth:   "authSecret",
				},
			},
			setup: func(uc *ucmocks.MockPushNotificationUseCase, ur *entitymocks.MockUserRepository) {
				ur.EXPECT().GetByExternalID(mock.Anything, "ext-user-1").Return(&entity.User{
					ID:         "user-uuid-1",
					ExternalID: "ext-user-1",
				}, nil)
				uc.EXPECT().Create(mock.Anything, "user-uuid-1", "https://push.example.com/sub", "key", "authSecret").
					Return(&entity.PushSubscription{
						ID:       "sub-uuid-1",
						UserID:   "user-uuid-1",
						Endpoint: "https://push.example.com/sub",
						P256dh:   "key",
						Auth:     "authSecret",
					}, nil).Once()
			},
			wantErr: false,
		},
		{
			name:     "error - unauthenticated",
			ctx:      context.Background(),
			req:      &rpcv1.CreateRequest{Endpoint: &entitypb.PushEndpoint{Value: "https://push.example.com/sub"}, Keys: &entitypb.PushKeys{P256Dh: "k", Auth: "a"}},
			setup:    func(_ *ucmocks.MockPushNotificationUseCase, _ *entitymocks.MockUserRepository) {},
			wantCode: connect.CodeUnauthenticated,
			wantErr:  true,
		},
		{
			name: "error - missing endpoint",
			ctx:  authedCtx("ext-user-1"),
			req:  &rpcv1.CreateRequest{Keys: &entitypb.PushKeys{P256Dh: "k", Auth: "a"}},
			setup: func(_ *ucmocks.MockPushNotificationUseCase, ur *entitymocks.MockUserRepository) {
				ur.EXPECT().GetByExternalID(mock.Anything, "ext-user-1").Return(&entity.User{
					ID:         "user-uuid-1",
					ExternalID: "ext-user-1",
				}, nil)
			},
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

			resp, err := h.Create(tt.ctx, connect.NewRequest(tt.req))

			if tt.wantErr {
				assert.Error(t, err)
				if tt.wantCode != 0 {
					assert.Equal(t, tt.wantCode, connect.CodeOf(err))
				}
				return
			}
			assert.NoError(t, err)
			assert.NotNil(t, resp)
			assert.Equal(t, "sub-uuid-1", resp.Msg.GetSubscription().GetId().GetValue())
		})
	}
}

func TestPushNotificationHandler_Get(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		ctx      context.Context
		req      *rpcv1.GetRequest
		setup    func(uc *ucmocks.MockPushNotificationUseCase, ur *entitymocks.MockUserRepository)
		wantCode connect.Code
		wantErr  bool
	}{
		{
			name: "success",
			ctx:  authedCtx("ext-user-1"),
			req: &rpcv1.GetRequest{
				UserId:   &entitypb.UserId{Value: "user-uuid-1"},
				Endpoint: &entitypb.PushEndpoint{Value: "https://push.example.com/sub"},
			},
			setup: func(uc *ucmocks.MockPushNotificationUseCase, ur *entitymocks.MockUserRepository) {
				ur.EXPECT().GetByExternalID(mock.Anything, "ext-user-1").Return(&entity.User{
					ID:         "user-uuid-1",
					ExternalID: "ext-user-1",
				}, nil)
				uc.EXPECT().Get(mock.Anything, "user-uuid-1", "https://push.example.com/sub").
					Return(&entity.PushSubscription{
						ID:       "sub-uuid-1",
						UserID:   "user-uuid-1",
						Endpoint: "https://push.example.com/sub",
						P256dh:   "k",
						Auth:     "a",
					}, nil).Once()
			},
			wantErr: false,
		},
		{
			name: "error - user_id mismatch returns PermissionDenied",
			ctx:  authedCtx("ext-user-1"),
			req: &rpcv1.GetRequest{
				UserId:   &entitypb.UserId{Value: "user-uuid-other"},
				Endpoint: &entitypb.PushEndpoint{Value: "https://push.example.com/sub"},
			},
			setup: func(_ *ucmocks.MockPushNotificationUseCase, ur *entitymocks.MockUserRepository) {
				ur.EXPECT().GetByExternalID(mock.Anything, "ext-user-1").Return(&entity.User{
					ID:         "user-uuid-1",
					ExternalID: "ext-user-1",
				}, nil)
			},
			wantCode: connect.CodePermissionDenied,
			wantErr:  true,
		},
		{
			name: "error - empty user_id returns InvalidArgument",
			ctx:  authedCtx("ext-user-1"),
			req: &rpcv1.GetRequest{
				Endpoint: &entitypb.PushEndpoint{Value: "https://push.example.com/sub"},
			},
			setup: func(_ *ucmocks.MockPushNotificationUseCase, ur *entitymocks.MockUserRepository) {
				ur.EXPECT().GetByExternalID(mock.Anything, "ext-user-1").Return(&entity.User{
					ID:         "user-uuid-1",
					ExternalID: "ext-user-1",
				}, nil)
			},
			wantCode: connect.CodeInvalidArgument,
			wantErr:  true,
		},
		{
			name: "error - NotFound is propagated",
			ctx:  authedCtx("ext-user-1"),
			req: &rpcv1.GetRequest{
				UserId:   &entitypb.UserId{Value: "user-uuid-1"},
				Endpoint: &entitypb.PushEndpoint{Value: "https://push.example.com/missing"},
			},
			setup: func(uc *ucmocks.MockPushNotificationUseCase, ur *entitymocks.MockUserRepository) {
				ur.EXPECT().GetByExternalID(mock.Anything, "ext-user-1").Return(&entity.User{
					ID:         "user-uuid-1",
					ExternalID: "ext-user-1",
				}, nil)
				uc.EXPECT().Get(mock.Anything, "user-uuid-1", "https://push.example.com/missing").
					Return(nil, apperr.ErrNotFound).Once()
			},
			wantCode: connect.CodeNotFound,
			wantErr:  true,
		},
		{
			name:     "error - unauthenticated",
			ctx:      context.Background(),
			req:      &rpcv1.GetRequest{UserId: &entitypb.UserId{Value: "u"}, Endpoint: &entitypb.PushEndpoint{Value: "e"}},
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

			resp, err := h.Get(tt.ctx, connect.NewRequest(tt.req))

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

func TestPushNotificationHandler_Delete(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		ctx      context.Context
		req      *rpcv1.DeleteRequest
		setup    func(uc *ucmocks.MockPushNotificationUseCase, ur *entitymocks.MockUserRepository)
		wantCode connect.Code
		wantErr  bool
	}{
		{
			name: "success",
			ctx:  authedCtx("ext-user-1"),
			req: &rpcv1.DeleteRequest{
				UserId:   &entitypb.UserId{Value: "user-uuid-1"},
				Endpoint: &entitypb.PushEndpoint{Value: "https://push.example.com/sub"},
			},
			setup: func(uc *ucmocks.MockPushNotificationUseCase, ur *entitymocks.MockUserRepository) {
				ur.EXPECT().GetByExternalID(mock.Anything, "ext-user-1").Return(&entity.User{
					ID:         "user-uuid-1",
					ExternalID: "ext-user-1",
				}, nil)
				uc.EXPECT().Delete(mock.Anything, "user-uuid-1", "https://push.example.com/sub").
					Return(nil).Once()
			},
			wantErr: false,
		},
		{
			name: "error - user_id mismatch returns PermissionDenied",
			ctx:  authedCtx("ext-user-1"),
			req: &rpcv1.DeleteRequest{
				UserId:   &entitypb.UserId{Value: "user-uuid-other"},
				Endpoint: &entitypb.PushEndpoint{Value: "https://push.example.com/sub"},
			},
			setup: func(_ *ucmocks.MockPushNotificationUseCase, ur *entitymocks.MockUserRepository) {
				ur.EXPECT().GetByExternalID(mock.Anything, "ext-user-1").Return(&entity.User{
					ID:         "user-uuid-1",
					ExternalID: "ext-user-1",
				}, nil)
			},
			wantCode: connect.CodePermissionDenied,
			wantErr:  true,
		},
		{
			name:     "error - unauthenticated",
			ctx:      context.Background(),
			req:      &rpcv1.DeleteRequest{UserId: &entitypb.UserId{Value: "u"}, Endpoint: &entitypb.PushEndpoint{Value: "e"}},
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
