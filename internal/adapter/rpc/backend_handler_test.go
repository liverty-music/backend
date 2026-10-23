package rpc_test

import (
	"context"
	"testing"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	backendorganizationv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/backend/organization/v1"
	backenduserv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/backend/user/v1"
	"github.com/liverty-music/backend/internal/adapter/rpc"
	"github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// fakePasskeyLinks is an identity provider that issues one fixed link, or
// fails with err.
type fakePasskeyLinks struct {
	err   error
	calls []string
}

func (f *fakePasskeyLinks) IssuePasskeyRegistrationLink(_ context.Context, providerUserID string) (string, string, error) {
	f.calls = append(f.calls, providerUserID)
	if f.err != nil {
		return "", "", f.err
	}
	return "fan@example.com", "https://liverty-music.app/passkey/register?code=one-time", nil
}

var backendUserID = &entityv1.User_ProviderUserId{Value: "zitadel-user-1"}

func TestBackendUserHandler(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("SendEmailVerification sends the code for the identity", func(t *testing.T) {
		t.Parallel()
		ev := mocks.NewMockEmailVerifier(t)
		ev.EXPECT().SendVerification(mock.Anything, "zitadel-user-1").Return(nil).Once()

		_, err := rpc.NewBackendUserHandler(ev, nil, nil).SendEmailVerification(ctx, &backenduserv1.SendEmailVerificationRequest{ProviderUserId: backendUserID})

		assert.NoError(t, err)
	})

	t.Run("SendEmailVerification keeps Unavailable", func(t *testing.T) {
		t.Parallel()
		ev := mocks.NewMockEmailVerifier(t)
		ev.EXPECT().SendVerification(mock.Anything, "zitadel-user-1").Return(apperr.New(codes.Unavailable, "down")).Once()

		_, err := rpc.NewBackendUserHandler(ev, nil, nil).SendEmailVerification(ctx, &backenduserv1.SendEmailVerificationRequest{ProviderUserId: backendUserID})

		assert.ErrorIs(t, err, apperr.ErrUnavailable)
	})

	t.Run("ResendEmailVerification keeps FailedPrecondition for a verified address", func(t *testing.T) {
		t.Parallel()
		ev := mocks.NewMockEmailVerifier(t)
		ev.EXPECT().ResendVerification(mock.Anything, "zitadel-user-1").Return(apperr.New(codes.FailedPrecondition, "verified")).Once()

		_, err := rpc.NewBackendUserHandler(ev, nil, nil).ResendEmailVerification(ctx, &backenduserv1.ResendEmailVerificationRequest{ProviderUserId: backendUserID})

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("IssuePasskeyRegistrationLink returns the verified address and the link", func(t *testing.T) {
		t.Parallel()
		links := &fakePasskeyLinks{}

		resp, err := rpc.NewBackendUserHandler(nil, nil, links).IssuePasskeyRegistrationLink(ctx, &backenduserv1.IssuePasskeyRegistrationLinkRequest{ProviderUserId: backendUserID})

		require.NoError(t, err)
		assert.Equal(t, "fan@example.com", resp.GetEmail().GetValue())
		assert.Equal(t, "https://liverty-music.app/passkey/register?code=one-time", resp.GetLink().GetValue())
		assert.Equal(t, []string{"zitadel-user-1"}, links.calls)
	})

	t.Run("IssuePasskeyRegistrationLink keeps NotFound and FailedPrecondition", func(t *testing.T) {
		t.Parallel()
		for _, want := range []*apperr.AppErr{apperr.ErrNotFound, apperr.ErrFailedPrecondition} {
			links := &fakePasskeyLinks{err: apperr.New(want.Code, "refused")}

			_, err := rpc.NewBackendUserHandler(nil, nil, links).IssuePasskeyRegistrationLink(ctx, &backenduserv1.IssuePasskeyRegistrationLinkRequest{ProviderUserId: backendUserID})

			assert.ErrorIs(t, err, want)
		}
	})

	t.Run("DeleteIdentity removes the identity", func(t *testing.T) {
		t.Parallel()
		ir := mocks.NewMockIdentityRemover(t)
		ir.EXPECT().DeleteIdentity(mock.Anything, "zitadel-user-1").Return(nil).Once()

		_, err := rpc.NewBackendUserHandler(nil, ir, nil).DeleteIdentity(ctx, &backenduserv1.DeleteIdentityRequest{ProviderUserId: backendUserID})

		assert.NoError(t, err)
	})
}

func TestBackendOrganizationHandler(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	shortName := &entityv1.OrganizerShortName{Value: "xx-records"}
	orgID := &entityv1.Organizer_ProviderOrganizerId{Value: "org-1"}

	t.Run("Create returns the new Organization's id", func(t *testing.T) {
		t.Parallel()
		orgs := mocks.NewMockOrganizationRepository(t)
		orgs.EXPECT().Create(mock.Anything, "xx-records").Return("org-1", nil).Once()

		resp, err := rpc.NewBackendOrganizationHandler(orgs).Create(ctx, &backendorganizationv1.CreateRequest{ShortName: shortName})

		require.NoError(t, err)
		assert.Equal(t, "org-1", resp.GetProviderOrganizerId().GetValue())
	})

	t.Run("Create keeps AlreadyExists", func(t *testing.T) {
		t.Parallel()
		orgs := mocks.NewMockOrganizationRepository(t)
		orgs.EXPECT().Create(mock.Anything, "xx-records").Return("", apperr.New(codes.AlreadyExists, "taken")).Once()

		_, err := rpc.NewBackendOrganizationHandler(orgs).Create(ctx, &backendorganizationv1.CreateRequest{ShortName: shortName})

		assert.ErrorIs(t, err, apperr.ErrAlreadyExists)
	})

	t.Run("Get finds the Organization by its name and keeps NotFound", func(t *testing.T) {
		t.Parallel()
		orgs := mocks.NewMockOrganizationRepository(t)
		orgs.EXPECT().GetByName(mock.Anything, "xx-records").Return("org-1", nil).Once()
		orgs.EXPECT().GetByName(mock.Anything, "yy-records").Return("", apperr.New(codes.NotFound, "none")).Once()
		h := rpc.NewBackendOrganizationHandler(orgs)

		resp, err := h.Get(ctx, &backendorganizationv1.GetRequest{ShortName: shortName})
		require.NoError(t, err)
		assert.Equal(t, "org-1", resp.GetProviderOrganizerId().GetValue())

		_, err = h.Get(ctx, &backendorganizationv1.GetRequest{ShortName: &entityv1.OrganizerShortName{Value: "yy-records"}})
		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})

	t.Run("Provision passes the Organization, short name and owner email", func(t *testing.T) {
		t.Parallel()
		orgs := mocks.NewMockOrganizationRepository(t)
		orgs.EXPECT().Provision(mock.Anything, "org-1", "xx-records", "band@example.com").Return(nil).Once()

		_, err := rpc.NewBackendOrganizationHandler(orgs).Provision(ctx, &backendorganizationv1.ProvisionRequest{
			ProviderOrganizerId: orgID,
			ShortName:           shortName,
			OwnerEmail:          &entityv1.Email{Value: "band@example.com"},
		})

		assert.NoError(t, err)
	})

	t.Run("DeactivateIdentities and Delete act on the Organization", func(t *testing.T) {
		t.Parallel()
		orgs := mocks.NewMockOrganizationRepository(t)
		orgs.EXPECT().DeactivateIdentities(mock.Anything, "org-1").Return(nil).Once()
		orgs.EXPECT().Delete(mock.Anything, "org-1").Return(nil).Once()
		h := rpc.NewBackendOrganizationHandler(orgs)

		_, err := h.DeactivateIdentities(ctx, &backendorganizationv1.DeactivateIdentitiesRequest{ProviderOrganizerId: orgID})
		require.NoError(t, err)
		_, err = h.Delete(ctx, &backendorganizationv1.DeleteRequest{ProviderOrganizerId: orgID})
		assert.NoError(t, err)
	})
}
