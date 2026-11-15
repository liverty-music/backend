package usecase_test

import (
	"context"
	"testing"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/liverty-music/backend/internal/entity"
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
		links := ucmocks.NewMockPasskeyRegistrationCodes(t)
		remover := entitymocks.NewMockIdentityRemover(t)
		codesPort.EXPECT().IssueEmailVerificationCode(ctx, "sub-1").Return("fan@example.com", "ABC123", nil).Once()
		codesPort.EXPECT().VerifyEmail(ctx, "sub-1", "ABC123").Return(nil).Once()
		links.EXPECT().GetIdentityEmail(ctx, "sub-1").
			Return(&entity.IdentityEmail{Address: "fan@example.com", Verified: true, Organization: "org-1"}, nil).Once()
		links.EXPECT().CreatePasskeyRegistrationLink(ctx, "sub-1", "org-1").Return("https://link", nil).Once()
		remover.EXPECT().DeleteIdentity(ctx, "sub-1").Return(nil).Once()
		uc := usecase.NewIdentityUserUseCase(codesPort, links, remover)

		address, code, err := uc.IssueEmailVerificationCode(ctx, "sub-1")
		require.NoError(t, err)
		assert.Equal(t, "fan@example.com", address)
		assert.Equal(t, "ABC123", code)
		require.NoError(t, uc.VerifyEmail(ctx, "sub-1", "ABC123"))
		linkAddress, link, err := uc.IssuePasskeyRegistrationLink(ctx, "sub-1")
		require.NoError(t, err)
		assert.Equal(t, "fan@example.com", linkAddress)
		assert.Equal(t, "https://link", link)
		assert.NoError(t, uc.DeleteIdentity(ctx, "sub-1"))
	})

	t.Run("keeps the port's error code", func(t *testing.T) {
		t.Parallel()
		codesPort := ucmocks.NewMockEmailVerificationCodes(t)
		links := ucmocks.NewMockPasskeyRegistrationCodes(t)
		remover := entitymocks.NewMockIdentityRemover(t)
		codesPort.EXPECT().IssueEmailVerificationCode(ctx, "sub-1").Return("", "", apperr.New(codes.FailedPrecondition, "verified")).Once()
		codesPort.EXPECT().VerifyEmail(ctx, "sub-1", "bad").Return(apperr.New(codes.InvalidArgument, "bad code")).Once()
		links.EXPECT().GetIdentityEmail(ctx, "sub-1").Return(nil, apperr.New(codes.NotFound, "gone")).Once()
		remover.EXPECT().DeleteIdentity(ctx, "sub-1").Return(apperr.New(codes.Internal, "failed")).Once()
		uc := usecase.NewIdentityUserUseCase(codesPort, links, remover)

		_, _, err := uc.IssueEmailVerificationCode(ctx, "sub-1")
		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		assert.ErrorIs(t, uc.VerifyEmail(ctx, "sub-1", "bad"), apperr.ErrInvalidArgument)
		_, _, err = uc.IssuePasskeyRegistrationLink(ctx, "sub-1")
		assert.ErrorIs(t, err, apperr.ErrNotFound)
		assert.ErrorIs(t, uc.DeleteIdentity(ctx, "sub-1"), apperr.ErrInternal)
	})

	t.Run("issues no passkey registration link for an address that is not verified", func(t *testing.T) {
		t.Parallel()
		for _, email := range []*entity.IdentityEmail{
			{Address: "fan@example.com", Verified: false, Organization: "org-1"},
			{Address: "", Verified: true, Organization: "org-1"},
		} {
			links := ucmocks.NewMockPasskeyRegistrationCodes(t)
			links.EXPECT().GetIdentityEmail(ctx, "sub-1").Return(email, nil).Once()
			uc := usecase.NewIdentityUserUseCase(ucmocks.NewMockEmailVerificationCodes(t), links, entitymocks.NewMockIdentityRemover(t))

			_, _, err := uc.IssuePasskeyRegistrationLink(ctx, "sub-1")

			assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
			links.AssertNotCalled(t, "CreatePasskeyRegistrationLink", mock.Anything, mock.Anything, mock.Anything)
		}
	})
}

