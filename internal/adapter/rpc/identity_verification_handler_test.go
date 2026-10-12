package rpc_test

import (
	"context"
	"testing"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	identityverificationv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/identity_verification/v1"
	"connectrpc.com/connect"
	"github.com/liverty-music/backend/internal/adapter/rpc"
	"github.com/liverty-music/backend/internal/entity"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// newIdentityVerificationHandler creates a IdentityVerificationHandler wired with the provided mocks.
func newIdentityVerificationHandler(t *testing.T, identityVerificationUC *ucmocks.MockIdentityVerificationUseCase, userUC *ucmocks.MockUserUseCase) *rpc.IdentityVerificationHandler {
	t.Helper()
	logger, err := logging.New()
	require.NoError(t, err)
	return rpc.NewIdentityVerificationHandler(identityVerificationUC, userUC, logger)
}

// expectCaller makes userUC resolve testCallerExtID to testCallerUserID, or
// fail with NotFound when found is false.
func expectCaller(userUC *ucmocks.MockUserUseCase, found bool) {
	if found {
		userUC.EXPECT().ResolveCaller(mock.Anything, testCallerExtID).
			Return(&entity.User{ID: testCallerUserID, ProviderUserID: testCallerExtID}, nil).Once()
		return
	}
	userUC.EXPECT().ResolveCaller(mock.Anything, testCallerExtID).
		Return(nil, apperr.New(codes.NotFound, "user not found")).Once()
}

// sampleIdentityVerification returns a minimal IdentityVerification of the caller with the given status.
func sampleIdentityVerification(status entity.VerificationStatus) *entity.IdentityVerification {
	return &entity.IdentityVerification{
		ID:             "identity verification-1",
		UserID:         testCallerUserID,
		Method:         entity.VerificationMethodJPKI,
		ProviderUserID: "ps-1",
		DedupeStrength: entity.DedupeStrengthStrong,
		Status:         status,
	}
}

func TestIdentityVerificationHandler_Start(t *testing.T) {
	t.Parallel()

	t.Run("return the session for the caller", func(t *testing.T) {
		t.Parallel()
		identityVerificationUC, userUC := ucmocks.NewMockIdentityVerificationUseCase(t), ucmocks.NewMockUserUseCase(t)
		expectCaller(userUC, true)
		identityVerificationUC.EXPECT().Start(mock.Anything, testCallerUserID, entity.VerificationMethodJPKI).
			Return("sess-abc", "https://stamp.p8n.app/redirect/sess-abc", nil).Once()

		resp, err := newIdentityVerificationHandler(t, identityVerificationUC, userUC).Start(authedCtx(testCallerExtID), connect.NewRequest(&identityverificationv1.StartRequest{
			Method: entityv1.VerificationMethod_VERIFICATION_METHOD_JPKI,
		}))

		require.NoError(t, err)
		assert.Equal(t, "sess-abc", resp.Msg.GetSessionId())
		assert.Equal(t, "https://stamp.p8n.app/redirect/sess-abc", resp.Msg.GetRedirectUrl().GetValue())
	})

	t.Run("return UNAUTHENTICATED without a sign-in", func(t *testing.T) {
		t.Parallel()
		identityVerificationUC, userUC := ucmocks.NewMockIdentityVerificationUseCase(t), ucmocks.NewMockUserUseCase(t)

		_, err := newIdentityVerificationHandler(t, identityVerificationUC, userUC).Start(context.Background(), connect.NewRequest(&identityverificationv1.StartRequest{
			Method: entityv1.VerificationMethod_VERIFICATION_METHOD_JPKI,
		}))

		assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	})

	t.Run("return UNAUTHENTICATED when the caller has no account", func(t *testing.T) {
		t.Parallel()
		identityVerificationUC, userUC := ucmocks.NewMockIdentityVerificationUseCase(t), ucmocks.NewMockUserUseCase(t)
		expectCaller(userUC, false)

		_, err := newIdentityVerificationHandler(t, identityVerificationUC, userUC).Start(authedCtx(testCallerExtID), connect.NewRequest(&identityverificationv1.StartRequest{
			Method: entityv1.VerificationMethod_VERIFICATION_METHOD_JPKI,
		}))

		assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	})

	t.Run("return INVALID_ARGUMENT for an unspecified method", func(t *testing.T) {
		t.Parallel()
		identityVerificationUC, userUC := ucmocks.NewMockIdentityVerificationUseCase(t), ucmocks.NewMockUserUseCase(t)
		expectCaller(userUC, true)

		_, err := newIdentityVerificationHandler(t, identityVerificationUC, userUC).Start(authedCtx(testCallerExtID), connect.NewRequest(&identityverificationv1.StartRequest{}))

		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	})
}

func TestIdentityVerificationHandler_Create(t *testing.T) {
	t.Parallel()

	t.Run("return the identity verification for an active identity verification", func(t *testing.T) {
		t.Parallel()
		identityVerificationUC, userUC := ucmocks.NewMockIdentityVerificationUseCase(t), ucmocks.NewMockUserUseCase(t)
		expectCaller(userUC, true)
		identityVerificationUC.EXPECT().Create(mock.Anything, testCallerUserID, "sess-abc").
			Return(sampleIdentityVerification(entity.VerificationStatusActive), nil).Once()

		resp, err := newIdentityVerificationHandler(t, identityVerificationUC, userUC).Create(authedCtx(testCallerExtID), connect.NewRequest(&identityverificationv1.CreateRequest{
			SessionId: "sess-abc",
		}))

		require.NoError(t, err)
		assert.Equal(t, "identity verification-1", resp.Msg.GetIdentityVerification().GetId().GetValue())
		assert.Equal(t, testCallerUserID, resp.Msg.GetIdentityVerification().GetUserId().GetValue())
	})

	t.Run("return the usecase failure unchanged", func(t *testing.T) {
		t.Parallel()
		identityVerificationUC, userUC := ucmocks.NewMockIdentityVerificationUseCase(t), ucmocks.NewMockUserUseCase(t)
		expectCaller(userUC, true)
		identityVerificationUC.EXPECT().Create(mock.Anything, testCallerUserID, "sess-abc").
			Return(nil, apperr.New(codes.AlreadyExists, "bound to another account")).Once()

		_, err := newIdentityVerificationHandler(t, identityVerificationUC, userUC).Create(authedCtx(testCallerExtID), connect.NewRequest(&identityverificationv1.CreateRequest{
			SessionId: "sess-abc",
		}))

		assert.ErrorIs(t, err, apperr.ErrAlreadyExists)
	})
}

func TestIdentityVerificationHandler_Recheck(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		status     entity.VerificationStatus
		wantStatus entityv1.VerificationStatus
	}{
		{
			name:       "return ACTIVE for a passing re-check",
			status:     entity.VerificationStatusActive,
			wantStatus: entityv1.VerificationStatus_VERIFICATION_STATUS_ACTIVE,
		},
		{
			name:       "return NEEDS_REVERIFICATION for a failed re-check",
			status:     entity.VerificationStatusNeedsReverification,
			wantStatus: entityv1.VerificationStatus_VERIFICATION_STATUS_NEEDS_REVERIFICATION,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			identityVerificationUC, userUC := ucmocks.NewMockIdentityVerificationUseCase(t), ucmocks.NewMockUserUseCase(t)
			expectCaller(userUC, true)
			identityVerificationUC.EXPECT().Recheck(mock.Anything, testCallerUserID).Return(sampleIdentityVerification(tt.status), nil).Once()

			resp, err := newIdentityVerificationHandler(t, identityVerificationUC, userUC).Recheck(authedCtx(testCallerExtID), connect.NewRequest(&identityverificationv1.RecheckRequest{}))

			require.NoError(t, err)
			assert.Equal(t, tt.wantStatus, resp.Msg.GetStatus())
		})
	}

	t.Run("return INTERNAL when the usecase returns no identity verification without an error", func(t *testing.T) {
		t.Parallel()
		identityVerificationUC, userUC := ucmocks.NewMockIdentityVerificationUseCase(t), ucmocks.NewMockUserUseCase(t)
		expectCaller(userUC, true)
		identityVerificationUC.EXPECT().Recheck(mock.Anything, testCallerUserID).Return(nil, nil).Once()

		_, err := newIdentityVerificationHandler(t, identityVerificationUC, userUC).Recheck(authedCtx(testCallerExtID), connect.NewRequest(&identityverificationv1.RecheckRequest{}))

		assert.Equal(t, connect.CodeInternal, connect.CodeOf(err))
	})
}

