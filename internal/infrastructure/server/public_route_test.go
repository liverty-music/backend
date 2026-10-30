package server_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	artistv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/artist/v1/artistv1connect"
	artistv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/artist/v1"
	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/liverty-music/backend/internal/adapter/linkpreview"
	"github.com/liverty-music/backend/internal/adapter/pb"
	"github.com/liverty-music/backend/internal/adapter/rpc"
	"github.com/liverty-music/backend/internal/infrastructure/auth"
	authmocks "github.com/liverty-music/backend/internal/infrastructure/auth/mocks"
	"github.com/liverty-music/backend/internal/infrastructure/server"
	"github.com/liverty-music/backend/internal/infrastructure/server/ratelimit"
	usecasemocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/liverty-music/backend/pkg/config"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// TestConnectServer_LinkPreviewRouteIsNotRateLimited drives the link preview
// route through a real consumer Connect server whose anonymous rate limit
// allows a single request. A public RPC from the same client is throttled on
// its second call, while every link preview request still answers 200, so
// crawler traffic that reaches fan-api from Caddy's single Pod IP is never
// throttled as one client. The route also needs no bearer token.
func TestConnectServer_LinkPreviewRouteIsNotRateLimited(t *testing.T) {
	t.Parallel()

	logger, err := logging.New()
	require.NoError(t, err)

	rateLimiter := ratelimit.NewLimiter(ratelimit.Config{
		AuthRPS: 0.001, AuthBurst: 1,
		AnonRPS: 0.001, AnonBurst: 1,
	}, time.Minute)
	t.Cleanup(func() { _ = rateLimiter.Close() })

	listTop := "/" + artistv1connect.ArtistServiceName + "/ListTop"
	authFunc := auth.NewAuthFunc(authmocks.NewMockTokenValidator(t), map[string]bool{listTop: true})

	artistUC := usecasemocks.NewMockArtistUseCase(t)
	artistUC.EXPECT().ListTop(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil).Maybe()

	concertUC := usecasemocks.NewMockConcertUseCase(t)
	concertUC.EXPECT().Get(mock.Anything, mock.Anything).Return(nil, apperr.New(codes.NotFound, "event page not found")).Maybe()
	preview := linkpreview.NewHandler(concertUC, linkpreview.NewTagBuilder("https://liverty-music.app", pb.NewSeriesImageURLBuilder("")), logger)

	healthHandler := func(*connect.Server) {}
	handlers := []server.RPCHandlerFunc{
		func(s *connect.Server) {
			artistv1connect.RegisterArtistServiceHandler(s, rpc.NewArtistHandler(artistUC, logger))
		},
	}
	cfg := config.ServerSettings{
		Host:              "127.0.0.1",
		HandlerTimeout:    5 * time.Second,
		ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout:       2 * time.Second,
		IdleTimeout:       5 * time.Second,
	}
	publicRoutes := []server.PublicHTTPRoute{{Pattern: linkpreview.Pattern, Handler: preview}}
	srv := server.NewConnectServer(cfg, logger, authFunc, rateLimiter, healthHandler, nil, publicRoutes, handlers...)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	// Control: the limiter admits one anonymous RPC and throttles the next.
	client := artistv1connect.NewArtistServiceClient(connect.NewClient(connecthttp.NewTransport(ts.Client(), ts.URL)))
	_, err = client.ListTop(context.Background(), &artistv1.ListTopRequest{})
	require.NoError(t, err)
	_, err = client.ListTop(context.Background(), &artistv1.ListTopRequest{})
	require.Equal(t, connect.CodeResourceExhausted, connect.CodeOf(err), "the RPC limit is tight enough to throttle")

	// Repeated link preview requests from the same client all succeed.
	for i := range 20 {
		resp, err := ts.Client().Get(ts.URL + "/link-preview/events/019a0000-0000-7000-8000-0000000000e1")
		require.NoError(t, err)
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode, "request %d", i+1)
	}
}
