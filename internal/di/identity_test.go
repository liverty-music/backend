package di_test

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	backendorganizationv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/backend/organization/v1/organizationv1connect"
	backenduserv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/backend/user/v1/userv1connect"
	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	backendorganizationv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/backend/organization/v1"
	backenduserv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/backend/user/v1"
	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"connectrpc.com/grpchealth/v2"
	"github.com/liverty-music/backend/internal/adapter/rpc"
	rpcmocks "github.com/liverty-music/backend/internal/adapter/rpc/mocks"
	"github.com/liverty-music/backend/internal/di"
	"github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/infrastructure/server"
	"github.com/liverty-music/backend/pkg/shutdown"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func readiness(t *testing.T, h *rpc.ProcessHealthCheckHandler, service string) grpchealth.Status {
	t.Helper()
	resp, err := h.Check(context.Background(), &grpchealth.CheckRequest{Service: service})
	require.NoError(t, err)
	return resp.Status
}

// The identity service is built and served with only its keys and the
// issuer: no database or messaging settings exist for it.
//
// @spec components/infrastructure/backend/process/health-probes "Identity provider unreachable"
func TestInitializeIdentityApp_StartsWithoutDatabaseWhileIdentityProviderIsUnreachable(t *testing.T) {
	t.Setenv("ENVIRONMENT", "local")
	// The issuer host must start with "auth."; ".invalid" never resolves.
	t.Setenv("OIDC_ISSUER_URL", "http://auth.liverty-music.invalid")
	t.Setenv("ORGANIZER_CONSOLE_PROJECT_ID", "organizer-console-project")
	t.Setenv("ZITADEL_MACHINE_KEY_FOR_BACKEND_APP_PATH", writeMachineKey(t))
	t.Setenv("ZITADEL_MACHINE_KEY_FOR_FAN_PASSKEY_RECOVERY_PATH", writeMachineKey(t))
	t.Setenv("ZITADEL_MACHINE_KEY_FOR_ORGANIZER_PROVISIONER_PATH", writeMachineKey(t))
	t.Cleanup(shutdown.Reset)

	app, err := di.InitializeIdentityApp(context.Background())
	require.NoError(t, err)

	assert.Equal(t, grpchealth.StatusNotServing, readiness(t, app.Health, ""), "not ready before the listeners are bound")
	app.Health.SetReady()

	srv := httptest.NewServer(app.EventConsumerServer.Handler())
	t.Cleanup(srv.Close)
	client := backenduserv1connect.NewUserServiceClient(connect.NewClient(connecthttp.NewTransport(srv.Client(), srv.URL)))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	_, err = client.SendEmailVerification(ctx, &backenduserv1.SendEmailVerificationRequest{
		ProviderUserId: &entityv1.User_ProviderUserId{Value: "zitadel-user-1"},
	})

	assert.Equal(t, connect.CodeUnavailable, connect.CodeOf(err), "a request fails with Unavailable: %v", err)
	assert.Equal(t, grpchealth.StatusServing, readiness(t, app.Health, ""), "still ready")
	assert.Equal(t, grpchealth.StatusServing, readiness(t, app.Health, rpc.LivenessService), "still alive")
}

