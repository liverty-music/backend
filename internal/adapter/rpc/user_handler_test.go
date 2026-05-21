package rpc_test

import (
	"context"
	"testing"

	entitypb "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
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

const (
	testCallerUserID  = "user-1"
	testCallerExtID   = "ext-123"
	testForeignUserID = "user-999"
)

func newUserIDProto(id string) *entitypb.UserId {
	return &entitypb.UserId{Value: id}
}

func TestUserHandler_Get(t *testing.T) {
	t.Parallel()

	t.Run("returns user when user_id matches JWT", func(t *testing.T) {
		t.Parallel()
		logger, err := logging.New()
		require.NoError(t, err)
		userUC := mocks.NewMockUserUseCase(t)
		h := rpc.NewUserHandler(userUC, nil, logger)

		userUC.EXPECT().GetByExternalID(mock.Anything, testCallerExtID).Return(&entity.User{
			ID:         testCallerUserID,
			ExternalID: testCallerExtID,
			Email:      "test@example.com",
			Name:       "Test User",
		}, nil).Once()

		ctx := authedCtx(testCallerExtID)
		req := connect.NewRequest(&userv1.GetRequest{UserId: newUserIDProto(testCallerUserID)})

		resp, err := h.Get(ctx, req)

		assert.NoError(t, err)
		require.NotNil(t, resp)
		assert.Equal(t, testCallerUserID, resp.Msg.User.Id.Value)
		assert.Equal(t, "test@example.com", resp.Msg.User.Email.Value)
	})

	t.Run("returns PermissionDenied when user_id mismatches JWT", func(t *testing.T) {
		t.Parallel()
		logger, err := logging.New()
		require.NoError(t, err)
		userUC := mocks.NewMockUserUseCase(t)
		h := rpc.NewUserHandler(userUC, nil, logger)

		userUC.EXPECT().GetByExternalID(mock.Anything, testCallerExtID).Return(&entity.User{
			ID:         testCallerUserID,
			ExternalID: testCallerExtID,
		}, nil).Once()

		ctx := authedCtx(testCallerExtID)
		req := connect.NewRequest(&userv1.GetRequest{UserId: newUserIDProto(testForeignUserID)})

		resp, err := h.Get(ctx, req)

		assert.Nil(t, resp)
		var connectErr *connect.Error
		require.ErrorAs(t, err, &connectErr)
		assert.Equal(t, connect.CodePermissionDenied, connectErr.Code())
	})

	t.Run("returns InvalidArgument when user_id is empty", func(t *testing.T) {
		t.Parallel()
		logger, err := logging.New()
		require.NoError(t, err)
		userUC := mocks.NewMockUserUseCase(t)
		h := rpc.NewUserHandler(userUC, nil, logger)

		userUC.EXPECT().GetByExternalID(mock.Anything, testCallerExtID).Return(&entity.User{
			ID:         testCallerUserID,
			ExternalID: testCallerExtID,
		}, nil).Once()

		ctx := authedCtx(testCallerExtID)
		req := connect.NewRequest(&userv1.GetRequest{})

		resp, err := h.Get(ctx, req)

		assert.Nil(t, resp)
		var connectErr *connect.Error
		require.ErrorAs(t, err, &connectErr)
		assert.Equal(t, connect.CodeInvalidArgument, connectErr.Code())
	})

	t.Run("returns error when user not found", func(t *testing.T) {
		t.Parallel()
		logger, err := logging.New()
		require.NoError(t, err)
		userUC := mocks.NewMockUserUseCase(t)
		h := rpc.NewUserHandler(userUC, nil, logger)

		userUC.EXPECT().GetByExternalID(mock.Anything, "ext-unknown").Return(
			nil, apperr.New(apperr.ErrNotFound.Code, "user not found"),
		).Once()

		ctx := authedCtx("ext-unknown")
		req := connect.NewRequest(&userv1.GetRequest{UserId: newUserIDProto(testCallerUserID)})

		resp, err := h.Get(ctx, req)

		assert.Nil(t, resp)
		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})
}

func TestUserHandler_UpdateHome(t *testing.T) {
	t.Parallel()

	existingUser := &entity.User{ID: testCallerUserID, ExternalID: testCallerExtID}
	updatedHome := &entity.Home{CountryCode: "JP", Level1: "JP-13"}
	updatedUser := &entity.User{ID: testCallerUserID, ExternalID: testCallerExtID, Home: updatedHome}

	homeProto := &entitypb.Home{CountryCode: "JP", Level_1: "JP-13"}

	t.Run("updates home when user_id matches JWT", func(t *testing.T) {
		t.Parallel()
		logger, err := logging.New()
		require.NoError(t, err)
		userUC := mocks.NewMockUserUseCase(t)
		h := rpc.NewUserHandler(userUC, nil, logger)

		userUC.EXPECT().GetByExternalID(mock.Anything, testCallerExtID).Return(existingUser, nil).Once()
		userUC.EXPECT().UpdateHome(mock.Anything, testCallerUserID, mock.Anything).Return(updatedUser, nil).Once()

		ctx := authedCtx(testCallerExtID)
		req := connect.NewRequest(&userv1.UpdateHomeRequest{
			UserId: newUserIDProto(testCallerUserID),
			Home:   homeProto,
		})

		resp, err := h.UpdateHome(ctx, req)

		assert.NoError(t, err)
		require.NotNil(t, resp)
		assert.Equal(t, testCallerUserID, resp.Msg.User.Id.Value)
	})

	t.Run("returns PermissionDenied on user_id mismatch", func(t *testing.T) {
		t.Parallel()
		logger, err := logging.New()
		require.NoError(t, err)
		userUC := mocks.NewMockUserUseCase(t)
		h := rpc.NewUserHandler(userUC, nil, logger)

		userUC.EXPECT().GetByExternalID(mock.Anything, testCallerExtID).Return(existingUser, nil).Once()
		// UpdateHome must NOT be called.

		ctx := authedCtx(testCallerExtID)
		req := connect.NewRequest(&userv1.UpdateHomeRequest{
			UserId: newUserIDProto(testForeignUserID),
			Home:   homeProto,
		})

		resp, err := h.UpdateHome(ctx, req)

		assert.Nil(t, resp)
		var connectErr *connect.Error
		require.ErrorAs(t, err, &connectErr)
		assert.Equal(t, connect.CodePermissionDenied, connectErr.Code())
	})

	t.Run("returns InvalidArgument when user_id empty", func(t *testing.T) {
		t.Parallel()
		logger, err := logging.New()
		require.NoError(t, err)
		userUC := mocks.NewMockUserUseCase(t)
		h := rpc.NewUserHandler(userUC, nil, logger)

		userUC.EXPECT().GetByExternalID(mock.Anything, testCallerExtID).Return(existingUser, nil).Once()

		ctx := authedCtx(testCallerExtID)
		req := connect.NewRequest(&userv1.UpdateHomeRequest{Home: homeProto})

		resp, err := h.UpdateHome(ctx, req)

		assert.Nil(t, resp)
		var connectErr *connect.Error
		require.ErrorAs(t, err, &connectErr)
		assert.Equal(t, connect.CodeInvalidArgument, connectErr.Code())
	})
}

// TestUserHandler_UpdatePreferredLanguage tests the UpdatePreferredLanguage handler
// via its pre-BSR-gen placeholder signature.
//
// TODO(persist-user-language): migrate these tests to use the generated
// *connect.Request[userv1.UpdatePreferredLanguageRequest] signature after BSR gen.
func TestUserHandler_UpdatePreferredLanguage(t *testing.T) {
	t.Parallel()

	existingUser := &entity.User{ID: testCallerUserID, ExternalID: testCallerExtID}

	t.Run("happy path — returns updated user", func(t *testing.T) {
		t.Parallel()
		logger, err := logging.New()
		require.NoError(t, err)
		userUC := mocks.NewMockUserUseCase(t)
		h := rpc.NewUserHandler(userUC, nil, logger)

		updatedUser := &entity.User{
			ID:                testCallerUserID,
			ExternalID:        testCallerExtID,
			PreferredLanguage: "en",
		}

		userUC.EXPECT().GetByExternalID(mock.Anything, testCallerExtID).
			Return(existingUser, nil).Once()
		userUC.EXPECT().UpdatePreferredLanguage(mock.Anything, testCallerUserID, "en").
			Return(updatedUser, nil).Once()

		ctx := authedCtx(testCallerExtID)
		params := rpc.UpdatePreferredLanguageParams{
			UserID:            testCallerUserID,
			PreferredLanguage: "en",
		}

		result, err := h.UpdatePreferredLanguage(ctx, params)

		assert.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, testCallerUserID, result.ID)
		assert.Equal(t, "en", result.PreferredLanguage)
	})

	t.Run("PermissionDenied when user_id mismatches JWT", func(t *testing.T) {
		t.Parallel()
		logger, err := logging.New()
		require.NoError(t, err)
		userUC := mocks.NewMockUserUseCase(t)
		h := rpc.NewUserHandler(userUC, nil, logger)

		userUC.EXPECT().GetByExternalID(mock.Anything, testCallerExtID).
			Return(existingUser, nil).Once()
		// UpdatePreferredLanguage must NOT be called.

		ctx := authedCtx(testCallerExtID)
		params := rpc.UpdatePreferredLanguageParams{
			UserID:            testForeignUserID, // cross-user request
			PreferredLanguage: "en",
		}

		result, err := h.UpdatePreferredLanguage(ctx, params)

		assert.Nil(t, result)
		var connectErr *connect.Error
		require.ErrorAs(t, err, &connectErr)
		assert.Equal(t, connect.CodePermissionDenied, connectErr.Code())
	})

	t.Run("Unauthenticated when no JWT claims", func(t *testing.T) {
		t.Parallel()
		logger, err := logging.New()
		require.NoError(t, err)
		userUC := mocks.NewMockUserUseCase(t)
		h := rpc.NewUserHandler(userUC, nil, logger)

		ctx := context.Background() // no auth claims
		params := rpc.UpdatePreferredLanguageParams{
			UserID:            testCallerUserID,
			PreferredLanguage: "ja",
		}

		result, err := h.UpdatePreferredLanguage(ctx, params)

		assert.Nil(t, result)
		var connectErr *connect.Error
		require.ErrorAs(t, err, &connectErr)
		assert.Equal(t, connect.CodeUnauthenticated, connectErr.Code())
	})
}
