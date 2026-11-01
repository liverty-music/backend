package usecase_test

import (
	"context"
	"errors"
	"testing"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase"
)

// retryingFlow runs a step again while it fails with Unavailable, as the
// workflow runtime does, and counts the tries.
type retryingFlow struct {
	inlineFlow
	tries int
}

func (f *retryingFlow) Step(ctx context.Context, _ string, fn func(context.Context) ([]byte, error)) ([]byte, error) {
	for {
		f.tries++
		out, err := fn(ctx)
		if !errors.Is(err, apperr.ErrUnavailable) {
			return out, err
		}
	}
}

func TestUserUseCase_RunReissueEmailVerification(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	user := &entity.User{ID: testResolveUserID, ProviderUserID: testResolveExtID}

	t.Run("sends a fresh code to the User of the request", func(t *testing.T) {
		t.Parallel()
		d := newUserTestDeps(t)
		d.repo.EXPECT().Get(mock.Anything, testResolveUserID).Return(user, nil).Once()
		d.emailVerifier.EXPECT().ResendVerification(mock.Anything, user).Return(nil).Once()

		_, err := d.uc.RunReissueEmailVerification(ctx, &inlineFlow{}, usecase.UserRequest{UserID: testResolveUserID})

		require.NoError(t, err)
	})

	// @spec components/usecase/user/reissue-email-verification "Identity provider down"
	t.Run("tries the step again and sends once the identity provider answers", func(t *testing.T) {
		t.Parallel()
		d := newUserTestDeps(t)
		d.repo.EXPECT().Get(mock.Anything, testResolveUserID).Return(user, nil).Twice()
		d.emailVerifier.EXPECT().ResendVerification(mock.Anything, user).
			Return(apperr.New(codes.Unavailable, "identity service unavailable")).Once()
		d.emailVerifier.EXPECT().ResendVerification(mock.Anything, user).Return(nil).Once()
		f := &retryingFlow{}

		_, err := d.uc.RunReissueEmailVerification(ctx, f, usecase.UserRequest{UserID: testResolveUserID})

		require.NoError(t, err)
		assert.Equal(t, 2, f.tries)
	})

	t.Run("ends with a refusal of the identity provider", func(t *testing.T) {
		t.Parallel()
		d := newUserTestDeps(t)
		d.repo.EXPECT().Get(mock.Anything, testResolveUserID).Return(user, nil).Once()
		d.emailVerifier.EXPECT().ResendVerification(mock.Anything, user).
			Return(apperr.New(codes.Internal, "identity provider refused")).Once()

		_, err := d.uc.RunReissueEmailVerification(ctx, &retryingFlow{}, usecase.UserRequest{UserID: testResolveUserID})

		assert.ErrorIs(t, err, apperr.ErrInternal)
	})
}

func TestUserUseCase_RunSendPasskeyRecoveryLink(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// @spec components/usecase/user/send-passkey-recovery-link "Fan with a verified address"
	t.Run("sends one registration link to the User of the request", func(t *testing.T) {
		t.Parallel()
		d := newUserPasskeyTestDeps(t)
		d.repo.EXPECT().Get(mock.Anything, "user-1").Return(passkeyFan, nil).Once()
		d.links.EXPECT().SendPasskeyRegistrationLink(mock.Anything, passkeyFan).Return(nil).Once()

		_, err := d.uc.RunSendPasskeyRecoveryLink(ctx, &inlineFlow{}, usecase.UserRequest{UserID: "user-1"})

		require.NoError(t, err)
	})

	// @spec components/usecase/user/send-passkey-recovery-link "Mail service down"
	t.Run("tries the step again and sends once the mail service answers", func(t *testing.T) {
		t.Parallel()
		d := newUserPasskeyTestDeps(t)
		d.repo.EXPECT().Get(mock.Anything, "user-1").Return(passkeyFan, nil).Twice()
		d.links.EXPECT().SendPasskeyRegistrationLink(mock.Anything, passkeyFan).
			Return(apperr.New(codes.Unavailable, "postmark down")).Once()
		d.links.EXPECT().SendPasskeyRegistrationLink(mock.Anything, passkeyFan).Return(nil).Once()
		f := &retryingFlow{}

		_, err := d.uc.RunSendPasskeyRecoveryLink(ctx, f, usecase.UserRequest{UserID: "user-1"})

		require.NoError(t, err)
		assert.Equal(t, 2, f.tries)
	})

	t.Run("ends with a refusal of the mail service", func(t *testing.T) {
		t.Parallel()
		d := newUserPasskeyTestDeps(t)
		d.repo.EXPECT().Get(mock.Anything, "user-1").Return(passkeyFan, nil).Once()
		d.links.EXPECT().SendPasskeyRegistrationLink(mock.Anything, passkeyFan).
			Return(apperr.New(codes.Internal, "postmark refused")).Once()

		_, err := d.uc.RunSendPasskeyRecoveryLink(ctx, &retryingFlow{}, usecase.UserRequest{UserID: "user-1"})

		assert.ErrorIs(t, err, apperr.ErrInternal)
	})
}
