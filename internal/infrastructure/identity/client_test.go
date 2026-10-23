package identity_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	backendorganizationv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/backend/organization/v1/organizationv1connect"
	backenduserv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/backend/user/v1/userv1connect"
	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	backendorganizationv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/backend/organization/v1"
	backenduserv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/backend/user/v1"
	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/infrastructure/identity"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// fakeIdentityService records each call and answers with err when set.
type fakeIdentityService struct {
	mu    sync.Mutex
	err   *connect.Error
	calls []string // "<procedure> <id>"
}

func (f *fakeIdentityService) record(procedure, id string) *connect.Error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, procedure+" "+id)
	return f.err
}

func (f *fakeIdentityService) recorded() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func (f *fakeIdentityService) SendEmailVerification(_ context.Context, req *backenduserv1.SendEmailVerificationRequest) (*backenduserv1.SendEmailVerificationResponse, error) {
	if err := f.record("SendEmailVerification", req.GetProviderUserId().GetValue()); err != nil {
		return nil, err
	}
	return &backenduserv1.SendEmailVerificationResponse{}, nil
}

func (f *fakeIdentityService) ResendEmailVerification(_ context.Context, req *backenduserv1.ResendEmailVerificationRequest) (*backenduserv1.ResendEmailVerificationResponse, error) {
	if err := f.record("ResendEmailVerification", req.GetProviderUserId().GetValue()); err != nil {
		return nil, err
	}
	return &backenduserv1.ResendEmailVerificationResponse{}, nil
}

func (f *fakeIdentityService) IssuePasskeyRegistrationLink(_ context.Context, req *backenduserv1.IssuePasskeyRegistrationLinkRequest) (*backenduserv1.IssuePasskeyRegistrationLinkResponse, error) {
	if err := f.record("IssuePasskeyRegistrationLink", req.GetProviderUserId().GetValue()); err != nil {
		return nil, err
	}
	return &backenduserv1.IssuePasskeyRegistrationLinkResponse{
		Email: &entityv1.Email{Value: "fan@example.com"},
		Link:  &entityv1.Url{Value: "https://liverty-music.app/passkey/register?code=one-time"},
	}, nil
}

func (f *fakeIdentityService) DeleteIdentity(_ context.Context, req *backenduserv1.DeleteIdentityRequest) (*backenduserv1.DeleteIdentityResponse, error) {
	if err := f.record("DeleteIdentity", req.GetProviderUserId().GetValue()); err != nil {
		return nil, err
	}
	return &backenduserv1.DeleteIdentityResponse{}, nil
}

func (f *fakeIdentityService) Create(_ context.Context, req *backendorganizationv1.CreateRequest) (*backendorganizationv1.CreateResponse, error) {
	if err := f.record("Create", req.GetShortName().GetValue()); err != nil {
		return nil, err
	}
	return &backendorganizationv1.CreateResponse{ProviderOrganizerId: &entityv1.Organizer_ProviderOrganizerId{Value: "org-1"}}, nil
}

func (f *fakeIdentityService) Get(_ context.Context, req *backendorganizationv1.GetRequest) (*backendorganizationv1.GetResponse, error) {
	if err := f.record("Get", req.GetShortName().GetValue()); err != nil {
		return nil, err
	}
	return &backendorganizationv1.GetResponse{ProviderOrganizerId: &entityv1.Organizer_ProviderOrganizerId{Value: "org-1"}}, nil
}

func (f *fakeIdentityService) Provision(_ context.Context, req *backendorganizationv1.ProvisionRequest) (*backendorganizationv1.ProvisionResponse, error) {
	id := req.GetProviderOrganizerId().GetValue() + " " + req.GetShortName().GetValue() + " " + req.GetOwnerEmail().GetValue()
	if err := f.record("Provision", id); err != nil {
		return nil, err
	}
	return &backendorganizationv1.ProvisionResponse{}, nil
}

func (f *fakeIdentityService) DeactivateIdentities(_ context.Context, req *backendorganizationv1.DeactivateIdentitiesRequest) (*backendorganizationv1.DeactivateIdentitiesResponse, error) {
	if err := f.record("DeactivateIdentities", req.GetProviderOrganizerId().GetValue()); err != nil {
		return nil, err
	}
	return &backendorganizationv1.DeactivateIdentitiesResponse{}, nil
}

