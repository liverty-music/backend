package server_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	receptionv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/organizer/reception/v1/receptionv1connect"
	receptionlinkv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/organizer/reception_link/v1/reception_linkv1connect"
	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	receptionv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/organizer/reception/v1"
	receptionlinkv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/organizer/reception_link/v1"
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

const organizerOrigin = "https://organizer.liverty-music.test"

// newOrganizerTestServer builds the organizer Connect server the way
// di.InitializeApp does: the organizer AuthFunc with OrganizerPublicProcedures,
// the OrgScopedInterceptor exempting them, and the ReceptionLinkService and
// ReceptionService handlers over mocked use cases.
func newOrganizerTestServer(t *testing.T) (*httptest.Server, *usecasemocks.MockReceptionLinkUseCase) {
	t.Helper()
	logger, err := logging.New()
	require.NoError(t, err)

	rateLimiter := ratelimit.NewLimiter(ratelimit.Config{AuthRPS: 100, AuthBurst: 100, AnonRPS: 100, AnonBurst: 100}, time.Minute)
	t.Cleanup(func() { _ = rateLimiter.Close() })

	public := auth.OrganizerPublicProcedures()
	authFunc := auth.NewAuthFunc(authmocks.NewMockTokenValidator(t), public)
	throttle := ratelimit.NewUnknownTokenThrottle(10, 10*time.Minute, time.Now)
	interceptors := []connect.Interceptor{
		auth.NewOrgScopedInterceptor("organizer-console-project", public),
		ratelimit.NewUnknownTokenInterceptor(throttle, public, func(err error) bool {
			return errors.Is(err, usecase.ErrUnknownReceptionLinkToken)
		}),
	}

	linkUC := usecasemocks.NewMockReceptionLinkUseCase(t)
	ticketUC := usecasemocks.NewMockTicketUseCase(t)
	organizerUC := usecasemocks.NewMockOrganizerUseCase(t)
	healthHandler := func(_ ...connect.HandlerOption) (string, http.Handler) {
		return "/unused-health-check/", http.NotFoundHandler()
	}
	handlers := []server.RPCHandlerFunc{
		func(opts ...connect.HandlerOption) (string, http.Handler) {
			return receptionlinkv1connect.NewReceptionLinkServiceHandler(rpc.NewOrganizerReceptionLinkHandler(linkUC, organizerUC, logger), opts...)
		},
		func(opts ...connect.HandlerOption) (string, http.Handler) {
			return receptionv1connect.NewReceptionServiceHandler(rpc.NewReceptionHandler(linkUC, ticketUC, logger), opts...)
		},
	}
	cfg := config.ServerSettings{
		Host:              "127.0.0.1",
		AllowedOrigins:    []string{organizerOrigin},
		HandlerTimeout:    5 * time.Second,
		ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout:       2 * time.Second,
		IdleTimeout:       5 * time.Second,
	}
	srv := server.NewConnectServer(cfg, logger, authFunc, rateLimiter, healthHandler, interceptors, nil, nil, handlers...)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, linkUC
}

