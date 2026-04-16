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
	"github.com/liverty-music/backend/internal/usecase"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/liverty-music/backend/pkg/config"
	"github.com/pannpers/go-apperr/apperr"
	apperrcodes "github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func authedCtx(externalID string) context.Context {
	return auth.WithClaims(context.Background(), &auth.Claims{Sub: externalID})
}

// devConfig returns a BaseConfig representing a non-production environment
// so the debug RPC is enabled under test.
func devConfig() config.BaseConfig {
	return config.BaseConfig{Environment: "development"}
}

// prodConfig returns a BaseConfig representing production so the debug RPC
// is gated at the handler.
func prodConfig() config.BaseConfig {
	return config.BaseConfig{Environment: "production"}
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
			h := handler.NewPushNotificationHandler(uc, ur, devConfig(), logger)

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
			h := handler.NewPushNotificationHandler(uc, ur, devConfig(), logger)

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

func TestPushNotificationHandler_NotifyNewConcerts(t *testing.T) {
	t.Parallel()

	validReq := func() *rpcv1.NotifyNewConcertsRequest {
		return &rpcv1.NotifyNewConcertsRequest{
			ArtistId:   &entitypb.ArtistId{Value: "artist-1"},
			ConcertIds: []*entitypb.EventId{{Value: "concert-1"}, {Value: "concert-2"}},
		}
	}

	tests := []struct {
		name     string
		ctx      context.Context
		cfg      config.BaseConfig
		req      *rpcv1.NotifyNewConcertsRequest
		setup    func(uc *ucmocks.MockPushNotificationUseCase)
		wantCode connect.Code
		wantErr  bool
	}{
		{
			name: "success in non-production delegates to use case",
			ctx:  authedCtx("ext-user-1"),
			cfg:  devConfig(),
			req:  validReq(),
			setup: func(uc *ucmocks.MockPushNotificationUseCase) {
				uc.EXPECT().NotifyNewConcerts(mock.Anything, usecase.ConcertCreatedData{
					ArtistID:   "artist-1",
					ConcertIDs: []string{"concert-1", "concert-2"},
				}).Return(nil).Once()
			},
			wantErr: false,
		},
		{
			name:     "error - PermissionDenied in production",
			ctx:      authedCtx("ext-user-1"),
			cfg:      prodConfig(),
			req:      validReq(),
			setup:    func(_ *ucmocks.MockPushNotificationUseCase) {},
			wantCode: connect.CodePermissionDenied,
			wantErr:  true,
		},
		{
			name:     "error - Unauthenticated when session missing",
			ctx:      context.Background(),
			cfg:      devConfig(),
			req:      validReq(),
			setup:    func(_ *ucmocks.MockPushNotificationUseCase) {},
			wantCode: connect.CodeUnauthenticated,
			wantErr:  true,
		},
		{
			name: "error - InvalidArgument when artist_id is nil",
			ctx:  authedCtx("ext-user-1"),
			cfg:  devConfig(),
			req: &rpcv1.NotifyNewConcertsRequest{
				ConcertIds: []*entitypb.EventId{{Value: "concert-1"}},
			},
			setup:    func(_ *ucmocks.MockPushNotificationUseCase) {},
			wantCode: connect.CodeInvalidArgument,
			wantErr:  true,
		},
		{
			name: "error - InvalidArgument when artist_id value is empty string",
			ctx:  authedCtx("ext-user-1"),
			cfg:  devConfig(),
			req: &rpcv1.NotifyNewConcertsRequest{
				ArtistId:   &entitypb.ArtistId{Value: ""},
				ConcertIds: []*entitypb.EventId{{Value: "concert-1"}},
			},
			setup:    func(_ *ucmocks.MockPushNotificationUseCase) {},
			wantCode: connect.CodeInvalidArgument,
			wantErr:  true,
		},
		{
			name: "error - InvalidArgument when concert_ids empty",
			ctx:  authedCtx("ext-user-1"),
			cfg:  devConfig(),
			req: &rpcv1.NotifyNewConcertsRequest{
				ArtistId:   &entitypb.ArtistId{Value: "artist-1"},
				ConcertIds: []*entitypb.EventId{},
			},
			setup:    func(_ *ucmocks.MockPushNotificationUseCase) {},
			wantCode: connect.CodeInvalidArgument,
			wantErr:  true,
		},
		{
			name: "error - InvalidArgument when a concert_id value is empty",
			ctx:  authedCtx("ext-user-1"),
			cfg:  devConfig(),
			req: &rpcv1.NotifyNewConcertsRequest{
				ArtistId:   &entitypb.ArtistId{Value: "artist-1"},
				ConcertIds: []*entitypb.EventId{{Value: "concert-1"}, {Value: ""}},
			},
			setup:    func(_ *ucmocks.MockPushNotificationUseCase) {},
			wantCode: connect.CodeInvalidArgument,
			wantErr:  true,
		},
		{
			name: "propagates use case error unchanged",
			ctx:  authedCtx("ext-user-1"),
			cfg:  devConfig(),
			req:  validReq(),
			setup: func(uc *ucmocks.MockPushNotificationUseCase) {
				uc.EXPECT().NotifyNewConcerts(mock.Anything, mock.Anything).
					Return(apperr.New(apperrcodes.InvalidArgument, "ownership violation")).Once()
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			logger, err := logging.New()
			require.NoError(t, err)

			uc := ucmocks.NewMockPushNotificationUseCase(t)
			ur := entitymocks.NewMockUserRepository(t)
			tt.setup(uc)
			h := handler.NewPushNotificationHandler(uc, ur, tt.cfg, logger)

			resp, err := h.NotifyNewConcerts(tt.ctx, connect.NewRequest(tt.req))

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
			h := handler.NewPushNotificationHandler(uc, ur, devConfig(), logger)

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
