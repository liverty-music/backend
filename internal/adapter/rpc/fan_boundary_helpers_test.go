package rpc_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/authn"
	"connectrpc.com/connect"
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
// mount is the generated
// New<Service>Handler bound to the handler under test.
func newFanBoundaryServer(t *testing.T, mount func(opts ...connect.HandlerOption) (string, http.Handler)) *httptest.Server {
	t.Helper()

	validator := authmocks.NewMockTokenValidator(t)
	validator.EXPECT().ValidateToken(mock.Anything, fanTestToken).
		Return(&auth.Claims{Sub: testCallerExtID}, nil).Maybe()

	path, svc := mount(connect.WithInterceptors(auth.ClaimsBridgeInterceptor{}, validate.NewInterceptor()))
	mux := http.NewServeMux()
	mux.Handle(path, svc)
	authFunc := auth.NewAuthFunc(validator, auth.FanPublicProcedures())
	srv := httptest.NewServer(authn.NewMiddleware(authFunc).Wrap(mux))
	t.Cleanup(srv.Close)
	return srv
}

// withFanToken marks a client request as sent by the signed-in test caller.
func withFanToken[T any](req *connect.Request[T]) *connect.Request[T] {
	req.Header().Set("Authorization", "Bearer "+fanTestToken)
	return req
}
