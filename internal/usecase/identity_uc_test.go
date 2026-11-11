package usecase_test

import (
	"context"
	"testing"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	entitymocks "github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/usecase"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
)

func TestIdentityUserUseCase(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("passes each call to its port and returns the result", func(t *testing.T) {
		t.Parallel()
		codesPort := ucmocks.NewMockEmailVerificationCodes(t)
		links := ucmocks.NewMockPasskeyRegistrationLinkIssuer(t)
		remover := entitymocks.NewMockIdentityRemover(t)
		codesPort.EXPECT().IssueEmailVerificationCode(ctx, "sub-1").Return("fan@example.com", "ABC123", nil).Once()
		codesPort.EXPECT().VerifyEmail(ctx, "sub-1", "ABC123").Return(nil).Once()
		links.EXPECT().IssuePasskeyRegistrationLink(ctx, "sub-1").Return("fan@example.com", "https://link", nil).Once()
		remover.EXPECT().DeleteIdentity(ctx, "sub-1").Return(nil).Once()
		uc := usecase.NewIdentityUserUseCase(codesPort, links, remover)

		address, code, err := uc.IssueEmailVerificationCode(ctx, "sub-1")
		require.NoError(t, err)
		assert.Equal(t, "fan@example.com", address)
		assert.Equal(t, "ABC123", code)
		require.NoError(t, uc.VerifyEmail(ctx, "sub-1", "ABC123"))
		_, link, err := uc.IssuePasskeyRegistrationLink(ctx, "sub-1")
		require.NoError(t, err)
		assert.Equal(t, "https://link", link)
		assert.NoError(t, uc.DeleteIdentity(ctx, "sub-1"))
	})

	t.Run("keeps the port's error code", func(t *testing.T) {
		t.Parallel()
		codesPort := ucmocks.NewMockEmailVerificationCodes(t)
		links := ucmocks.NewMockPasskeyRegistrationLinkIssuer(t)
		remover := entitymocks.NewMockIdentityRemover(t)
		codesPort.EXPECT().IssueEmailVerificationCode(ctx, "sub-1").Return("", "", apperr.New(codes.FailedPrecondition, "verified")).Once()
		codesPort.EXPECT().VerifyEmail(ctx, "sub-1", "bad").Return(apperr.New(codes.InvalidArgument, "bad code")).Once()
		links.EXPECT().IssuePasskeyRegistrationLink(ctx, "sub-1").Return("", "", apperr.New(codes.NotFound, "gone")).Once()
		remover.EXPECT().DeleteIdentity(ctx, "sub-1").Return(apperr.New(codes.Internal, "failed")).Once()
		uc := usecase.NewIdentityUserUseCase(codesPort, links, remover)

		_, _, err := uc.IssueEmailVerificationCode(ctx, "sub-1")
		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		assert.ErrorIs(t, uc.VerifyEmail(ctx, "sub-1", "bad"), apperr.ErrInvalidArgument)
		_, _, err = uc.IssuePasskeyRegistrationLink(ctx, "sub-1")
		assert.ErrorIs(t, err, apperr.ErrNotFound)
		assert.ErrorIs(t, uc.DeleteIdentity(ctx, "sub-1"), apperr.ErrInternal)
	})
}

func TestIdentityOrganizationUseCase(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("passes each call to the Organizations and returns the result", func(t *testing.T) {
		t.Parallel()
		orgs := entitymocks.NewMockOrganizationRepository(t)
		orgs.EXPECT().Create(ctx, "xx-records").Return("org-1", nil).Once()
		orgs.EXPECT().GetByName(ctx, "xx-records").Return("org-1", nil).Once()
		orgs.EXPECT().Provision(ctx, "org-1", "xx-records", "band@example.com").Return(nil).Once()
		orgs.EXPECT().DeactivateIdentities(ctx, "org-1").Return(nil).Once()
		orgs.EXPECT().Delete(ctx, "org-1").Return(nil).Once()
		uc := usecase.NewIdentityOrganizationUseCase(orgs)

		id, err := uc.Create(ctx, "xx-records")
		require.NoError(t, err)
		assert.Equal(t, "org-1", id)
		id, err = uc.GetByName(ctx, "xx-records")
		require.NoError(t, err)
		assert.Equal(t, "org-1", id)
		require.NoError(t, uc.Provision(ctx, "org-1", "xx-records", "band@example.com"))
		require.NoError(t, uc.DeactivateIdentities(ctx, "org-1"))
		assert.NoError(t, uc.Delete(ctx, "org-1"))
	})

	t.Run("keeps the Organizations' error code", func(t *testing.T) {
		t.Parallel()
		orgs := entitymocks.NewMockOrganizationRepository(t)
		orgs.EXPECT().Create(ctx, "xx-records").Return("", apperr.New(codes.AlreadyExists, "taken")).Once()
		orgs.EXPECT().GetByName(ctx, "yy-records").Return("", apperr.New(codes.NotFound, "none")).Once()
		uc := usecase.NewIdentityOrganizationUseCase(orgs)

		_, err := uc.Create(ctx, "xx-records")
		assert.ErrorIs(t, err, apperr.ErrAlreadyExists)
		_, err = uc.GetByName(ctx, "yy-records")
		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})
}
