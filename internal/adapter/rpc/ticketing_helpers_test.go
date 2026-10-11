package rpc_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	"connectrpc.com/validate"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/auth"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	apperrconnect "github.com/pannpers/go-apperr/apperr/connect"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// IDs shared by the ticketing handler tests.
const (
	tkSeriesID     = "019a0000-0000-7000-8000-0000000000c1"
	tkSaleID       = "019a0000-0000-7000-8000-0000000000c2"
	tkTicketTypeID = "019a0000-0000-7000-8000-0000000000c3"
	tkEntryID      = "019a0000-0000-7000-8000-0000000000c4"
	tkCheckoutID   = "019a0000-0000-7000-8000-0000000000c5"
	tkOrderID      = "019a0000-0000-7000-8000-0000000000c6"
	tkFanSub       = "ext-fan"
	tkFanUserID    = "019a0000-0000-7000-8000-0000000000c7"
)

// serveValidated serves one Connect service behind the production error
// mapping and protovalidate interceptors, after an interceptor standing in for
// the authn middleware that applies decorate to the request context.
func serveValidated(
	t *testing.T,
	mount func(opts ...connect.HandlerOption) (string, http.Handler),
	decorate func(context.Context) context.Context,
) *httptest.Server {
	t.Helper()
	withCtx := connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			return next(decorate(ctx), req)
		}
	})
	logger, err := logging.New()
	require.NoError(t, err)
	path, svc := mount(connect.WithInterceptors(apperrconnect.NewErrorHandlingInterceptor(logger), withCtx, validate.NewInterceptor()))
	mux := http.NewServeMux()
	mux.Handle(path, svc)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// signedInAs returns a decorator that signs the request in as sub.
func signedInAs(sub string) func(context.Context) context.Context {
	return func(ctx context.Context) context.Context {
		return auth.WithClaims(ctx, &auth.Claims{Sub: sub})
	}
}

// asOperator returns a decorator that signs the request in as an operator of
// the given Zitadel tenant.
func asOperator(zitadelOrgID string) func(context.Context) context.Context {
	return func(ctx context.Context) context.Context {
		return auth.WithCallerOrgID(auth.WithClaims(ctx, &auth.Claims{Sub: "op-sub"}), zitadelOrgID)
	}
}

// fanCtx returns a context signed in as sub.
func fanCtx(sub string) context.Context {
	return signedInAs(sub)(context.Background())
}

// expectFan makes the signed-in tkFanSub resolve to the stored tkFanUserID.
func expectFan(users *ucmocks.MockUserUseCase) {
	users.EXPECT().ResolveCaller(mock.Anything, tkFanSub).Return(&entity.User{ID: tkFanUserID, ExternalID: tkFanSub}, nil)
}

// expectNoAccount makes sub resolve to no stored account.
func expectNoAccount(users *ucmocks.MockUserUseCase, sub string) {
	users.EXPECT().ResolveCaller(mock.Anything, sub).Return(nil, apperr.New(codes.NotFound, "no user"))
}
