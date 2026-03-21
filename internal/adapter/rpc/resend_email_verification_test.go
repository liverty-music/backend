package rpc_test

import (
	"testing"

	userv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/user/v1"
	"connectrpc.com/connect"
	"github.com/liverty-music/backend/internal/adapter/rpc"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestUserHandler_ResendEmailVerification(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		logger, err := logging.New()
		require.NoError(t, err)
		userUC := ucmocks.NewMockUserUseCase(t)
		verifier := ucmocks.NewMockEmailVerifier(t)
		h := rpc.NewUserHandler(userUC, verifier, logger)

		verifier.EXPECT().ResendVerification(mock.Anything, "ext-123").Return(nil).Once()

		ctx := authedCtx("ext-123")
		req := connect.NewRequest(&userv1.ResendEmailVerificationRequest{})

		resp, err := h.ResendEmailVerification(ctx, req)

		assert.NoError(t, err)
		assert.NotNil(t, resp)
	})

	t.Run("unavailable when verifier is nil", func(t *testing.T) {
		t.Parallel()
		logger, err := logging.New()
		require.NoError(t, err)
		userUC := ucmocks.NewMockUserUseCase(t)
		h := rpc.NewUserHandler(userUC, nil, logger)

		ctx := authedCtx("ext-123")
		req := connect.NewRequest(&userv1.ResendEmailVerificationRequest{})

		resp, err := h.ResendEmailVerification(ctx, req)

		assert.Error(t, err)
		assert.Nil(t, resp)
		assert.Equal(t, connect.CodeUnavailable, connect.CodeOf(err))
	})

	t.Run("failed precondition when already verified", func(t *testing.T) {
		t.Parallel()
		logger, err := logging.New()
		require.NoError(t, err)
		userUC := ucmocks.NewMockUserUseCase(t)
		verifier := ucmocks.NewMockEmailVerifier(t)
		h := rpc.NewUserHandler(userUC, verifier, logger)

		verifier.EXPECT().ResendVerification(mock.Anything, "ext-123").
			Return(apperr.New(apperr.ErrFailedPrecondition.Code, "email is already verified")).Once()

		ctx := authedCtx("ext-123")
		req := connect.NewRequest(&userv1.ResendEmailVerificationRequest{})

		resp, err := h.ResendEmailVerification(ctx, req)

		assert.Error(t, err)
		assert.Nil(t, resp)
		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("rate limited after 3 requests", func(t *testing.T) {
		t.Parallel()
		logger, err := logging.New()
		require.NoError(t, err)
		userUC := ucmocks.NewMockUserUseCase(t)
		verifier := ucmocks.NewMockEmailVerifier(t)
		h := rpc.NewUserHandler(userUC, verifier, logger)

		verifier.EXPECT().ResendVerification(mock.Anything, "ext-rate").Return(nil).Times(3)

		ctx := authedCtx("ext-rate")
		req := connect.NewRequest(&userv1.ResendEmailVerificationRequest{})

		// First 3 requests succeed.
		for range 3 {
			resp, err := h.ResendEmailVerification(ctx, req)
			assert.NoError(t, err)
			assert.NotNil(t, resp)
		}

		// 4th request is rate limited.
		resp, err := h.ResendEmailVerification(ctx, req)
		assert.Error(t, err)
		assert.Nil(t, resp)
		assert.Equal(t, connect.CodeResourceExhausted, connect.CodeOf(err))
	})
}
