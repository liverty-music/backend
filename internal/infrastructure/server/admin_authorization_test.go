package server_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	adminv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/admin/v1"
	artistv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/artist/v1"
	"connectrpc.com/connect"

	adminv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/admin/v1/adminv1connect"
	artistv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/artist/v1/artistv1connect"

	"github.com/liverty-music/backend/internal/adapter/rpc"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/auth"
	authmocks "github.com/liverty-music/backend/internal/infrastructure/auth/mocks"
	"github.com/liverty-music/backend/internal/infrastructure/server"
	"github.com/liverty-music/backend/internal/infrastructure/server/ratelimit"
	usecasemocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/liverty-music/backend/pkg/config"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// newTestAdminServer builds the admin Connect server the same way
// internal/di/provider.go does: an AuthFunc with NO public procedures (see
// provider.go's adminAuthFunc) plus a server-wide RequireRoleInterceptor for
// the "admin" role, mounting the real ArtistHandler (also mounted on the
// admin server so the console can reuse ArtistService.Search) and the real
// AdminConcertHandler as a stand-in "ordinary" admin-only procedure. It
// returns an httptest server so tests can issue real HTTP requests through
// the full authn + interceptor pipeline.
func newTestAdminServer(t *testing.T, validator auth.TokenValidator, artistUC *usecasemocks.MockArtistUseCase, concertUC *usecasemocks.MockAdminConcertUseCase) *httptest.Server {
	t.Helper()

	logger, err := logging.New()
	require.NoError(t, err)

	// No public procedures, ever — mirrors provider.go's adminAuthFunc, which
	// must NOT reuse the consumer server's publicProcedures allowlist even
	// though ArtistService is mounted on both servers (backend#481).
	adminAuthFunc := auth.NewAuthFunc(validator, nil)

	rateLimiter := ratelimit.NewLimiter(ratelimit.Config{
		AuthRPS: 100, AuthBurst: 100,
		AnonRPS: 100, AnonBurst: 100,
	}, time.Minute)
	t.Cleanup(func() { _ = rateLimiter.Close() })

	healthHandler := func(_ ...connect.HandlerOption) (string, http.Handler) {
		return "/unused-health-check/", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		})
	}

	handlers := []server.RPCHandlerFunc{
		func(opts ...connect.HandlerOption) (string, http.Handler) {
			return artistv1connect.NewArtistServiceHandler(rpc.NewArtistHandler(artistUC, logger), opts...)
		},
		func(opts ...connect.HandlerOption) (string, http.Handler) {
			return adminv1connect.NewConcertServiceHandler(rpc.NewAdminConcertHandler(concertUC, logger), opts...)
		},
	}

	cfg := config.ServerSettings{
		Host:              "127.0.0.1",
		HandlerTimeout:    5 * time.Second,
		ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout:       2 * time.Second,
		IdleTimeout:       5 * time.Second,
	}
	adminInterceptors := []connect.Interceptor{auth.NewRequireRoleInterceptor("admin")}
	adminSrv := server.NewConnectServer(cfg, logger, adminAuthFunc, rateLimiter, healthHandler, adminInterceptors, nil, handlers...)

	ts := httptest.NewServer(adminSrv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

// TestAdminServer_Authorization is a table test over the admin server's
// registered procedures, covering both ArtistService methods that are
// public on the fan server (ListTop, Search) and an ordinary admin-only
// method (ConcertService.ListPending), verifying every one of them enforces
// the same contract: unauthenticated -> Unauthenticated, signed-in non-admin
// -> PermissionDenied, admin -> the usecase runs. Before backend#481 was
// fixed, ListTop/Search fell through to PermissionDenied for an
// unauthenticated caller instead, because the admin server shared the
// consumer server's public-procedure allowlist.
func TestAdminServer_Authorization(t *testing.T) {
	t.Parallel()

	const (
		adminToken    = "admin-token"
		nonAdminToken = "non-admin-token"
	)
	adminClaims := &auth.Claims{Sub: "admin-user", Roles: []string{"admin"}}
	nonAdminClaims := &auth.Claims{Sub: "regular-user", Roles: []string{"viewer"}}

	validator := authmocks.NewMockTokenValidator(t)
	validator.EXPECT().ValidateToken(mock.Anything, adminToken).Return(adminClaims, nil).Maybe()
	validator.EXPECT().ValidateToken(mock.Anything, nonAdminToken).Return(nonAdminClaims, nil).Maybe()

	artistUC := usecasemocks.NewMockArtistUseCase(t)
	artistUC.EXPECT().ListTop(mock.Anything, "", "", int32(0)).Return([]*entity.Artist{}, nil).Maybe()
	artistUC.EXPECT().Search(mock.Anything, "query").Return([]*entity.Artist{}, nil).Maybe()

	concertUC := usecasemocks.NewMockAdminConcertUseCase(t)
	concertUC.EXPECT().ListPending(mock.Anything).Return(nil, nil).Maybe()

	ts := newTestAdminServer(t, validator, artistUC, concertUC)
	httpClient := ts.Client()

	type call func(ctx context.Context, token string) error

	callArtistListTop := func(ctx context.Context, token string) error {
		client := artistv1connect.NewArtistServiceClient(httpClient, ts.URL)
		req := connect.NewRequest(&artistv1.ListTopRequest{})
		if token != "" {
			req.Header().Set("Authorization", "Bearer "+token)
		}
		_, err := client.ListTop(ctx, req)
		return err
	}
	callArtistSearch := func(ctx context.Context, token string) error {
		client := artistv1connect.NewArtistServiceClient(httpClient, ts.URL)
		req := connect.NewRequest(&artistv1.SearchRequest{Query: "query"})
		if token != "" {
			req.Header().Set("Authorization", "Bearer "+token)
		}
		_, err := client.Search(ctx, req)
		return err
	}
	callConcertListPending := func(ctx context.Context, token string) error {
		client := adminv1connect.NewConcertServiceClient(httpClient, ts.URL)
		req := connect.NewRequest(&adminv1.ListPendingRequest{})
		if token != "" {
			req.Header().Set("Authorization", "Bearer "+token)
		}
		_, err := client.ListPending(ctx, req)
		return err
	}

	procedures := map[string]call{
		"ArtistService.ListTop":      callArtistListTop,
		"ArtistService.Search":       callArtistSearch,
		"ConcertService.ListPending": callConcertListPending,
	}

	for name, do := range procedures {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			t.Run("unauthenticated caller fails with Unauthenticated", func(t *testing.T) {
				t.Parallel()
				err := do(context.Background(), "")
				require.Error(t, err)
				assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
			})

			t.Run("signed-in non-admin caller fails with PermissionDenied", func(t *testing.T) {
				t.Parallel()
				err := do(context.Background(), nonAdminToken)
				require.Error(t, err)
				assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
			})

			t.Run("admin caller is allowed through to the usecase", func(t *testing.T) {
				t.Parallel()
				err := do(context.Background(), adminToken)
				assert.NoError(t, err)
			})
		})
	}
}

