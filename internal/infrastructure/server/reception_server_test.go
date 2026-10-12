package server_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	organizerscannerv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/organizer/scanner/v1/scannerv1connect"
	scannerv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/scanner/v1/scannerv1connect"
	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	organizerscannerv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/organizer/scanner/v1"
	scannerv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/scanner/v1"
	"connectrpc.com/connect"
	"github.com/liverty-music/backend/internal/adapter/rpc"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/auth"
	authmocks "github.com/liverty-music/backend/internal/infrastructure/auth/mocks"
	"github.com/liverty-music/backend/internal/infrastructure/server"
	"github.com/liverty-music/backend/internal/infrastructure/server/ratelimit"
	"github.com/liverty-music/backend/internal/testutil"
	"github.com/liverty-music/backend/internal/usecase"
	usecasemocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/liverty-music/backend/pkg/config"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	receptionOrigin = "https://reception.liverty-music.test"
	organizerOrigin = "https://organizer.liverty-music.test"
)

// testServerSettings returns the listener settings shared by the test servers,
// allowing CORS from origin only.
func testServerSettings(origin string) config.ServerSettings {
	return config.ServerSettings{
		Host:              "127.0.0.1",
		AllowedOrigins:    []string{origin},
		HandlerTimeout:    5 * time.Second,
		ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout:       2 * time.Second,
		IdleTimeout:       5 * time.Second,
	}
}

// newTestRateLimiter returns a rate limiter loose enough not to interfere.
func newTestRateLimiter(t *testing.T) *ratelimit.Limiter {
	t.Helper()
	rateLimiter := ratelimit.NewLimiter(ratelimit.Config{AuthRPS: 100, AuthBurst: 100, AnonRPS: 100, AnonBurst: 100}, time.Minute)
	t.Cleanup(func() { _ = rateLimiter.Close() })
	return rateLimiter
}

func unusedHealthHandler(_ ...connect.HandlerOption) (string, http.Handler) {
	return "/unused-health-check/", http.NotFoundHandler()
}

// newReceptionTestServer builds the reception Connect server the way
// di.InitializeApp does: the reception AuthFunc with ReceptionPublicProcedures,
// the unknown-token interceptor, and only the device-facing ScannerService handler over
// mocked use cases.
func newReceptionTestServer(t *testing.T) (*httptest.Server, *usecasemocks.MockScannerUseCase) {
	t.Helper()
	logger, err := logging.New()
	require.NoError(t, err)

	public := auth.ReceptionPublicProcedures()
	authFunc := auth.NewAuthFunc(authmocks.NewMockTokenValidator(t), public)
	throttle := ratelimit.NewUnknownTokenThrottle(10, 10*time.Minute, time.Now)
	interceptors := []connect.Interceptor{
		ratelimit.NewUnknownTokenInterceptor(throttle, public, func(err error) bool {
			return errors.Is(err, usecase.ErrUnknownScannerLinkToken)
		}),
	}

	scannerUC := usecasemocks.NewMockScannerUseCase(t)
	ticketUC := usecasemocks.NewMockTicketUseCase(t)
	handlers := []server.RPCHandlerFunc{
		func(opts ...connect.HandlerOption) (string, http.Handler) {
			return scannerv1connect.NewScannerServiceHandler(rpc.NewScannerHandler(scannerUC, ticketUC, logger), opts...)
		},
	}
	srv := server.NewConnectServer(testServerSettings(receptionOrigin), logger, authFunc, newTestRateLimiter(t), unusedHealthHandler, interceptors, nil, handlers...)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, scannerUC
}

// newOrganizerTestServer builds the organizer Connect server the way
// di.InitializeApp does as far as Scanners are concerned: an AuthFunc with no
// public procedures, the OrgScopedInterceptor, and the ScannerService
// handler over mocked use cases.
func newOrganizerTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	logger, err := logging.New()
	require.NoError(t, err)

	authFunc := auth.NewAuthFunc(authmocks.NewMockTokenValidator(t), nil)
	interceptors := []connect.Interceptor{auth.NewOrgScopedInterceptor("organizer-console-project")}
	scannerUC := usecasemocks.NewMockScannerUseCase(t)
	organizerUC := usecasemocks.NewMockOrganizerUseCase(t)
	handlers := []server.RPCHandlerFunc{
		func(opts ...connect.HandlerOption) (string, http.Handler) {
			return organizerscannerv1connect.NewScannerServiceHandler(rpc.NewOrganizerScannerHandler(scannerUC, organizerUC, logger), opts...)
		},
	}
	srv := server.NewConnectServer(testServerSettings(organizerOrigin), logger, authFunc, newTestRateLimiter(t), unusedHealthHandler, interceptors, nil, handlers...)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

