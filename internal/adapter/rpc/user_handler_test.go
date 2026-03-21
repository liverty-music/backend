package rpc_test

import (
	"testing"

	userv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/user/v1"
	"connectrpc.com/connect"
	"github.com/liverty-music/backend/internal/adapter/rpc"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestUserHandler_Get(t *testing.T) {
	t.Parallel()

	t.Run("returns user when found", func(t *testing.T) {
		t.Parallel()
		logger, err := logging.New()
		require.NoError(t, err)
		userUC := mocks.NewMockUserUseCase(t)
		h := rpc.NewUserHandler(userUC, logger)

		userUC.EXPECT().GetByExternalID(mock.Anything, "ext-123").Return(&entity.User{
			ID:         "user-1",
			ExternalID: "ext-123",
			Email:      "test@example.com",
			Name:       "Test User",
		}, nil).Once()

		ctx := authedCtx("ext-123")
		req := connect.NewRequest(&userv1.GetRequest{})

		resp, err := h.Get(ctx, req)

		assert.NoError(t, err)
		assert.NotNil(t, resp)
		assert.NotNil(t, resp.Msg.User)
		assert.Equal(t, "user-1", resp.Msg.User.Id.Value)
		assert.Equal(t, "test@example.com", resp.Msg.User.Email.Value)
	})

	t.Run("returns error when user not found", func(t *testing.T) {
		t.Parallel()
		logger, err := logging.New()
		require.NoError(t, err)
		userUC := mocks.NewMockUserUseCase(t)
		h := rpc.NewUserHandler(userUC, logger)

		userUC.EXPECT().GetByExternalID(mock.Anything, "ext-unknown").Return(
			nil, apperr.New(apperr.ErrNotFound.Code, "user not found"),
		).Once()

		ctx := authedCtx("ext-unknown")
		req := connect.NewRequest(&userv1.GetRequest{})

		resp, err := h.Get(ctx, req)

		assert.Error(t, err)
		assert.Nil(t, resp)
		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})
}