func TestIdentityVerificationHandler_Get(t *testing.T) {
	t.Parallel()

	t.Run("return the caller's identity verification whatever its status", func(t *testing.T) {
		t.Parallel()
		identityVerificationUC, userUC := ucmocks.NewMockIdentityVerificationUseCase(t), ucmocks.NewMockUserUseCase(t)
		expectCaller(userUC, true)
		identityVerificationUC.EXPECT().Get(mock.Anything, testCallerUserID).
			Return(sampleIdentityVerification(entity.VerificationStatusNeedsReverification), nil).Once()

		resp, err := newIdentityVerificationHandler(t, identityVerificationUC, userUC).Get(authedCtx(testCallerExtID), connect.NewRequest(&identityverificationv1.GetRequest{}))

		require.NoError(t, err)
		assert.Equal(t, "identity verification-1", resp.Msg.GetIdentityVerification().GetId().GetValue())
		assert.Equal(t, entityv1.VerificationStatus_VERIFICATION_STATUS_NEEDS_REVERIFICATION, resp.Msg.GetIdentityVerification().GetStatus())
	})

	t.Run("return NOT_FOUND for a fan who never verified their identity", func(t *testing.T) {
		t.Parallel()
		identityVerificationUC, userUC := ucmocks.NewMockIdentityVerificationUseCase(t), ucmocks.NewMockUserUseCase(t)
		expectCaller(userUC, true)
		identityVerificationUC.EXPECT().Get(mock.Anything, testCallerUserID).
			Return(nil, apperr.New(codes.NotFound, "no identity verification")).Once()

		_, err := newIdentityVerificationHandler(t, identityVerificationUC, userUC).Get(authedCtx(testCallerExtID), connect.NewRequest(&identityverificationv1.GetRequest{}))

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})
}
