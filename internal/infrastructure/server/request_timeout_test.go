package server_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	concertv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/concert/v1/concertv1connect"
	followv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/follow/v1/followv1connect"
	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	concertv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/concert/v1"
	followv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/follow/v1"
	"connectrpc.com/connect"
	"github.com/liverty-music/backend/internal/adapter/rpc"
	"github.com/liverty-music/backend/internal/adapter/rpc/mapper"
	"github.com/liverty-music/backend/internal/entity"
	entitymocks "github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/infrastructure/auth"
	authmocks "github.com/liverty-music/backend/internal/infrastructure/auth/mocks"
	"github.com/liverty-music/backend/internal/infrastructure/server"
	usecasemocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/liverty-music/backend/pkg/config"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// testHandlerTimeout stands in for the production SERVER_HANDLER_TIMEOUT (30s,
// pinned by the config defaults test) so the tests finish quickly.
const testHandlerTimeout = 100 * time.Millisecond

// newTimeoutTestServer builds a consumer Connect server the way
// di.InitializeApp does — every fan service registered with the same
// HandlerTimeout — serving the concert and follow services over mocks whose
// calls block until the request context is cancelled.
func newTimeoutTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	logger, err := logging.New()
	require.NoError(t, err)

	concertUC := usecasemocks.NewMockConcertUseCase(t)
	concertUC.EXPECT().ListByArtist(mock.Anything, mock.Anything).
		RunAndReturn(func(ctx context.Context, _ string) ([]*entity.Concert, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		}).Maybe()

	validator := authmocks.NewMockTokenValidator(t)
	validator.EXPECT().ValidateToken(mock.Anything, "fan-token").Return(&auth.Claims{Sub: "ext-user-1"}, nil).Maybe()
	userRepo := entitymocks.NewMockUserRepository(t)
	userRepo.EXPECT().GetByExternalID(mock.Anything, "ext-user-1").
		RunAndReturn(func(ctx context.Context, _ string) (*entity.User, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		}).Maybe()

	handlers := []server.RPCHandlerFunc{
		func(opts ...connect.HandlerOption) (string, http.Handler) {
			return concertv1connect.NewConcertServiceHandler(
				rpc.NewConcertHandler(concertUC, userRepo, mapper.NewMediaURLBuilder(""), logger), opts...)
		},
		func(opts ...connect.HandlerOption) (string, http.Handler) {
			return followv1connect.NewFollowServiceHandler(
				rpc.NewFollowHandler(usecasemocks.NewMockFollowUseCase(t), userRepo, logger), opts...)
		},
	}
	cfg := config.ServerSettings{
		Host:              "127.0.0.1",
		HandlerTimeout:    testHandlerTimeout,
		ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout:       2 * time.Second,
		IdleTimeout:       5 * time.Second,
	}
	authFunc := auth.NewAuthFunc(validator, auth.FanPublicProcedures())
	srv := server.NewConnectServer(cfg, logger, authFunc, newTestRateLimiter(t), unusedHealthHandler, nil, nil, handlers...)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func TestConnectServer_RequestTimeout(t *testing.T) {
	t.Parallel()

	t.Run("give up a concert call after the server's handler timeout", func(t *testing.T) {
		// @spec components/infrastructure/fan/api/server/request-timeout "Concert call over the limit"
		t.Parallel()
		ts := newTimeoutTestServer(t)
		client := concertv1connect.NewConcertServiceClient(ts.Client(), ts.URL)

		start := time.Now()
		resp, err := client.List(context.Background(), connect.NewRequest(&concertv1.ListRequest{
			ArtistId: &entityv1.ArtistId{Value: "019a0000-0000-7000-8000-0000000000b1"},
		}))

		assert.Nil(t, resp)
		assert.Equal(t, connect.CodeUnavailable, connect.CodeOf(err))
		assert.Less(t, time.Since(start), 10*testHandlerTimeout, "the concert service has no longer limit")
	})

	t.Run("give up a follow call after the server's handler timeout", func(t *testing.T) {
		// @spec components/infrastructure/fan/api/server/request-timeout "Other call over the limit"
		t.Parallel()
		ts := newTimeoutTestServer(t)
		client := followv1connect.NewFollowServiceClient(ts.Client(), ts.URL)
		req := connect.NewRequest(&followv1.ListFollowedRequest{})
		req.Header().Set("Authorization", "Bearer fan-token")

		resp, err := client.ListFollowed(context.Background(), req)

		assert.Nil(t, resp)
		assert.Equal(t, connect.CodeUnavailable, connect.CodeOf(err))
	})
}