// TestArtistService_AdminAuthorizationScenarios covers each scenario of the
// components/adapter/admin/api/rpc/artist "Every artist call on the admin
// console needs the admin role" requirement against the real admin server
// wiring (authn middleware + RequireRoleInterceptor), including the two
// browsing/write shapes ("Guest browses" via ListTop, "Guest creates" via
// Create) that must both fail with Unauthenticated even though ListTop is
// also served, unauthenticated, on the fan boundary.
func TestArtistService_AdminAuthorizationScenarios(t *testing.T) {
	t.Parallel()

	const adminToken = "admin-token"
	const nonAdminToken = "non-admin-token"
	adminClaims := &auth.Claims{Sub: "admin-user", Roles: []string{"admin"}}
	nonAdminClaims := &auth.Claims{Sub: "regular-user", Roles: []string{"viewer"}}

	validator := authmocks.NewMockTokenValidator(t)
	validator.EXPECT().ValidateToken(mock.Anything, adminToken).Return(adminClaims, nil).Maybe()
	validator.EXPECT().ValidateToken(mock.Anything, nonAdminToken).Return(nonAdminClaims, nil).Maybe()

	artistUC := usecasemocks.NewMockArtistUseCase(t)
	artistUC.EXPECT().Search(mock.Anything, "queen").Return([]*entity.Artist{{Name: "Queen"}}, nil).Maybe()

	concertUC := usecasemocks.NewMockAdminConcertUseCase(t)

	ts := newTestAdminServer(t, validator, artistUC, concertUC)
	client := artistv1connect.NewArtistServiceClient(ts.Client(), ts.URL)

	t.Run("Admin searches for an artist", func(t *testing.T) {
		// @spec components/adapter/admin/api/rpc/artist "Admin searches for an artist"
		t.Parallel()

		req := connect.NewRequest(&artistv1.SearchRequest{Query: "queen"})
		req.Header().Set("Authorization", "Bearer "+adminToken)

		resp, err := client.Search(context.Background(), req)
		require.NoError(t, err)
		assert.Len(t, resp.Msg.GetArtists(), 1, "ArtistUseCase.Search should have run and returned the matching artist")
	})

	t.Run("Signed-in caller without the admin role", func(t *testing.T) {
		// @spec components/adapter/admin/api/rpc/artist "Signed-in caller without the admin role"
		t.Parallel()

		req := connect.NewRequest(&artistv1.SearchRequest{Query: "queen"})
		req.Header().Set("Authorization", "Bearer "+nonAdminToken)

		_, err := client.Search(context.Background(), req)
		require.Error(t, err)
		assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	})

	t.Run("Guest browses", func(t *testing.T) {
		// @spec components/adapter/admin/api/rpc/artist "Guest browses"
		t.Parallel()

		_, err := client.ListTop(context.Background(), connect.NewRequest(&artistv1.ListTopRequest{}))
		require.Error(t, err)
		assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	})

	t.Run("Guest creates", func(t *testing.T) {
		// @spec components/adapter/admin/api/rpc/artist "Guest creates"
		t.Parallel()

		// RequireRoleInterceptor runs before request validation, so an empty
		// (otherwise-invalid) CreateRequest still exercises the auth check
		// this scenario verifies, without needing a valid Name/Mbid.
		_, err := client.Create(context.Background(), connect.NewRequest(&artistv1.CreateRequest{}))
		require.Error(t, err)
		assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	})
}