// openRequest returns a well-formed Open request with no bearer token.
func openRequest(t *testing.T) *connect.Request[scannerv1.OpenRequest] {
	t.Helper()
	return connect.NewRequest(&scannerv1.OpenRequest{
		LinkToken: &entityv1.ScannerLinkToken{Value: "tok_0123456789abcdefghijklmn"},
		SignTime:  timestamppb.Now(),
		Signature: &entityv1.Signature{Value: make([]byte, 64)},
		PublicKey: &entityv1.PublicKey{Value: testutil.NewDeviceKey(t).PublicKey(t)},
	})
}

func TestReceptionServer(t *testing.T) {
	t.Parallel()

	t.Run("staff device without an account", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/organizer/api/rpc/scanner-device "Staff device without an account"
		ts, scannerUC := newReceptionTestServer(t)
		scannerUC.EXPECT().Open(mock.Anything, mock.MatchedBy(func(in usecase.OpenScannerInput) bool {
			return in.LinkToken == "tok_0123456789abcdefghijklmn"
		})).Return(&usecase.OpenScannerResult{
			Scanner: &entity.Scanner{ID: "019a0000-0000-7000-8000-0000000000a1", EventID: "019a0000-0000-7000-8000-0000000000e1", Number: 1, Status: entity.ScannerStatusInUse},
		}, nil)

		client := scannerv1connect.NewScannerServiceClient(ts.Client(), ts.URL)
		resp, err := client.Open(context.Background(), openRequest(t))
		require.NoError(t, err, "no bearer token is needed for the device-facing ScannerService")
		assert.Equal(t, int32(1), resp.Msg.Scanner.Number.Value)
	})

	t.Run("malformed scanner call is refused by validation", func(t *testing.T) {
		t.Parallel()
		ts, _ := newReceptionTestServer(t)
		client := scannerv1connect.NewScannerServiceClient(ts.Client(), ts.URL)
		_, err := client.Admit(context.Background(), connect.NewRequest(&scannerv1.AdmitRequest{
			LinkToken: &entityv1.ScannerLinkToken{Value: "tok_0123456789abcdefghijklmn"},
			SignTime:  timestamppb.Now(),
			Signature: &entityv1.Signature{Value: make([]byte, 10)},
		}))
		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	})

	t.Run("no other service is served", func(t *testing.T) {
		t.Parallel()
		ts, _ := newReceptionTestServer(t)
		client := organizerscannerv1connect.NewScannerServiceClient(ts.Client(), ts.URL)
		_, err := client.Create(context.Background(), connect.NewRequest(&organizerscannerv1.CreateRequest{
			EventId: &entityv1.EventId{Value: "019a0000-0000-7000-8000-0000000000e1"},
		}))
		assert.Contains(t, []connect.Code{connect.CodeUnauthenticated, connect.CodeUnimplemented}, connect.CodeOf(err))
	})

	t.Run("procedures of the old ReceptionService are not served", func(t *testing.T) {
		t.Parallel()
		// The mocked use cases have no expectations, so a call that reached a
		// handler would fail the test.
		ts, _ := newReceptionTestServer(t)
		for _, procedure := range []string{
			"/liverty_music.rpc.organizer.reception.v1.ReceptionService/Open",
			"/liverty_music.rpc.organizer.reception.v1.ReceptionService/Admit",
		} {
			client := connect.NewClient[scannerv1.OpenRequest, scannerv1.OpenResponse](ts.Client(), ts.URL+procedure)
			_, err := client.CallUnary(context.Background(), openRequest(t))
			assert.Contains(t, []connect.Code{connect.CodeUnauthenticated, connect.CodeUnimplemented}, connect.CodeOf(err), procedure)
		}
	})

	t.Run("CORS preflight from the reception web origin", func(t *testing.T) {
		t.Parallel()
		ts, _ := newReceptionTestServer(t)
		req, err := http.NewRequest(http.MethodOptions, ts.URL+scannerv1connect.ScannerServiceAdmitProcedure, nil)
		require.NoError(t, err)
		req.Header.Set("Origin", receptionOrigin)
		req.Header.Set("Access-Control-Request-Method", http.MethodPost)
		req.Header.Set("Access-Control-Request-Headers", "connect-protocol-version,content-type") // browsers send the list sorted
		resp, err := ts.Client().Do(req)
		require.NoError(t, err)
		_ = resp.Body.Close()
		assert.Equal(t, receptionOrigin, resp.Header.Get("Access-Control-Allow-Origin"))

		for _, origin := range []string{organizerOrigin, "https://evil.example"} {
			req.Header.Set("Origin", origin)
			resp, err = ts.Client().Do(req)
			require.NoError(t, err)
			_ = resp.Body.Close()
			assert.Empty(t, resp.Header.Get("Access-Control-Allow-Origin"), "%s is not allowed", origin)
		}
	})
}

