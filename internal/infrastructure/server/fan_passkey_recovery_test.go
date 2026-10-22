package server_test

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	userv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/user/v1/userv1connect"
	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	userv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/user/v1"
	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/liverty-music/backend/internal/adapter/rpc"
	"github.com/liverty-music/backend/internal/infrastructure/auth"
	authmocks "github.com/liverty-music/backend/internal/infrastructure/auth/mocks"
	"github.com/liverty-music/backend/internal/infrastructure/server"
	"github.com/liverty-music/backend/internal/infrastructure/server/ratelimit"
	usecasemocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const fanOrigin = "https://liverty-music.test"

// newFanPasskeyRecoveryTestServer builds the fan Connect server the way
// di.InitializeApp does for UserService: the AuthFunc with
// FanPublicProcedures and the per-client quota on FanClientLimitedProcedures
// (10 per 60 minutes), over a mocked UserUseCase.
func newFanPasskeyRecoveryTestServer(t *testing.T) (userv1connect.UserServiceClient, *usecasemocks.MockUserUseCase) {
	t.Helper()
	logger, err := logging.New()
	require.NoError(t, err)

	authFunc := auth.NewAuthFunc(authmocks.NewMockTokenValidator(t), auth.FanPublicProcedures())
	interceptors := []connect.ServerInterceptor{
		ratelimit.NewClientQuotaInterceptor(ratelimit.NewClientThrottle(10, 60*time.Minute, time.Now), auth.FanClientLimitedProcedures()),
	}
	userUC := usecasemocks.NewMockUserUseCase(t)
	handlers := []server.RPCHandlerFunc{
		func(s *connect.Server) {
			userv1connect.RegisterUserServiceHandler(s, rpc.NewUserHandler(userUC, logger))
		},
	}
	srv := server.NewConnectServer(testServerSettings(fanOrigin), logger, authFunc, newTestRateLimiter(t), unusedHealthHandler, interceptors, nil, handlers...)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return userv1connect.NewUserServiceClient(connect.NewClient(connecthttp.NewTransport(ts.Client(), ts.URL))), userUC
}

// passkeyRecoveryRequest is the context and request of a recovery request
// without a sign-in from the client at ip.
func passkeyRecoveryRequest(email, ip string) (context.Context, *userv1.RequestPasskeyRecoveryRequest) {
	return withHeader(context.Background(), "X-Forwarded-For", ip),
		&userv1.RequestPasskeyRecoveryRequest{Email: &entityv1.Email{Value: email}}
}

func TestFanServer_RequestPasskeyRecovery(t *testing.T) {
	t.Parallel()

	// @spec components/adapter/fan/api/rpc/user "Client over its limit"
	// @spec components/usecase/user/request-passkey-recovery "Eleventh request from one client"
	t.Run("refuses the eleventh request from one client within 60 minutes with ResourceExhausted", func(t *testing.T) {
		t.Parallel()
		client, userUC := newFanPasskeyRecoveryTestServer(t)
		// Ten from the first client and one from another; the eleventh from
		// the first client never reaches the usecase.
		userUC.EXPECT().RequestPasskeyRecovery(mock.Anything, mock.Anything).Return().Times(11)

		for i := range 10 {
			_, err := client.RequestPasskeyRecovery(passkeyRecoveryRequest("fan@example.com", "203.0.113.10"))
			require.NoError(t, err, "request %d", i+1)
		}
		_, err := client.RequestPasskeyRecovery(passkeyRecoveryRequest("other@example.com", "203.0.113.10"))
		assert.Equal(t, connect.CodeResourceExhausted, connect.CodeOf(err))

		// Another client is not limited by the first one's requests.
		_, err = client.RequestPasskeyRecovery(passkeyRecoveryRequest("fan@example.com", "198.51.100.10"))
		assert.NoError(t, err)
	})

	// @spec components/adapter/fan/api/rpc/user "Recovery request without sign-in"
	t.Run("is served without a sign-in on the fan server", func(t *testing.T) {
		t.Parallel()
		client, userUC := newFanPasskeyRecoveryTestServer(t)
		userUC.EXPECT().RequestPasskeyRecovery(mock.Anything, "fan@example.com").Return().Once()

		_, err := client.RequestPasskeyRecovery(passkeyRecoveryRequest("fan@example.com", "203.0.113.11"))

		assert.NoError(t, err)
	})

	// @spec components/adapter/fan/api/rpc/user "Not signed in"
	t.Run("other user calls still fail with Unauthenticated and are not limited per client", func(t *testing.T) {
		t.Parallel()
		client, _ := newFanPasskeyRecoveryTestServer(t)

		_, err := client.Get(context.Background(), &userv1.GetRequest{})

		assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	})
}

func TestFanClientLimitedProcedures(t *testing.T) {
	t.Parallel()

	assert.Equal(t, map[string]bool{userv1connect.UserServiceRequestPasskeyRecoveryProcedure: true},
		auth.FanClientLimitedProcedures(), "RequestPasskeyRecovery is the only fan call limited per client")
	assert.True(t, auth.FanPublicProcedures()[userv1connect.UserServiceRequestPasskeyRecoveryProcedure],
		"RequestPasskeyRecovery needs no sign-in")
}