func TestOrganizerServer_Reception(t *testing.T) {
	t.Parallel()

	t.Run("staff device without an account", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/organizer/api/rpc/reception "Staff device without an account"
		ts, linkUC := newOrganizerTestServer(t)
		linkUC.EXPECT().Open(mock.Anything, mock.MatchedBy(func(in usecase.OpenReceptionLinkInput) bool {
			return in.LinkToken == "tok_0123456789abcdefghijklmn"
		})).Return(&usecase.OpenReceptionLinkResult{
			Link: &entity.ReceptionLink{ID: "019a0000-0000-7000-8000-0000000000a1", EventID: "019a0000-0000-7000-8000-0000000000e1", Number: 1, Status: entity.ReceptionLinkStatusInUse},
		}, nil)

		client := receptionv1connect.NewReceptionServiceClient(ts.Client(), ts.URL)
		resp, err := client.Open(context.Background(), connect.NewRequest(&receptionv1.OpenRequest{
			LinkToken: &entityv1.ReceptionLinkToken{Value: "tok_0123456789abcdefghijklmn"},
			SignTime:  timestamppb.Now(),
			Signature: &entityv1.Signature{Value: make([]byte, 64)},
			PublicKey: &entityv1.PublicKey{Value: testutil.NewDeviceKey(t).PublicKey(t)},
		}))
		require.NoError(t, err, "no bearer token is needed for the reception service")
		assert.Equal(t, int32(1), resp.Msg.ReceptionLink.Number.Value)
	})

	t.Run("malformed reception call is refused by validation", func(t *testing.T) {
		t.Parallel()
		ts, _ := newOrganizerTestServer(t)
		client := receptionv1connect.NewReceptionServiceClient(ts.Client(), ts.URL)
		_, err := client.Admit(context.Background(), connect.NewRequest(&receptionv1.AdmitRequest{
			LinkToken: &entityv1.ReceptionLinkToken{Value: "tok_0123456789abcdefghijklmn"},
			SignTime:  timestamppb.Now(),
			Signature: &entityv1.Signature{Value: make([]byte, 10)},
		}))
		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	})

	t.Run("not signed in", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/organizer/api/rpc/reception-link "Not signed in"
		ts, _ := newOrganizerTestServer(t)
		client := receptionlinkv1connect.NewReceptionLinkServiceClient(ts.Client(), ts.URL)
		_, err := client.Issue(context.Background(), connect.NewRequest(&receptionlinkv1.IssueRequest{
			EventId: &entityv1.EventId{Value: "019a0000-0000-7000-8000-0000000000e1"},
		}))
		assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	})

	t.Run("CORS preflight from the organizer web origin", func(t *testing.T) {
		t.Parallel()
		ts, _ := newOrganizerTestServer(t)
		req, err := http.NewRequest(http.MethodOptions, ts.URL+receptionv1connect.ReceptionServiceAdmitProcedure, nil)
		require.NoError(t, err)
		req.Header.Set("Origin", organizerOrigin)
		req.Header.Set("Access-Control-Request-Method", http.MethodPost)
		req.Header.Set("Access-Control-Request-Headers", "connect-protocol-version,content-type") // browsers send the list sorted
		resp, err := ts.Client().Do(req)
		require.NoError(t, err)
		_ = resp.Body.Close()
		assert.Equal(t, organizerOrigin, resp.Header.Get("Access-Control-Allow-Origin"))

		req.Header.Set("Origin", "https://evil.example")
		resp, err = ts.Client().Do(req)
		require.NoError(t, err)
		_ = resp.Body.Close()
		assert.Empty(t, resp.Header.Get("Access-Control-Allow-Origin"), "other origins are not allowed")
	})
}

func TestOrganizerServer_ReceptionTokenGuessing(t *testing.T) {
	t.Parallel()

	// openFrom sends an Open with a fresh unknown-looking token from ip.
	openFrom := func(t *testing.T, client receptionv1connect.ReceptionServiceClient, ip string) error {
		t.Helper()
		req := connect.NewRequest(&receptionv1.OpenRequest{
			LinkToken: &entityv1.ReceptionLinkToken{Value: entity.NewReceptionLink("x").Token},
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
		// @spec components/adapter/organizer/api/rpc/reception "Token guessing"
		ts, linkUC := newOrganizerTestServer(t)
		unknown := apperr.Wrap(usecase.ErrUnknownReceptionLinkToken, codes.PermissionDenied, "reception link refused")
		linkUC.EXPECT().Open(mock.Anything, mock.Anything).Return(nil, unknown).Times(11)
		client := receptionv1connect.NewReceptionServiceClient(ts.Client(), ts.URL)

		for i := range 10 {
			assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(openFrom(t, client, "203.0.113.7")), "call %d", i+1)
		}
		assert.Equal(t, connect.CodeResourceExhausted, connect.CodeOf(openFrom(t, client, "203.0.113.7")), "the 11th call fails")

		// Admit from the same client is refused too, before validation or any usecase.
		_, err := client.Admit(context.Background(), func() *connect.Request[receptionv1.AdmitRequest] {
			r := connect.NewRequest(&receptionv1.AdmitRequest{})
			r.Header().Set("X-Forwarded-For", "203.0.113.7")
			return r
		}())
		assert.Equal(t, connect.CodeResourceExhausted, connect.CodeOf(err))

		// Another client is not affected.
		assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(openFrom(t, client, "198.51.100.9")))
	})

	t.Run("refusals other than unknown tokens do not count", func(t *testing.T) {
		t.Parallel()
		ts, linkUC := newOrganizerTestServer(t)
		linkUC.EXPECT().Open(mock.Anything, mock.Anything).
			Return(nil, apperr.New(codes.PermissionDenied, "reception link refused")).Times(12)
		client := receptionv1connect.NewReceptionServiceClient(ts.Client(), ts.URL)
		for range 12 {
			assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(openFrom(t, client, "203.0.113.8")))
		}
	})
}
