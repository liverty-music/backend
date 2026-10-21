package rpc_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/authn"
	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"connectrpc.com/validate"
	"github.com/liverty-music/backend/internal/infrastructure/auth"
	authmocks "github.com/liverty-music/backend/internal/infrastructure/auth/mocks"
	"github.com/stretchr/testify/mock"
)

// fanTestToken is the bearer token the fan boundary test server accepts as a
// signed-in caller whose subject is testCallerExtID.
const fanTestToken = "fan-token"

// newFanBoundaryServer serves one fan Connect service behind the same authn
// middleware (with the production public-procedure allowlist) and validation
// interceptor as the consumer server (with the claims bridge it installs).
// mount registers the service under test with the generated
// Register<Service>Handler.
func newFanBoundaryServer(t *testing.T, mount func(*connect.Server)) *httptest.Server {
	t.Helper()

	validator := authmocks.NewMockTokenValidator(t)
	validator.EXPECT().ValidateToken(mock.Anything, fanTestToken).
		Return(&auth.Claims{Sub: testCallerExtID}, nil).Maybe()

	server := connect.NewServer(auth.NewClaimsBridgeInterceptor(), validate.NewServerInterceptor())
	mount(server)
	mux := http.NewServeMux()
	connecthttp.Mount(mux, server)
	authFunc := auth.NewAuthFunc(validator, auth.FanPublicProcedures())
	srv := httptest.NewServer(authn.NewMiddleware(authFunc).Wrap(mux))
	t.Cleanup(srv.Close)
	return srv
}

// withFanToken returns the context and request of a call sent by the
// signed-in test caller.
func withFanToken[T any](req *T) (context.Context, *T) {
	ctx, info := connect.NewClientContext(context.Background())
	info.RequestHeader().Set("Authorization", "Bearer "+fanTestToken)
	return ctx, req
}