func TestIdentityOrganizationUseCase(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("passes each other call to the Organizations and returns the result", func(t *testing.T) {
		t.Parallel()
		orgs := ucmocks.NewMockIdentityOrganizations(t)
		orgs.EXPECT().Create(ctx, "xx-records").Return("org-1", nil).Once()
		orgs.EXPECT().GetByName(ctx, "xx-records").Return("org-1", nil).Once()
		orgs.EXPECT().DeactivateIdentities(ctx, "org-1").Return(nil).Once()
		orgs.EXPECT().Delete(ctx, "org-1").Return(nil).Once()
		uc := usecase.NewIdentityOrganizationUseCase(orgs)

		id, err := uc.Create(ctx, "xx-records")
		require.NoError(t, err)
		assert.Equal(t, "org-1", id)
		id, err = uc.GetByName(ctx, "xx-records")
		require.NoError(t, err)
		assert.Equal(t, "org-1", id)
		require.NoError(t, uc.DeactivateIdentities(ctx, "org-1"))
		assert.NoError(t, uc.Delete(ctx, "org-1"))
	})

	t.Run("keeps the Organizations' error code", func(t *testing.T) {
		t.Parallel()
		orgs := ucmocks.NewMockIdentityOrganizations(t)
		orgs.EXPECT().Create(ctx, "xx-records").Return("", apperr.New(codes.AlreadyExists, "taken")).Once()
		orgs.EXPECT().GetByName(ctx, "yy-records").Return("", apperr.New(codes.NotFound, "none")).Once()
		uc := usecase.NewIdentityOrganizationUseCase(orgs)

		_, err := uc.Create(ctx, "xx-records")
		assert.ErrorIs(t, err, apperr.ErrAlreadyExists)
		_, err = uc.GetByName(ctx, "yy-records")
		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})

	t.Run("provisions in order: login policy, project grant, owner identity, owner grant", func(t *testing.T) {
		t.Parallel()
		orgs := ucmocks.NewMockIdentityOrganizations(t)
		var steps []string
		orgs.EXPECT().EnsurePasskeyLoginPolicy(ctx, "org-1").
			RunAndReturn(func(context.Context, string) error { steps = append(steps, "policy"); return nil }).Once()
		orgs.EXPECT().EnsureConsoleProjectGrant(ctx, "org-1").
			RunAndReturn(func(context.Context, string) error { steps = append(steps, "project grant"); return nil }).Once()
		orgs.EXPECT().EnsureOwnerIdentity(ctx, "org-1", "xx-records", "band@example.com").
			RunAndReturn(func(context.Context, string, string, string) (string, error) {
				steps = append(steps, "owner identity")
				return "owner-1", nil
			}).Once()
		orgs.EXPECT().EnsureOwnerGrant(ctx, "org-1", "owner-1").
			RunAndReturn(func(context.Context, string, string) error { steps = append(steps, "owner grant"); return nil }).Once()
		uc := usecase.NewIdentityOrganizationUseCase(orgs)

		require.NoError(t, uc.Provision(ctx, "org-1", "xx-records", "band@example.com"))
		assert.Equal(t, []string{"policy", "project grant", "owner identity", "owner grant"}, steps)
	})

	t.Run("an owner identity the identity provider rejects stops provisioning with its code", func(t *testing.T) {
		t.Parallel()
		orgs := ucmocks.NewMockIdentityOrganizations(t)
		orgs.EXPECT().EnsurePasskeyLoginPolicy(ctx, "org-1").Return(nil).Once()
		orgs.EXPECT().EnsureConsoleProjectGrant(ctx, "org-1").Return(nil).Once()
		orgs.EXPECT().EnsureOwnerIdentity(ctx, "org-1", "xx-records", "band@example.com").
			Return("", apperr.New(codes.AlreadyExists, "user name taken")).Once()
		uc := usecase.NewIdentityOrganizationUseCase(orgs)

		err := uc.Provision(ctx, "org-1", "xx-records", "band@example.com")

		assert.ErrorIs(t, err, apperr.ErrAlreadyExists)
		orgs.AssertNotCalled(t, "EnsureOwnerGrant", mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("a failed step stops provisioning", func(t *testing.T) {
		t.Parallel()
		orgs := ucmocks.NewMockIdentityOrganizations(t)
		orgs.EXPECT().EnsurePasskeyLoginPolicy(ctx, "org-1").Return(apperr.New(codes.Internal, "policy")).Once()
		uc := usecase.NewIdentityOrganizationUseCase(orgs)

		err := uc.Provision(ctx, "org-1", "xx-records", "band@example.com")

		assert.ErrorIs(t, err, apperr.ErrInternal)
	})

	t.Run("an Organizer without a short name is not provisioned", func(t *testing.T) {
		t.Parallel()
		orgs := ucmocks.NewMockIdentityOrganizations(t)
		uc := usecase.NewIdentityOrganizationUseCase(orgs)

		err := uc.Provision(ctx, "org-1", "", "band@example.com")

		assert.ErrorIs(t, err, apperr.ErrInternal)
	})
}