func (f *fakeIdentityService) Delete(_ context.Context, req *backendorganizationv1.DeleteRequest) (*backendorganizationv1.DeleteResponse, error) {
	if err := f.record("Delete", req.GetProviderOrganizerId().GetValue()); err != nil {
		return nil, err
	}
	return &backendorganizationv1.DeleteResponse{}, nil
}

// startIdentityService serves f over HTTP/2 without TLS, as the identity
// service does, and returns its base URL.
func startIdentityService(t *testing.T, f *fakeIdentityService) string {
	t.Helper()
	s := connect.NewServer()
	backenduserv1connect.RegisterUserServiceHandler(s, f)
	backendorganizationv1connect.RegisterOrganizationServiceHandler(s, f)
	mux := http.NewServeMux()
	connecthttp.Mount(mux, s)
	ts := httptest.NewUnstartedServer(mux)
	p := new(http.Protocols)
	p.SetUnencryptedHTTP2(true)
	ts.Config.Protocols = p
	ts.Start()
	t.Cleanup(ts.Close)
	return ts.URL
}

func TestUserClient(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("each method calls its procedure with the provider user id", func(t *testing.T) {
		t.Parallel()
		f := &fakeIdentityService{}
		c := identity.NewUserClient(startIdentityService(t, f))

		require.NoError(t, c.SendVerification(ctx, "u1"))
		require.NoError(t, c.ResendVerification(ctx, "u2"))
		address, link, err := c.IssuePasskeyRegistrationLink(ctx, "u3")
		require.NoError(t, err)
		require.NoError(t, c.DeleteIdentity(ctx, "u4"))

		assert.Equal(t, "fan@example.com", address)
		assert.Equal(t, "https://liverty-music.app/passkey/register?code=one-time", link)
		assert.Equal(t, []string{
			"SendEmailVerification u1",
			"ResendEmailVerification u2",
			"IssuePasskeyRegistrationLink u3",
			"DeleteIdentity u4",
		}, f.recorded())
	})

	codesToErrs := []struct {
		code connect.Code
		want *apperr.AppErr
	}{
		{connect.CodeNotFound, apperr.ErrNotFound},
		{connect.CodeFailedPrecondition, apperr.ErrFailedPrecondition},
		{connect.CodeUnavailable, apperr.ErrUnavailable},
		{connect.CodeInternal, apperr.ErrInternal},
		{connect.CodePermissionDenied, apperr.ErrPermissionDenied},
	}
	for _, tc := range codesToErrs {
		t.Run("the identity service's "+tc.code.String()+" is the same code", func(t *testing.T) {
			t.Parallel()
			f := &fakeIdentityService{err: connect.NewError(tc.code, "refused")}
			c := identity.NewUserClient(startIdentityService(t, f))

			assert.ErrorIs(t, c.SendVerification(ctx, "u1"), tc.want)
			assert.ErrorIs(t, c.ResendVerification(ctx, "u1"), tc.want)
			_, _, err := c.IssuePasskeyRegistrationLink(ctx, "u1")
			assert.ErrorIs(t, err, tc.want)
			assert.ErrorIs(t, c.DeleteIdentity(ctx, "u1"), tc.want)
		})
	}

	t.Run("an identity service that cannot be reached is Unavailable", func(t *testing.T) {
		t.Parallel()
		ts := httptest.NewServer(http.NotFoundHandler())
		url := ts.URL
		ts.Close()

		assert.ErrorIs(t, identity.NewUserClient(url).DeleteIdentity(ctx, "u1"), apperr.ErrUnavailable)
	})
}

