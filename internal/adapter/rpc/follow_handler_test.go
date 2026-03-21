package rpc_test

import (
	"context"
	"testing"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	followv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/follow/v1"
	"connectrpc.com/connect"
	handler "github.com/liverty-music/backend/internal/adapter/rpc"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/auth"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func followAuthedCtx(sub string) context.Context {
	return auth.WithClaims(context.Background(), &auth.Claims{Sub: sub})
}

func TestFollowHandler_Follow(t *testing.T) {
	t.Parallel()

	artistID := "artist-uuid-1"

	tests := []struct {
		name     string
		ctx      context.Context
		req      *followv1.FollowRequest
		setup    func(uc *ucmocks.MockFollowUseCase)
		wantCode connect.Code
		wantErr  bool
	}{
		{
			name: "success",
			ctx:  followAuthedCtx("ext-user-1"),
			req:  &followv1.FollowRequest{ArtistId: &entityv1.ArtistId{Value: artistID}},
			setup: func(uc *ucmocks.MockFollowUseCase) {
				uc.EXPECT().Follow(mock.Anything, "ext-user-1", artistID).Return(nil).Once()
			},
			wantErr: false,
		},
		{
			name:     "error - unauthenticated",
			ctx:      context.Background(),
			req:      &followv1.FollowRequest{ArtistId: &entityv1.ArtistId{Value: artistID}},
			setup:    func(_ *ucmocks.MockFollowUseCase) {},
			wantCode: connect.CodeUnauthenticated,
			wantErr:  true,
		},
		{
			name:     "error - missing artist_id",
			ctx:      followAuthedCtx("ext-user-1"),
			req:      &followv1.FollowRequest{ArtistId: nil},
			setup:    func(_ *ucmocks.MockFollowUseCase) {},
			wantCode: connect.CodeInvalidArgument,
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			logger, err := logging.New()
			require.NoError(t, err)

			uc := ucmocks.NewMockFollowUseCase(t)
			tt.setup(uc)
			h := handler.NewFollowHandler(uc, logger)

			resp, err := h.Follow(tt.ctx, connect.NewRequest(tt.req))

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

func TestFollowHandler_Unfollow(t *testing.T) {
	t.Parallel()

	artistID := "artist-uuid-1"

	tests := []struct {
		name     string
		ctx      context.Context
		req      *followv1.UnfollowRequest
		setup    func(uc *ucmocks.MockFollowUseCase)
		wantCode connect.Code
		wantErr  bool
	}{
		{
			name: "success",
			ctx:  followAuthedCtx("ext-user-1"),
			req:  &followv1.UnfollowRequest{ArtistId: &entityv1.ArtistId{Value: artistID}},
			setup: func(uc *ucmocks.MockFollowUseCase) {
				uc.EXPECT().Unfollow(mock.Anything, "ext-user-1", artistID).Return(nil).Once()
			},
			wantErr: false,
		},
		{
			name:     "error - unauthenticated",
			ctx:      context.Background(),
			req:      &followv1.UnfollowRequest{ArtistId: &entityv1.ArtistId{Value: artistID}},
			setup:    func(_ *ucmocks.MockFollowUseCase) {},
			wantCode: connect.CodeUnauthenticated,
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			logger, err := logging.New()
			require.NoError(t, err)

			uc := ucmocks.NewMockFollowUseCase(t)
			tt.setup(uc)
			h := handler.NewFollowHandler(uc, logger)

			resp, err := h.Unfollow(tt.ctx, connect.NewRequest(tt.req))

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

func TestFollowHandler_ListFollowed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		ctx      context.Context
		setup    func(uc *ucmocks.MockFollowUseCase)
		wantCode connect.Code
		wantErr  bool
	}{
		{
			name: "success",
			ctx:  followAuthedCtx("ext-user-1"),
			setup: func(uc *ucmocks.MockFollowUseCase) {
				uc.EXPECT().ListFollowed(mock.Anything, "ext-user-1").Return([]*entity.FollowedArtist{}, nil).Once()
			},
			wantErr: false,
		},
		{
			name:     "error - unauthenticated",
			ctx:      context.Background(),
			setup:    func(_ *ucmocks.MockFollowUseCase) {},
			wantCode: connect.CodeUnauthenticated,
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			logger, err := logging.New()
			require.NoError(t, err)

			uc := ucmocks.NewMockFollowUseCase(t)
			tt.setup(uc)
			h := handler.NewFollowHandler(uc, logger)

			resp, err := h.ListFollowed(tt.ctx, connect.NewRequest(&followv1.ListFollowedRequest{}))

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

func TestFollowHandler_SetHype(t *testing.T) {
	t.Parallel()

	artistID := "artist-uuid-1"

	tests := []struct {
		name     string
		ctx      context.Context
		req      *followv1.SetHypeRequest
		setup    func(uc *ucmocks.MockFollowUseCase)
		wantCode connect.Code
		wantErr  bool
	}{
		{
			name: "success",
			ctx:  followAuthedCtx("ext-user-1"),
			req: &followv1.SetHypeRequest{
				ArtistId: &entityv1.ArtistId{Value: artistID},
				Hype:     entityv1.HypeType_HYPE_TYPE_HOME,
			},
			setup: func(uc *ucmocks.MockFollowUseCase) {
				uc.EXPECT().SetHype(mock.Anything, "ext-user-1", artistID, mock.Anything).Return(nil).Once()
			},
			wantErr: false,
		},
		{
			name:     "error - unauthenticated",
			ctx:      context.Background(),
			req:      &followv1.SetHypeRequest{ArtistId: &entityv1.ArtistId{Value: artistID}, Hype: entityv1.HypeType_HYPE_TYPE_HOME},
			setup:    func(_ *ucmocks.MockFollowUseCase) {},
			wantCode: connect.CodeUnauthenticated,
			wantErr:  true,
		},
		{
			name:     "error - missing artist_id",
			ctx:      followAuthedCtx("ext-user-1"),
			req:      &followv1.SetHypeRequest{ArtistId: nil, Hype: entityv1.HypeType_HYPE_TYPE_HOME},
			setup:    func(_ *ucmocks.MockFollowUseCase) {},
			wantCode: connect.CodeInvalidArgument,
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			logger, err := logging.New()
			require.NoError(t, err)

			uc := ucmocks.NewMockFollowUseCase(t)
			tt.setup(uc)
			h := handler.NewFollowHandler(uc, logger)

			resp, err := h.SetHype(tt.ctx, connect.NewRequest(tt.req))

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