func TestReceptionServer_TokenGuessing(t *testing.T) {
	t.Parallel()

	// openFrom sends an Open with a fresh unknown-looking token from ip.
	openFrom := func(t *testing.T, client scannerv1connect.ScannerServiceClient, ip string) error {
		t.Helper()
		req := connect.NewRequest(&scannerv1.OpenRequest{
			LinkToken: &entityv1.ScannerLinkToken{Value: entity.NewScanner("x").LinkToken},
			SignTime:  timestamppb.Now(),
			Signature: &entityv1.Signature{Value: make([]byte, 64)},
			PublicKey: &entityv1.PublicKey{Value: testutil.NewDeviceKey(t).PublicKey(t)},
		})
		req.Header().Set("X-Forwarded-For", ip)
		_, err := client.Open(context.Background(), req)
		return err
	}

	t.Run("token guessing", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/organizer/api/rpc/scanner-device "Token guessing"
		ts, scannerUC := newReceptionTestServer(t)
		unknown := apperr.Wrap(usecase.ErrUnknownScannerLinkToken, codes.PermissionDenied, "scanner refused")
		scannerUC.EXPECT().Open(mock.Anything, mock.Anything).Return(nil, unknown).Times(11)
		client := scannerv1connect.NewScannerServiceClient(ts.Client(), ts.URL)

		for i := range 10 {
			assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(openFrom(t, client, "203.0.113.7")), "call %d", i+1)
		}
		assert.Equal(t, connect.CodeResourceExhausted, connect.CodeOf(openFrom(t, client, "203.0.113.7")), "the 11th call fails")

		// Admit from the same client is refused too, before validation or any usecase.
		_, err := client.Admit(context.Background(), func() *connect.Request[scannerv1.AdmitRequest] {
			r := connect.NewRequest(&scannerv1.AdmitRequest{})
			r.Header().Set("X-Forwarded-For", "203.0.113.7")
			return r
		}())
		assert.Equal(t, connect.CodeResourceExhausted, connect.CodeOf(err))

		// Another client is not affected.
		assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(openFrom(t, client, "198.51.100.9")))
	})

	t.Run("refusals other than unknown tokens do not count", func(t *testing.T) {
		t.Parallel()
		ts, scannerUC := newReceptionTestServer(t)
		scannerUC.EXPECT().Open(mock.Anything, mock.Anything).
			Return(nil, apperr.New(codes.PermissionDenied, "scanner refused")).Times(12)
		client := scannerv1connect.NewScannerServiceClient(ts.Client(), ts.URL)
		for range 12 {
			assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(openFrom(t, client, "203.0.113.8")))
		}
	})
}

func TestOrganizerServer_NoPublicProcedures(t *testing.T) {
	t.Parallel()

	t.Run("not signed in", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/organizer/api/rpc/scanner "Not signed in"
		ts := newOrganizerTestServer(t)
		client := organizerscannerv1connect.NewScannerServiceClient(ts.Client(), ts.URL)
		_, err := client.Create(context.Background(), connect.NewRequest(&organizerscannerv1.CreateRequest{
			EventId: &entityv1.EventId{Value: "019a0000-0000-7000-8000-0000000000e1"},
		}))
		assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	})

	t.Run("the device-facing ScannerService is not served", func(t *testing.T) {
		t.Parallel()
		ts := newOrganizerTestServer(t)
		client := scannerv1connect.NewScannerServiceClient(ts.Client(), ts.URL)
		_, err := client.Open(context.Background(), openRequest(t))
		assert.Contains(t, []connect.Code{connect.CodeUnauthenticated, connect.CodeUnimplemented}, connect.CodeOf(err))
	})
}