func TestOrganizationClient(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("each method calls its procedure", func(t *testing.T) {
		t.Parallel()
		f := &fakeIdentityService{}
		c := identity.NewOrganizationClient(startIdentityService(t, f))

		created, err := c.Create(ctx, "xx-records")
		require.NoError(t, err)
		found, err := c.GetByName(ctx, "xx-records")
		require.NoError(t, err)
		require.NoError(t, c.Provision(ctx, "org-1", "xx-records", "band@example.com"))
		require.NoError(t, c.DeactivateIdentities(ctx, "org-1"))
		require.NoError(t, c.Delete(ctx, "org-1"))

		assert.Equal(t, "org-1", created)
		assert.Equal(t, "org-1", found)
		assert.Equal(t, []string{
			"Create xx-records",
			"Get xx-records",
			"Provision org-1 xx-records band@example.com",
			"DeactivateIdentities org-1",
			"Delete org-1",
		}, f.recorded())
	})

	codesToErrs := []struct {
		code connect.Code
		want *apperr.AppErr
	}{
		{connect.CodeAlreadyExists, apperr.ErrAlreadyExists},
		{connect.CodeNotFound, apperr.ErrNotFound},
		{connect.CodeInvalidArgument, apperr.ErrInvalidArgument},
		{connect.CodeUnavailable, apperr.ErrUnavailable},
		{connect.CodeInternal, apperr.ErrInternal},
	}
	for _, tc := range codesToErrs {
		t.Run("the identity service's "+tc.code.String()+" is the same code", func(t *testing.T) {
			t.Parallel()
			f := &fakeIdentityService{err: connect.NewError(tc.code, "refused")}
			c := identity.NewOrganizationClient(startIdentityService(t, f))

			_, err := c.Create(ctx, "xx-records")
			assert.ErrorIs(t, err, tc.want)
			_, err = c.GetByName(ctx, "xx-records")
			assert.ErrorIs(t, err, tc.want)
			assert.ErrorIs(t, c.Provision(ctx, "org-1", "xx-records", "band@example.com"), tc.want)
			assert.ErrorIs(t, c.DeactivateIdentities(ctx, "org-1"), tc.want)
			assert.ErrorIs(t, c.Delete(ctx, "org-1"), tc.want)
		})
	}

	t.Run("the message of a refused Provision reaches the admin", func(t *testing.T) {
		t.Parallel()
		f := &fakeIdentityService{err: connect.NewError(connect.CodeAlreadyExists, "the owner identity's user name is already used outside the organization (already_exists)")}
		c := identity.NewOrganizationClient(startIdentityService(t, f))

		err := c.Provision(ctx, "org-1", "xx-records", "band@example.com")

		// The admin sees the message of the innermost AppErr of a client error,
		// the same as with the Zitadel client.
		var appErr *apperr.AppErr
		require.ErrorAs(t, err, &appErr)
		assert.Equal(t, apperr.ErrAlreadyExists.Code, appErr.Code)
		assert.Equal(t, "the owner identity's user name is already used outside the organization (already_exists)", appErr.Msg)
	})
}

func TestUserUseCaseThroughIdentityService(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	logger, err := logging.New()
	require.NoError(t, err)
	fan := &entity.User{ID: "019a0000-0000-7000-8000-000000000001", ProviderUserID: "zitadel-fan-1"}

	// @spec components/infrastructure/backend/process/identity-provider-credentials "Fan requests a verification resend"
	t.Run("the event consumer's verification resend asks the identity service", func(t *testing.T) {
		t.Parallel()
		f := &fakeIdentityService{}
		users := mocks.NewMockUserRepository(t)
		users.EXPECT().Get(mock.Anything, fan.ID).Return(fan, nil).Once()
		client := identity.NewUserClient(startIdentityService(t, f))
		uc := usecase.NewUserUseCase(users, nil, nil, nil, client, nil, usecase.PasskeyMailers{}, logger)

		require.NoError(t, uc.ReissueEmailVerification(ctx, fan.ID))

		assert.Equal(t, []string{"ResendEmailVerification zitadel-fan-1"}, f.recorded())
	})

	// @spec components/infrastructure/backend/process/identity-provider-credentials "Admin deletes a fan"
	t.Run("the admin console API's fan deletion asks the identity service to delete the identity", func(t *testing.T) {
		t.Parallel()
		f := &fakeIdentityService{}
		users := mocks.NewMockUserRepository(t)
		users.EXPECT().Get(mock.Anything, fan.ID).Return(fan, nil).Once()
		users.EXPECT().Delete(mock.Anything, fan.ID).Return(nil).Once()
		tickets := mocks.NewMockTicketRepository(t)
		tickets.EXPECT().ListByUser(mock.Anything, entity.UserID(fan.ID)).Return(nil, nil).Once()
		orders := mocks.NewMockOrderRepository(t)
		orders.EXPECT().ListByUser(mock.Anything, entity.UserID(fan.ID)).Return(nil, nil).Once()
		client := identity.NewUserClient(startIdentityService(t, f))
		uc := usecase.NewUserUseCase(users, tickets, orders, nil, nil, client, usecase.PasskeyMailers{}, logger)

		require.NoError(t, uc.Delete(ctx, fan.ID))

		assert.Equal(t, []string{"DeleteIdentity zitadel-fan-1"}, f.recorded())
	})
}
