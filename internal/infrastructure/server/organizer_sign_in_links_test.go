package server_test

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	organizerseriesv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/organizer/series/v1/seriesv1connect"
	organizerv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/organizer/v1/organizerv1connect"
	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	organizerseriesv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/organizer/series/v1"
	organizerv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/organizer/v1"
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
	"google.golang.org/protobuf/proto"
)

// newOrganizerSignInTestServer builds the organizer Connect server the way
// di.InitializeApp does: the AuthFunc with OrganizerPublicProcedures, the
// per-client quota on them (10 per 60 minutes), the OrgScopedInterceptor that
// skips them, and the OrganizerService and SeriesService handlers over
// mocked use cases.
func newOrganizerSignInTestServer(t *testing.T) (organizerv1connect.OrganizerServiceClient, organizerseriesv1connect.SeriesServiceClient, *usecasemocks.MockOrganizerUseCase) {
	t.Helper()
	logger, err := logging.New()
	require.NoError(t, err)

	public := auth.OrganizerPublicProcedures()
	authFunc := auth.NewAuthFunc(authmocks.NewMockTokenValidator(t), public)
	interceptors := []connect.ServerInterceptor{
		ratelimit.NewClientQuotaInterceptor(ratelimit.NewClientThrottle(10, 60*time.Minute, time.Now), public),
		auth.NewOrgScopedInterceptor("organizer-console-project", public),
	}
	organizerUC := usecasemocks.NewMockOrganizerUseCase(t)
	seriesUC := usecasemocks.NewMockSeriesUseCase(t)
	handlers := []server.RPCHandlerFunc{
		func(s *connect.Server) {
			organizerv1connect.RegisterOrganizerServiceHandler(s, rpc.NewOrganizerHandler(organizerUC, logger))
		},
		func(s *connect.Server) {
			organizerseriesv1connect.RegisterSeriesServiceHandler(s, rpc.NewOrganizerSeriesHandler(seriesUC, organizerUC, nil, logger))
		},
	}
	srv := server.NewConnectServer(testServerSettings(organizerOrigin), logger, authFunc, newTestRateLimiter(t), unusedHealthHandler, interceptors, nil, handlers...)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return organizerv1connect.NewOrganizerServiceClient(connect.NewClient(connecthttp.NewTransport(ts.Client(), ts.URL))),
		organizerseriesv1connect.NewSeriesServiceClient(connect.NewClient(connecthttp.NewTransport(ts.Client(), ts.URL))),
		organizerUC
}

// signInLinksRequest is the context and SendSignInLinks request of a call
// without a sign-in from the client at ip.
func signInLinksRequest(email, ip string) (context.Context, *organizerv1.SendSignInLinksRequest) {
	return withHeader(context.Background(), "X-Forwarded-For", ip),
		&organizerv1.SendSignInLinksRequest{Email: &entityv1.Email{Value: email}}
}

func TestOrganizerServer_SendSignInLinks(t *testing.T) {
	t.Parallel()

	// @spec components/adapter/organizer/api/rpc/organizer "Owner asks for sign-in links"
	t.Run("succeeds without a sign-in with an empty response and runs the usecase for the address", func(t *testing.T) {
		t.Parallel()
		client, _, organizerUC := newOrganizerSignInTestServer(t)
		organizerUC.EXPECT().SendSignInLinks(mock.Anything, "band@example.com").Return().Once()

		resp, err := client.SendSignInLinks(signInLinksRequest("band@example.com", "203.0.113.1"))

		require.NoError(t, err)
		assert.True(t, proto.Equal(&organizerv1.SendSignInLinksResponse{}, resp))
	})

	// @spec components/adapter/organizer/api/rpc/organizer "Unknown address"
	t.Run("answers an address that owns no Organizer with the same empty response", func(t *testing.T) {
		t.Parallel()
		client, _, organizerUC := newOrganizerSignInTestServer(t)
		organizerUC.EXPECT().SendSignInLinks(mock.Anything, "nobody@example.com").Return().Once()

		resp, err := client.SendSignInLinks(signInLinksRequest("nobody@example.com", "203.0.113.2"))

		require.NoError(t, err)
		assert.True(t, proto.Equal(&organizerv1.SendSignInLinksResponse{}, resp))
	})

	// @spec components/adapter/organizer/api/rpc/organizer "Malformed address"
	t.Run("fails with InvalidArgument for band@ and runs no usecase", func(t *testing.T) {
		t.Parallel()
		// The strict usecase mock fails the test if SendSignInLinks runs.
		client, _, _ := newOrganizerSignInTestServer(t)

		_, err := client.SendSignInLinks(signInLinksRequest("band@", "203.0.113.3"))

		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	})

	t.Run("fails with InvalidArgument without an address", func(t *testing.T) {
		t.Parallel()
		client, _, _ := newOrganizerSignInTestServer(t)
		req := &organizerv1.SendSignInLinksRequest{}

		_, err := client.SendSignInLinks(withHeader(context.Background(), "X-Forwarded-For", "203.0.113.4"), req)

		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	})

	// @spec components/adapter/organizer/api/rpc/organizer "Too many requests"
	// @spec components/usecase/organizer/send-sign-in-links "Eleventh request from one client"
	t.Run("refuses the eleventh request from one client within 60 minutes with ResourceExhausted", func(t *testing.T) {
		t.Parallel()
		client, _, organizerUC := newOrganizerSignInTestServer(t)
		organizerUC.EXPECT().SendSignInLinks(mock.Anything, mock.Anything).Return().Times(11)

		for i := range 10 {
			_, err := client.SendSignInLinks(signInLinksRequest("band@example.com", "203.0.113.5"))
			require.NoError(t, err, "request %d", i+1)
		}
		_, err := client.SendSignInLinks(signInLinksRequest("other@example.com", "203.0.113.5"))
		assert.Equal(t, connect.CodeResourceExhausted, connect.CodeOf(err))

		// Another client is not limited by the first one's requests.
		_, err = client.SendSignInLinks(signInLinksRequest("band@example.com", "198.51.100.7"))
		assert.NoError(t, err)
	})

	t.Run("every other organizer procedure still answers Unauthenticated without a sign-in", func(t *testing.T) {
		t.Parallel()
		client, seriesClient, _ := newOrganizerSignInTestServer(t)

		_, err := client.Get(context.Background(), &organizerv1.GetRequest{})
		assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err), "OrganizerService.Get")

		_, err = seriesClient.List(context.Background(), &organizerseriesv1.ListRequest{})
		assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err), "SeriesService.List")
	})
}

func TestOrganizerPublicProcedures(t *testing.T) {
	t.Parallel()

	assert.Equal(t, map[string]bool{organizerv1connect.OrganizerServiceSendSignInLinksProcedure: true},
		auth.OrganizerPublicProcedures(), "SendSignInLinks is the only organizer call without a sign-in")
}
