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

// UserHandler delegates the user_id ownership precondition to
// UserUseCase.ResolveCaller (see internal/usecase/user_uc.go); these tests
// exercise the handler's proto<->entity mapping and error propagation only.
// The InvalidArgument/PermissionDenied decision itself is covered by
// TestUserUseCase_ResolveCaller in internal/usecase/user_uc_test.go.

func TestUserHandler_Get(t *testing.T) {
	t.Parallel()

	// @spec components/adapter/fan/api/rpc/user "Own account"
	t.Run("returns user when the use case resolves the caller", func(t *testing.T) {
		t.Parallel()
		logger, err := logging.New()
		require.NoError(t, err)
		userUC := mocks.NewMockUserUseCase(t)
		h := rpc.NewUserHandler(userUC, logger)

		userUC.EXPECT().ResolveCaller(mock.Anything, testCallerExtID, testCallerUserID).Return(&entity.User{
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

	// @spec components/adapter/fan/api/rpc/user "Another user's account"
	t.Run("propagates PermissionDenied from ResolveCaller on user_id mismatch", func(t *testing.T) {
		t.Parallel()
		logger, err := logging.New()
		require.NoError(t, err)
		userUC := mocks.NewMockUserUseCase(t)
		h := rpc.NewUserHandler(userUC, logger)

		userUC.EXPECT().ResolveCaller(mock.Anything, testCallerExtID, testForeignUserID).
			Return(nil, apperr.New(apperr.ErrPermissionDenied.Code, "user_id does not match authenticated user")).Once()

		ctx := authedCtx(testCallerExtID)
		req := connect.NewRequest(&userv1.GetRequest{UserId: newUserIDProto(testForeignUserID)})

		resp, err := h.Get(ctx, req)

		assert.Nil(t, resp)
		assert.ErrorIs(t, err, apperr.ErrPermissionDenied)
	})

	// @spec components/adapter/fan/api/rpc/user "Missing user id"
	t.Run("propagates InvalidArgument from ResolveCaller when user_id is empty", func(t *testing.T) {
		t.Parallel()
		logger, err := logging.New()
		require.NoError(t, err)
		userUC := mocks.NewMockUserUseCase(t)
		h := rpc.NewUserHandler(userUC, logger)

		userUC.EXPECT().ResolveCaller(mock.Anything, testCallerExtID, "").
			Return(nil, apperr.New(apperr.ErrInvalidArgument.Code, "user_id is required")).Once()

		ctx := authedCtx(testCallerExtID)
		req := connect.NewRequest(&userv1.GetRequest{})

		resp, err := h.Get(ctx, req)

		assert.Nil(t, resp)
		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})

	// @spec components/adapter/fan/api/rpc/user "Caller has no account"
	t.Run("returns error when user not found", func(t *testing.T) {
		t.Parallel()
		logger, err := logging.New()
		require.NoError(t, err)
		userUC := mocks.NewMockUserUseCase(t)
		h := rpc.NewUserHandler(userUC, logger)

		userUC.EXPECT().ResolveCaller(mock.Anything, "ext-unknown", testCallerUserID).Return(
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

	callerUser := &entity.User{ID: testCallerUserID, ExternalID: testCallerExtID}
	updatedHome := &entity.Home{CountryCode: "JP", Level1: "JP-13"}
	updatedUser := &entity.User{ID: testCallerUserID, ExternalID: testCallerExtID, Home: updatedHome}

	homeProto := &entitypb.Home{CountryCode: "JP", Level_1: "JP-13"}

	t.Run("updates home when the use case resolves the caller", func(t *testing.T) {
		t.Parallel()
		logger, err := logging.New()
		require.NoError(t, err)
		userUC := mocks.NewMockUserUseCase(t)
		h := rpc.NewUserHandler(userUC, logger)

		userUC.EXPECT().ResolveCaller(mock.Anything, testCallerExtID, testCallerUserID).Return(callerUser, nil).Once()
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

	t.Run("propagates PermissionDenied from ResolveCaller on user_id mismatch", func(t *testing.T) {
		t.Parallel()
		logger, err := logging.New()
		require.NoError(t, err)
		userUC := mocks.NewMockUserUseCase(t)
		h := rpc.NewUserHandler(userUC, logger)

		userUC.EXPECT().ResolveCaller(mock.Anything, testCallerExtID, testForeignUserID).
			Return(nil, apperr.New(apperr.ErrPermissionDenied.Code, "user_id does not match authenticated user")).Once()
		// UpdateHome must NOT be called.

		ctx := authedCtx(testCallerExtID)
		req := connect.NewRequest(&userv1.UpdateHomeRequest{
			UserId: newUserIDProto(testForeignUserID),
			Home:   homeProto,
		})

		resp, err := h.UpdateHome(ctx, req)

		assert.Nil(t, resp)
		assert.ErrorIs(t, err, apperr.ErrPermissionDenied)
	})

	t.Run("propagates InvalidArgument from ResolveCaller when user_id empty", func(t *testing.T) {
		t.Parallel()
		logger, err := logging.New()
		require.NoError(t, err)
		userUC := mocks.NewMockUserUseCase(t)
		h := rpc.NewUserHandler(userUC, logger)

		userUC.EXPECT().ResolveCaller(mock.Anything, testCallerExtID, "").
			Return(nil, apperr.New(apperr.ErrInvalidArgument.Code, "user_id is required")).Once()

		ctx := authedCtx(testCallerExtID)
		req := connect.NewRequest(&userv1.UpdateHomeRequest{Home: homeProto})

		resp, err := h.UpdateHome(ctx, req)

		assert.Nil(t, resp)
		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})
}

func TestUserHandler_UpdatePreferredLanguage(t *testing.T) {
	t.Parallel()

	callerUser := &entity.User{ID: testCallerUserID, ExternalID: testCallerExtID}

	t.Run("happy path — returns updated user", func(t *testing.T) {
		t.Parallel()
		logger, err := logging.New()
		require.NoError(t, err)
		userUC := mocks.NewMockUserUseCase(t)
		h := rpc.NewUserHandler(userUC, logger)

		updatedUser := &entity.User{
			ID:                testCallerUserID,
			ExternalID:        testCallerExtID,
			PreferredLanguage: "en",
		}

		userUC.EXPECT().ResolveCaller(mock.Anything, testCallerExtID, testCallerUserID).
			Return(callerUser, nil).Once()
		userUC.EXPECT().UpdatePreferredLanguage(mock.Anything, testCallerUserID, "en").
			Return(updatedUser, nil).Once()

		ctx := authedCtx(testCallerExtID)
		req := connect.NewRequest(&userv1.UpdatePreferredLanguageRequest{
			UserId:            newUserIDProto(testCallerUserID),
			PreferredLanguage: "en",
		})

		resp, err := h.UpdatePreferredLanguage(ctx, req)

		assert.NoError(t, err)
		require.NotNil(t, resp)
		assert.Equal(t, testCallerUserID, resp.Msg.User.GetId().GetValue())
		assert.Equal(t, "en", resp.Msg.User.GetPreferredLanguage())
	})

	t.Run("propagates PermissionDenied from ResolveCaller on user_id mismatch", func(t *testing.T) {
		t.Parallel()
		logger, err := logging.New()
		require.NoError(t, err)
		userUC := mocks.NewMockUserUseCase(t)
		h := rpc.NewUserHandler(userUC, logger)

		userUC.EXPECT().ResolveCaller(mock.Anything, testCallerExtID, testForeignUserID).
			Return(nil, apperr.New(apperr.ErrPermissionDenied.Code, "user_id does not match authenticated user")).Once()
		// UpdatePreferredLanguage must NOT be called.

		ctx := authedCtx(testCallerExtID)
		req := connect.NewRequest(&userv1.UpdatePreferredLanguageRequest{
			UserId:            newUserIDProto(testForeignUserID), // cross-user request
			PreferredLanguage: "en",
		})

		resp, err := h.UpdatePreferredLanguage(ctx, req)

		assert.Nil(t, resp)
		assert.ErrorIs(t, err, apperr.ErrPermissionDenied)
	})

	t.Run("propagates InvalidArgument from ResolveCaller when user_id is empty", func(t *testing.T) {
		// Per the rpc-auth-scoping convention, ResolveCaller rejects an
		// empty client-supplied user_id with InvalidArgument before any
		// business logic runs. This test pins the contract; the format
		// check has already passed at this point.
		t.Parallel()
		logger, err := logging.New()
		require.NoError(t, err)
		userUC := mocks.NewMockUserUseCase(t)
		h := rpc.NewUserHandler(userUC, logger)

		userUC.EXPECT().ResolveCaller(mock.Anything, testCallerExtID, "").
			Return(nil, apperr.New(apperr.ErrInvalidArgument.Code, "user_id is required")).Once()
		// UpdatePreferredLanguage MUST NOT be called.

		ctx := authedCtx(testCallerExtID)
		req := connect.NewRequest(&userv1.UpdatePreferredLanguageRequest{
			UserId:            newUserIDProto(""), // empty
			PreferredLanguage: "en",
		})

		resp, err := h.UpdatePreferredLanguage(ctx, req)

		assert.Nil(t, resp)
		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})

	t.Run("Unauthenticated when no JWT claims", func(t *testing.T) {
		t.Parallel()
		logger, err := logging.New()
		require.NoError(t, err)
		userUC := mocks.NewMockUserUseCase(t)
		h := rpc.NewUserHandler(userUC, logger)

		ctx := context.Background() // no auth claims
		req := connect.NewRequest(&userv1.UpdatePreferredLanguageRequest{
			UserId:            newUserIDProto(testCallerUserID),
			PreferredLanguage: "ja",
		})

		resp, err := h.UpdatePreferredLanguage(ctx, req)

		assert.Nil(t, resp)
		var connectErr *connect.Error
		require.ErrorAs(t, err, &connectErr)
		assert.Equal(t, connect.CodeUnauthenticated, connectErr.Code())
	})

	t.Run("InvalidArgument when preferred_language is malformed", func(t *testing.T) {
		// Defense-in-depth: protovalidate already rejects empty / malformed
		// values at the wire boundary, but the handler also guards in case
		// the validation interceptor is bypassed (internal callers, test
		// harnesses, misconfigured chain). Table-driven so the full set
		// of invalid shapes is exercised — if the regex is ever loosened
		// (e.g. to accept uppercase), at least one case here will fail.
		//
		// The format check runs BEFORE ResolveCaller, so no auth-related
		// mocks are needed.
		t.Parallel()
		cases := []string{
			"",        // empty
			"e",       // too short
			"eng",     // too long
			"EN",      // uppercase
			"42",      // digits
			"ja-JP",   // region tag
			"english", // word
			" ja",     // whitespace
		}
		for _, bad := range cases {
			t.Run(bad, func(t *testing.T) {
				t.Parallel()
				logger, err := logging.New()
				require.NoError(t, err)
				userUC := mocks.NewMockUserUseCase(t)
				h := rpc.NewUserHandler(userUC, logger)
				// Neither ResolveCaller nor UpdatePreferredLanguage
				// must be called — format check should reject first.

				ctx := authedCtx(testCallerExtID)
				req := connect.NewRequest(&userv1.UpdatePreferredLanguageRequest{
					UserId:            newUserIDProto(testCallerUserID),
					PreferredLanguage: bad,
				})

				resp, err := h.UpdatePreferredLanguage(ctx, req)

				assert.Nil(t, resp)
				var connectErr *connect.Error
				require.ErrorAs(t, err, &connectErr)
				assert.Equal(t, connect.CodeInvalidArgument, connectErr.Code())
			})
		}
	})
}

func TestUserHandler_ResendEmailVerification(t *testing.T) {
	t.Parallel()

	// @spec components/adapter/fan/api/rpc/user "Caller resends their own email"
	t.Run("delegates to the use case and returns an empty response on success", func(t *testing.T) {
		t.Parallel()
		logger, err := logging.New()
		require.NoError(t, err)
		userUC := mocks.NewMockUserUseCase(t)
		h := rpc.NewUserHandler(userUC, logger)

		userUC.EXPECT().ResendEmailVerification(mock.Anything, testCallerExtID, testCallerUserID).Return(nil).Once()

		ctx := authedCtx(testCallerExtID)
		req := connect.NewRequest(&userv1.ResendEmailVerificationRequest{
			UserId: newUserIDProto(testCallerUserID),
		})

		resp, err := h.ResendEmailVerification(ctx, req)

		assert.NoError(t, err)
		assert.NotNil(t, resp)
	})

	// @spec components/adapter/fan/api/rpc/user "Usecase failure returned unchanged"
	t.Run("propagates the use case's error unchanged", func(t *testing.T) {
		// The use case owns the ownership check, the resend rate limit, and
		// the Zitadel call (see TestUserUseCase_ResendEmailVerification in
		// internal/usecase/user_uc_test.go); the handler only needs to
		// forward whatever code it returns.
		t.Parallel()

		cases := []struct {
			name    string
			ucErr   error
			wantErr error
		}{
			{"PermissionDenied on user_id mismatch", apperr.New(apperr.ErrPermissionDenied.Code, "user_id does not match authenticated user"), apperr.ErrPermissionDenied},
			{"InvalidArgument on empty user_id", apperr.New(apperr.ErrInvalidArgument.Code, "user_id is required"), apperr.ErrInvalidArgument},
			{"ResourceExhausted on rate limit", apperr.New(apperr.ErrResourceExhausted.Code, "resend rate limit exceeded"), apperr.ErrResourceExhausted},
			{"FailedPrecondition when already verified", apperr.New(apperr.ErrFailedPrecondition.Code, "email is already verified"), apperr.ErrFailedPrecondition},
			{"Unavailable when verifier is not configured", apperr.New(apperr.ErrUnavailable.Code, "email verification service is not configured"), apperr.ErrUnavailable},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				logger, err := logging.New()
				require.NoError(t, err)
				userUC := mocks.NewMockUserUseCase(t)
				h := rpc.NewUserHandler(userUC, logger)

				userUC.EXPECT().ResendEmailVerification(mock.Anything, testCallerExtID, testCallerUserID).
					Return(tc.ucErr).Once()

				ctx := authedCtx(testCallerExtID)
				req := connect.NewRequest(&userv1.ResendEmailVerificationRequest{
					UserId: newUserIDProto(testCallerUserID),
				})

				resp, err := h.ResendEmailVerification(ctx, req)

				assert.Nil(t, resp)
				assert.ErrorIs(t, err, tc.wantErr)
			})
		}
	})

	t.Run("Unauthenticated when no JWT claims", func(t *testing.T) {
		t.Parallel()
		logger, err := logging.New()
		require.NoError(t, err)
		userUC := mocks.NewMockUserUseCase(t)
		h := rpc.NewUserHandler(userUC, logger)
		// The use case MUST NOT be called.

		ctx := context.Background()
		req := connect.NewRequest(&userv1.ResendEmailVerificationRequest{
			UserId: newUserIDProto(testCallerUserID),
		})

		resp, err := h.ResendEmailVerification(ctx, req)

		assert.Nil(t, resp)
		assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	})
}
