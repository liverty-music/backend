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
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const fanOrigin = "https://liverty-music.test"

// newFanPasskeyRecoveryTestServer builds the fan Connect server the way
// di.InitializeApp does for UserService: the AuthFunc with
// FanPublicProcedures and the per-client quotas on FanClientLimitedProcedures
// and FanEmailVerificationProcedures (each 10 per 60 minutes), over a mocked
// UserUseCase.
func newFanPasskeyRecoveryTestServer(t *testing.T) (userv1connect.UserServiceClient, *usecasemocks.MockUserUseCase) {
	t.Helper()
	logger, err := logging.New()
	require.NoError(t, err)

	authFunc := auth.NewAuthFunc(authmocks.NewMockTokenValidator(t), auth.FanPublicProcedures())
	interceptors := []connect.ServerInterceptor{
		ratelimit.NewClientQuotaInterceptor(ratelimit.NewClientThrottle(10, 60*time.Minute, time.Now), auth.FanClientLimitedProcedures()),
		ratelimit.NewClientQuotaInterceptor(ratelimit.NewClientThrottle(10, 60*time.Minute, time.Now), auth.FanEmailVerificationProcedures()),
	}
	userUC := usecasemocks.NewMockUserUseCase(t)
	handlers := []server.RPCHandlerFunc{
		func(s *connect.Server) {
			userv1connect.RegisterUserServiceHandler(s, rpc.NewUserHandler(userUC, nil, logger))
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

// verifyEmailRequest is the context and request of an email verification
// without a sign-in from the client at ip.
func verifyEmailRequest(userID, code, ip string) (context.Context, *userv1.VerifyEmailRequest) {
	return withHeader(context.Background(), "X-Forwarded-For", ip),
		&userv1.VerifyEmailRequest{UserId: &entityv1.UserId{Value: userID}, Code: &entityv1.EmailVerificationCode{Value: code}}
}

func TestFanServer_VerifyEmail(t *testing.T) {
	t.Parallel()
	const userID = "019a0000-0000-7000-8000-000000000001"

	// @spec components/adapter/fan/api/rpc/user "Email verification without sign-in"
	// @spec components/adapter/fan/api/rpc/user "Link from the email"
	t.Run("is served without a sign-in and hands the link to the usecase", func(t *testing.T) {
		t.Parallel()
		client, userUC := newFanPasskeyRecoveryTestServer(t)
		userUC.EXPECT().VerifyEmail(mock.Anything, userID, "AB12CD").Return(nil).Once()

		_, err := client.VerifyEmail(verifyEmailRequest(userID, "AB12CD", "203.0.113.20"))

		assert.NoError(t, err)
	})

	t.Run("returns the usecase's outcome unchanged", func(t *testing.T) {
		t.Parallel()
		client, userUC := newFanPasskeyRecoveryTestServer(t)
		userUC.EXPECT().VerifyEmail(mock.Anything, userID, "AB12CD").Return(apperr.New(codes.InvalidArgument, "email verification link is not valid")).Once()

		_, err := client.VerifyEmail(verifyEmailRequest(userID, "AB12CD", "203.0.113.21"))

		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	})

	malformed := []struct {
		name   string
		userID string
		code   string
	}{
		// @spec components/adapter/fan/api/rpc/user "Malformed link"
		{name: "a User id that is not a UUID", userID: "not-a-uuid", code: "AB12CD"},
		{name: "an empty code", userID: userID, code: ""},
		{name: "a code longer than 32 characters", userID: userID, code: "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456"},
	}
	for _, tt := range malformed {
		t.Run("refuses "+tt.name+" with InvalidArgument before the usecase", func(t *testing.T) {
			t.Parallel()
			client, _ := newFanPasskeyRecoveryTestServer(t) // no expectation: a usecase call fails the test

			_, err := client.VerifyEmail(verifyEmailRequest(tt.userID, tt.code, "203.0.113.22"))

			assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
		})
	}

	// @spec components/usecase/user/verify-email "Eleventh attempt from one client"
	t.Run("refuses the eleventh attempt from one client within 60 minutes, counted apart from recovery requests", func(t *testing.T) {
		t.Parallel()
		client, userUC := newFanPasskeyRecoveryTestServer(t)
		userUC.EXPECT().VerifyEmail(mock.Anything, mock.Anything, mock.Anything).Return(nil).Times(11)
		userUC.EXPECT().RequestPasskeyRecovery(mock.Anything, mock.Anything).Return().Once()

		for i := range 10 {
			_, err := client.VerifyEmail(verifyEmailRequest(userID, "AB12CD", "203.0.113.23"))
			require.NoError(t, err, "attempt %d", i+1)
		}
		_, err := client.VerifyEmail(verifyEmailRequest(userID, "AB12CD", "203.0.113.23"))
		assert.Equal(t, connect.CodeResourceExhausted, connect.CodeOf(err))

		// The client's recovery requests and another client's attempts have
		// their own counts.
		_, err = client.RequestPasskeyRecovery(passkeyRecoveryRequest("fan@example.com", "203.0.113.23"))
		assert.NoError(t, err)
		_, err = client.VerifyEmail(verifyEmailRequest(userID, "AB12CD", "198.51.100.23"))
		assert.NoError(t, err)
	})
}

func TestFanClientLimitedProcedures(t *testing.T) {
	t.Parallel()

	assert.Equal(t, map[string]bool{userv1connect.UserServiceRequestPasskeyRecoveryProcedure: true},
		auth.FanClientLimitedProcedures(), "RequestPasskeyRecovery is the only fan call on the recovery count")
	assert.Equal(t, map[string]bool{userv1connect.UserServiceVerifyEmailProcedure: true},
		auth.FanEmailVerificationProcedures(), "VerifyEmail is the only fan call on the email verification count")
	assert.True(t, auth.FanPublicProcedures()[userv1connect.UserServiceRequestPasskeyRecoveryProcedure],
		"RequestPasskeyRecovery needs no sign-in")
	assert.True(t, auth.FanPublicProcedures()[userv1connect.UserServiceVerifyEmailProcedure],
		"VerifyEmail needs no sign-in")
}