// identityListener serves the identity service's handlers over mocked
// identity-provider clients behind the given caller's allowlist.
func identityListener(t *testing.T, allowed []string, ev *rpcmocks.MockEmailVerificationCodes, ir *mocks.MockIdentityRemover, orgs *mocks.MockOrganizationRepository) *httptest.Server {
	t.Helper()
	logger, err := logging.New()
	require.NoError(t, err)
	srv := server.NewBackendServer(server.BackendServerSettings{HandlerTimeout: 5 * time.Second}, logger, allowed,
		func(s *connect.Server) {
			backenduserv1connect.RegisterUserServiceHandler(s, rpc.NewBackendUserHandler(ev, ir, nil))
		},
		func(s *connect.Server) {
			backendorganizationv1connect.RegisterOrganizationServiceHandler(s, rpc.NewBackendOrganizationHandler(orgs))
		},
	)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func TestIdentityListeners(t *testing.T) {
	t.Parallel()
	userID := &entityv1.User_ProviderUserId{Value: "zitadel-user-1"}

	// @spec components/infrastructure/backend/process/identity-provider-credentials "Operation outside the caller's set"
	t.Run("the event consumer listener refuses DeleteIdentity and removes nothing", func(t *testing.T) {
		t.Parallel()
		ir := mocks.NewMockIdentityRemover(t) // no expectation: a call fails the test
		ts := identityListener(t, di.EventConsumerProcedures, rpcmocks.NewMockEmailVerificationCodes(t), ir, mocks.NewMockOrganizationRepository(t))
		client := backenduserv1connect.NewUserServiceClient(connect.NewClient(connecthttp.NewTransport(ts.Client(), ts.URL)))

		_, err := client.DeleteIdentity(context.Background(), &backenduserv1.DeleteIdentityRequest{ProviderUserId: userID})

		assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	})

	t.Run("the event consumer listener refuses every Organization procedure", func(t *testing.T) {
		t.Parallel()
		ts := identityListener(t, di.EventConsumerProcedures, rpcmocks.NewMockEmailVerificationCodes(t), mocks.NewMockIdentityRemover(t), mocks.NewMockOrganizationRepository(t))
		client := backendorganizationv1connect.NewOrganizationServiceClient(connect.NewClient(connecthttp.NewTransport(ts.Client(), ts.URL)))

		_, err := client.Delete(context.Background(), &backendorganizationv1.DeleteRequest{
			ProviderOrganizerId: &entityv1.Organizer_ProviderOrganizerId{Value: "org-1"},
		})

		assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	})

	t.Run("the event consumer listener serves ResendEmailVerification", func(t *testing.T) {
		t.Parallel()
		ev := rpcmocks.NewMockEmailVerificationCodes(t)
		ev.EXPECT().IssueEmailVerificationCode(mock.Anything, "zitadel-user-1").Return("fan@example.com", "ABC123", nil).Once()
		ts := identityListener(t, di.EventConsumerProcedures, ev, mocks.NewMockIdentityRemover(t), mocks.NewMockOrganizationRepository(t))
		client := backenduserv1connect.NewUserServiceClient(connect.NewClient(connecthttp.NewTransport(ts.Client(), ts.URL)))

		resp, err := client.ResendEmailVerification(context.Background(), &backenduserv1.ResendEmailVerificationRequest{ProviderUserId: userID})

		require.NoError(t, err)
		assert.Equal(t, "fan@example.com", resp.GetEmail().GetValue())
		assert.Equal(t, "ABC123", resp.GetCode().GetValue())
	})

	// @spec components/infrastructure/backend/process/identity-provider-credentials "Fan API asks for another operation"
	t.Run("the fan API listener refuses SendEmailVerification and every Organization procedure and changes nothing", func(t *testing.T) {
		t.Parallel()
		ev := rpcmocks.NewMockEmailVerificationCodes(t) // no expectation: a call fails the test
		orgs := mocks.NewMockOrganizationRepository(t)
		ts := identityListener(t, di.FanAPIProcedures, ev, mocks.NewMockIdentityRemover(t), orgs)
		transport := connect.NewClient(connecthttp.NewTransport(ts.Client(), ts.URL))

		_, err := backenduserv1connect.NewUserServiceClient(transport).
			SendEmailVerification(context.Background(), &backenduserv1.SendEmailVerificationRequest{ProviderUserId: userID})
		assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))

		_, err = backendorganizationv1connect.NewOrganizationServiceClient(transport).
			Delete(context.Background(), &backendorganizationv1.DeleteRequest{
				ProviderOrganizerId: &entityv1.Organizer_ProviderOrganizerId{Value: "org-1"},
			})
		assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	})

	// @spec components/infrastructure/backend/process/identity-provider-credentials "Fan deletes their account"
	t.Run("the fan API listener serves DeleteIdentity", func(t *testing.T) {
		t.Parallel()
		ir := mocks.NewMockIdentityRemover(t)
		ir.EXPECT().DeleteIdentity(mock.Anything, "zitadel-user-1").Return(nil).Once()
		ts := identityListener(t, di.FanAPIProcedures, rpcmocks.NewMockEmailVerificationCodes(t), ir, mocks.NewMockOrganizationRepository(t))
		client := backenduserv1connect.NewUserServiceClient(connect.NewClient(connecthttp.NewTransport(ts.Client(), ts.URL)))

		_, err := client.DeleteIdentity(context.Background(), &backenduserv1.DeleteIdentityRequest{ProviderUserId: userID})

		assert.NoError(t, err)
	})

	t.Run("the fan API listener serves VerifyEmail", func(t *testing.T) {
		t.Parallel()
		ev := rpcmocks.NewMockEmailVerificationCodes(t)
		ev.EXPECT().VerifyEmail(mock.Anything, "zitadel-user-1", "ABC123").Return(nil).Once()
		ts := identityListener(t, di.FanAPIProcedures, ev, mocks.NewMockIdentityRemover(t), mocks.NewMockOrganizationRepository(t))
		client := backenduserv1connect.NewUserServiceClient(connect.NewClient(connecthttp.NewTransport(ts.Client(), ts.URL)))

		_, err := client.VerifyEmail(context.Background(), &backenduserv1.VerifyEmailRequest{
			ProviderUserId: userID,
			Code:           &entityv1.EmailVerificationCode{Value: "ABC123"},
		})

		assert.NoError(t, err)
	})

	t.Run("the event consumer listener refuses VerifyEmail", func(t *testing.T) {
		t.Parallel()
		ts := identityListener(t, di.EventConsumerProcedures, rpcmocks.NewMockEmailVerificationCodes(t), mocks.NewMockIdentityRemover(t), mocks.NewMockOrganizationRepository(t))
		client := backenduserv1connect.NewUserServiceClient(connect.NewClient(connecthttp.NewTransport(ts.Client(), ts.URL)))

		_, err := client.VerifyEmail(context.Background(), &backenduserv1.VerifyEmailRequest{
			ProviderUserId: userID,
			Code:           &entityv1.EmailVerificationCode{Value: "ABC123"},
		})

		assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	})

	t.Run("the admin console API listener refuses SendEmailVerification", func(t *testing.T) {
		t.Parallel()
		ts := identityListener(t, di.AdminConsoleAPIProcedures, rpcmocks.NewMockEmailVerificationCodes(t), mocks.NewMockIdentityRemover(t), mocks.NewMockOrganizationRepository(t))
		client := backenduserv1connect.NewUserServiceClient(connect.NewClient(connecthttp.NewTransport(ts.Client(), ts.URL)))

		_, err := client.SendEmailVerification(context.Background(), &backenduserv1.SendEmailVerificationRequest{ProviderUserId: userID})

		assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	})

	t.Run("the admin console API listener serves DeleteIdentity", func(t *testing.T) {
		t.Parallel()
		ir := mocks.NewMockIdentityRemover(t)
		ir.EXPECT().DeleteIdentity(mock.Anything, "zitadel-user-1").Return(nil).Once()
		ts := identityListener(t, di.AdminConsoleAPIProcedures, rpcmocks.NewMockEmailVerificationCodes(t), ir, mocks.NewMockOrganizationRepository(t))
		client := backenduserv1connect.NewUserServiceClient(connect.NewClient(connecthttp.NewTransport(ts.Client(), ts.URL)))

		_, err := client.DeleteIdentity(context.Background(), &backenduserv1.DeleteIdentityRequest{ProviderUserId: userID})

		assert.NoError(t, err)
	})
}
