package rpc_test

import (
	"testing"

	userv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/user/v1"
	"connectrpc.com/connect"
	"github.com/liverty-music/backend/internal/adapter/rpc"
	"github.com/liverty-music/backend/internal/entity"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestUserHandler_ResendEmailVerification(t *testing.T) {
	t.Parallel()

	existingUser := &entity.User{ID: testCallerUserID, ExternalID: testCallerExtID}

	t.Run("success when user_id matches JWT", func(t *testing.T) {
		t.Parallel()
		logger, err := logging.New()
		require.NoError(t, err)
		userUC := ucmocks.NewMockUserUseCase(t)
		verifier := ucmocks.NewMockEmailVerifier(t)
		h := rpc.NewUserHandler(userUC, verifier, logger)

		userUC.EXPECT().GetByExternalID(mock.Anything, testCallerExtID).Return(existingUser, nil).Once()
		verifier.EXPECT().ResendVerification(mock.Anything, testCallerExtID).Return(nil).Once()

		ctx := authedCtx(testCallerExtID)
		req := connect.NewRequest(&userv1.ResendEmailVerificationRequest{
			UserId: newUserIDProto(testCallerUserID),
		})

		resp, err := h.ResendEmailVerification(ctx, req)

		assert.NoError(t, err)
		assert.NotNil(t, resp)
	})

	t.Run("returns PermissionDenied on user_id mismatch — verifier not invoked", func(t *testing.T) {
		t.Parallel()
		logger, err := logging.New()
		require.NoError(t, err)
		userUC := ucmocks.NewMockUserUseCase(t)
		verifier := ucmocks.NewMockEmailVerifier(t)
		h := rpc.NewUserHandler(userUC, verifier, logger)

		userUC.EXPECT().GetByExternalID(mock.Anything, testCallerExtID).Return(existingUser, nil).Once()
		// ResendVerification must NOT be called.

		ctx := authedCtx(testCallerExtID)
		req := connect.NewRequest(&userv1.ResendEmailVerificationRequest{
			UserId: newUserIDProto(testForeignUserID),
		})

		resp, err := h.ResendEmailVerification(ctx, req)

		assert.Nil(t, resp)
		var connectErr *connect.Error
		require.ErrorAs(t, err, &connectErr)
		assert.Equal(t, connect.CodePermissionDenied, connectErr.Code())
	})

	t.Run("returns InvalidArgument when user_id is empty", func(t *testing.T) {
		t.Parallel()
		logger, err := logging.New()
		require.NoError(t, err)
		userUC := ucmocks.NewMockUserUseCase(t)
		verifier := ucmocks.NewMockEmailVerifier(t)
		h := rpc.NewUserHandler(userUC, verifier, logger)

		userUC.EXPECT().GetByExternalID(mock.Anything, testCallerExtID).Return(existingUser, nil).Once()

		ctx := authedCtx(testCallerExtID)
		req := connect.NewRequest(&userv1.ResendEmailVerificationRequest{})

		resp, err := h.ResendEmailVerification(ctx, req)

		assert.Nil(t, resp)
		var connectErr *connect.Error
		require.ErrorAs(t, err, &connectErr)
		assert.Equal(t, connect.CodeInvalidArgument, connectErr.Code())
	})

	t.Run("unavailable when verifier is nil", func(t *testing.T) {
		t.Parallel()
		logger, err := logging.New()
		require.NoError(t, err)
		userUC := ucmocks.NewMockUserUseCase(t)
		h := rpc.NewUserHandler(userUC, nil, logger)

		ctx := authedCtx(testCallerExtID)
		req := connect.NewRequest(&userv1.ResendEmailVerificationRequest{
			UserId: newUserIDProto(testCallerUserID),
		})

		resp, err := h.ResendEmailVerification(ctx, req)

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

		userUC.EXPECT().GetByExternalID(mock.Anything, testCallerExtID).Return(existingUser, nil).Once()
		verifier.EXPECT().ResendVerification(mock.Anything, testCallerExtID).
			Return(apperr.New(apperr.ErrFailedPrecondition.Code, "email is already verified")).Once()

		ctx := authedCtx(testCallerExtID)
		req := connect.NewRequest(&userv1.ResendEmailVerificationRequest{
			UserId: newUserIDProto(testCallerUserID),
		})

		resp, err := h.ResendEmailVerification(ctx, req)

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

		const rateExtID = "ext-rate"
		const rateUserID = "user-rate"
		rateUser := &entity.User{ID: rateUserID, ExternalID: rateExtID}

		userUC.EXPECT().GetByExternalID(mock.Anything, rateExtID).Return(rateUser, nil).Times(4)
		verifier.EXPECT().ResendVerification(mock.Anything, rateExtID).Return(nil).Times(3)

		ctx := authedCtx(rateExtID)
		req := connect.NewRequest(&userv1.ResendEmailVerificationRequest{
			UserId: newUserIDProto(rateUserID),
		})

		for range 3 {
			resp, err := h.ResendEmailVerification(ctx, req)
			assert.NoError(t, err)
			assert.NotNil(t, resp)
		}

		resp, err := h.ResendEmailVerification(ctx, req)
		assert.Nil(t, resp)
		assert.Equal(t, connect.CodeResourceExhausted, connect.CodeOf(err))
	})
}
