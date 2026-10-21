package rpc_test

import (
	"context"
	"testing"

	followv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/follow/v1/followv1connect"
	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	followv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/follow/v1"
	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
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

func followAuthedCtx(sub string) context.Context {
	return auth.WithClaims(context.Background(), &auth.Claims{Sub: sub})
}

func TestFollowHandler_Follow(t *testing.T) {
	t.Parallel()

	artistID := "artist-uuid-1"
	internalUserID := "internal-user-uuid-1"

	tests := []struct {
		name     string
		ctx      context.Context
		req      *followv1.FollowRequest
		setup    func(uc *ucmocks.MockFollowUseCase, repo *entitymocks.MockUserRepository)
		wantCode connect.Code
		wantErr  bool
	}{
		{
			// @spec components/adapter/fan/api/rpc/follow "Signed-in fan follows"
			name: "success",
			ctx:  followAuthedCtx("ext-user-1"),
			req:  &followv1.FollowRequest{ArtistId: &entityv1.ArtistId{Value: artistID}},
			setup: func(uc *ucmocks.MockFollowUseCase, repo *entitymocks.MockUserRepository) {
				repo.EXPECT().GetByProviderUserID(mock.Anything, "ext-user-1").Return(&entity.User{ID: internalUserID}, nil).Once()
				uc.EXPECT().Follow(mock.Anything, internalUserID, artistID).Return(nil).Once()
			},
			wantErr: false,
		},
		{
			// @spec components/adapter/fan/api/rpc/follow "Not signed in"
			name:     "error - unauthenticated",
			ctx:      context.Background(),
			req:      &followv1.FollowRequest{ArtistId: &entityv1.ArtistId{Value: artistID}},
			setup:    func(_ *ucmocks.MockFollowUseCase, _ *entitymocks.MockUserRepository) {},
			wantCode: connect.CodeUnauthenticated,
			wantErr:  true,
		},
		{
			// @spec components/adapter/fan/api/rpc/follow "Caller has no account"
			name: "error - caller has no account",
			ctx:  followAuthedCtx("ext-user-1"),
			req:  &followv1.FollowRequest{ArtistId: &entityv1.ArtistId{Value: artistID}},
			setup: func(_ *ucmocks.MockFollowUseCase, repo *entitymocks.MockUserRepository) {
				repo.EXPECT().GetByProviderUserID(mock.Anything, "ext-user-1").Return(nil, apperr.ErrNotFound).Once()
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			logger, err := logging.New()
			require.NoError(t, err)

			uc := ucmocks.NewMockFollowUseCase(t)
			repo := entitymocks.NewMockUserRepository(t)
			tt.setup(uc, repo)
			h := handler.NewFollowHandler(uc, repo, logger)

			resp, err := h.Follow(tt.ctx, tt.req)

			if tt.wantErr {
				assert.Error(t, err)
				if tt.wantCode == 0 {
					assert.ErrorIs(t, err, apperr.ErrNotFound)
				}
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
	internalUserID := "internal-user-uuid-1"

	tests := []struct {
		name     string
		ctx      context.Context
		req      *followv1.UnfollowRequest
		setup    func(uc *ucmocks.MockFollowUseCase, repo *entitymocks.MockUserRepository)
		wantCode connect.Code
		wantErr  bool
	}{
		{
			name: "success",
			ctx:  followAuthedCtx("ext-user-1"),
			req:  &followv1.UnfollowRequest{ArtistId: &entityv1.ArtistId{Value: artistID}},
			setup: func(uc *ucmocks.MockFollowUseCase, repo *entitymocks.MockUserRepository) {
				repo.EXPECT().GetByProviderUserID(mock.Anything, "ext-user-1").Return(&entity.User{ID: internalUserID}, nil).Once()
				uc.EXPECT().Unfollow(mock.Anything, internalUserID, artistID).Return(nil).Once()
			},
			wantErr: false,
		},
		{
			name:     "error - unauthenticated",
			ctx:      context.Background(),
			req:      &followv1.UnfollowRequest{ArtistId: &entityv1.ArtistId{Value: artistID}},
			setup:    func(_ *ucmocks.MockFollowUseCase, _ *entitymocks.MockUserRepository) {},
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
			repo := entitymocks.NewMockUserRepository(t)
			tt.setup(uc, repo)
			h := handler.NewFollowHandler(uc, repo, logger)

			resp, err := h.Unfollow(tt.ctx, tt.req)

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

func TestFollowHandler_List(t *testing.T) {
	t.Parallel()

	internalUserID := "internal-user-uuid-1"

	tests := []struct {
		name     string
		ctx      context.Context
		setup    func(uc *ucmocks.MockFollowUseCase, repo *entitymocks.MockUserRepository)
		wantCode connect.Code
		wantErr  bool
	}{
		{
			name: "success",
			ctx:  followAuthedCtx("ext-user-1"),
			setup: func(uc *ucmocks.MockFollowUseCase, repo *entitymocks.MockUserRepository) {
				repo.EXPECT().GetByProviderUserID(mock.Anything, "ext-user-1").Return(&entity.User{ID: internalUserID}, nil).Once()
				uc.EXPECT().List(mock.Anything, internalUserID).Return([]*entity.FollowedArtist{}, nil).Once()
			},
			wantErr: false,
		},
		{
			name:     "error - unauthenticated",
			ctx:      context.Background(),
			setup:    func(_ *ucmocks.MockFollowUseCase, _ *entitymocks.MockUserRepository) {},
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
			repo := entitymocks.NewMockUserRepository(t)
			tt.setup(uc, repo)
			h := handler.NewFollowHandler(uc, repo, logger)

			resp, err := h.List(tt.ctx, &followv1.ListRequest{})

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
	internalUserID := "internal-user-uuid-1"

	tests := []struct {
		name     string
		ctx      context.Context
		req      *followv1.SetHypeRequest
		setup    func(uc *ucmocks.MockFollowUseCase, repo *entitymocks.MockUserRepository)
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
			setup: func(uc *ucmocks.MockFollowUseCase, repo *entitymocks.MockUserRepository) {
				repo.EXPECT().GetByProviderUserID(mock.Anything, "ext-user-1").Return(&entity.User{ID: internalUserID}, nil).Once()
				uc.EXPECT().SetHype(mock.Anything, internalUserID, artistID, mock.Anything).Return(nil).Once()
			},
			wantErr: false,
		},
		{
			name:     "error - unauthenticated",
			ctx:      context.Background(),
			req:      &followv1.SetHypeRequest{ArtistId: &entityv1.ArtistId{Value: artistID}, Hype: entityv1.HypeType_HYPE_TYPE_HOME},
			setup:    func(_ *ucmocks.MockFollowUseCase, _ *entitymocks.MockUserRepository) {},
			wantCode: connect.CodeUnauthenticated,
			wantErr:  true,
		},
		{
			// @spec components/adapter/fan/api/rpc/follow "Missing hype level"
			name: "error - HYPE_TYPE_UNSPECIFIED rejected with InvalidArgument",
			ctx:  followAuthedCtx("ext-user-1"),
			req: &followv1.SetHypeRequest{
				ArtistId: &entityv1.ArtistId{Value: artistID},
				Hype:     entityv1.HypeType_HYPE_TYPE_UNSPECIFIED,
			},
			setup: func(_ *ucmocks.MockFollowUseCase, repo *entitymocks.MockUserRepository) {
				repo.EXPECT().GetByProviderUserID(mock.Anything, "ext-user-1").Return(&entity.User{ID: internalUserID}, nil).Once()
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

			uc := ucmocks.NewMockFollowUseCase(t)
			repo := entitymocks.NewMockUserRepository(t)
			tt.setup(uc, repo)
			h := handler.NewFollowHandler(uc, repo, logger)

			resp, err := h.SetHype(tt.ctx, tt.req)

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

func TestFollowHandler_Boundary(t *testing.T) {
	t.Parallel()

	t.Run("missing artist", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/fan/api/rpc/follow "Missing artist"
		logger, err := logging.New()
		require.NoError(t, err)
		// The mocks fail the test if the usecase runs.
		h := handler.NewFollowHandler(ucmocks.NewMockFollowUseCase(t), entitymocks.NewMockUserRepository(t), logger)
		srv := newFanBoundaryServer(t, func(s *connect.Server) {
			followv1connect.RegisterFollowServiceHandler(s, h)
		})
		client := followv1connect.NewFollowServiceClient(connect.NewClient(connecthttp.NewTransport(srv.Client(), srv.URL)))

		_, err = client.Follow(withFanToken(&followv1.FollowRequest{}))

		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	})
}
