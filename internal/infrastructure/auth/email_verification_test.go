package auth_test

import (
	"context"
	"net/http"
	"testing"

	"connectrpc.com/connect"
	"github.com/liverty-music/backend/internal/infrastructure/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubStreamingHandlerConn is a minimal no-op implementation of
// connect.StreamingHandlerConn used in streaming interceptor tests.
type stubStreamingHandlerConn struct{}

func (stubStreamingHandlerConn) Spec() connect.Spec             { return connect.Spec{} }
func (stubStreamingHandlerConn) Peer() connect.Peer             { return connect.Peer{} }
func (stubStreamingHandlerConn) Receive(any) error              { return nil }
func (stubStreamingHandlerConn) RequestHeader() http.Header     { return http.Header{} }
func (stubStreamingHandlerConn) Send(any) error                 { return nil }
func (stubStreamingHandlerConn) ResponseHeader() http.Header    { return http.Header{} }
func (stubStreamingHandlerConn) ResponseTrailer() http.Header   { return http.Header{} }

func TestEmailVerificationInterceptor_WrapUnary(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		claims  *auth.Claims
		wantErr bool
		wantCode connect.Code
	}{
		{
			name: "pass through when claims are nil (public endpoint)",
			claims:  nil,
			wantErr: false,
		},
		{
			name: "pass through when email is empty (machine user)",
			claims: &auth.Claims{
				Sub:           "machine-user",
				Email:         "",
				EmailVerified: false,
			},
			wantErr: false,
		},
		{
			name: "pass through when email is verified",
			claims: &auth.Claims{
				Sub:           "user-123",
				Email:         "verified@example.com",
				EmailVerified: true,
			},
			wantErr: false,
		},
		{
			name: "block unverified human user",
			claims: &auth.Claims{
				Sub:           "user-456",
				Email:         "unverified@example.com",
				EmailVerified: false,
			},
			wantErr:  true,
			wantCode: connect.CodeUnauthenticated,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			interceptor := auth.EmailVerificationInterceptor{}

			handlerCalled := false
			next := func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
				handlerCalled = true
				return connect.NewResponse(&testMsg{}), nil
			}

			wrapped := interceptor.WrapUnary(next)

			ctx := context.Background()
			if tt.claims != nil {
				ctx = auth.WithClaims(ctx, tt.claims)
			}
			req := connect.NewRequest(&testMsg{})

			_, err := wrapped(ctx, req)

			if tt.wantErr {
				require.Error(t, err)
				assert.Equal(t, tt.wantCode, connect.CodeOf(err))
				assert.False(t, handlerCalled, "handler must not be called on blocked request")
			} else {
				require.NoError(t, err)
				assert.True(t, handlerCalled, "handler must be called on allowed request")
			}
		})
	}
}

func TestEmailVerificationInterceptor_WrapStreamingHandler(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		claims  *auth.Claims
		wantErr bool
		wantCode connect.Code
	}{
		{
			name:    "pass through when claims are nil (public endpoint)",
			claims:  nil,
			wantErr: false,
		},
		{
			name: "pass through when email is empty (machine user)",
			claims: &auth.Claims{
				Sub:           "machine-user",
				Email:         "",
				EmailVerified: false,
			},
			wantErr: false,
		},
		{
			name: "pass through when email is verified",
			claims: &auth.Claims{
				Sub:           "user-123",
				Email:         "verified@example.com",
				EmailVerified: true,
			},
			wantErr: false,
		},
		{
			name: "block unverified human user",
			claims: &auth.Claims{
				Sub:           "user-456",
				Email:         "unverified@example.com",
				EmailVerified: false,
			},
			wantErr:  true,
			wantCode: connect.CodeUnauthenticated,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			interceptor := auth.EmailVerificationInterceptor{}

			handlerCalled := false
			next := func(ctx context.Context, conn connect.StreamingHandlerConn) error {
				handlerCalled = true
				return nil
			}

			wrapped := interceptor.WrapStreamingHandler(next)

			ctx := context.Background()
			if tt.claims != nil {
				ctx = auth.WithClaims(ctx, tt.claims)
			}

			err := wrapped(ctx, stubStreamingHandlerConn{})

			if tt.wantErr {
				require.Error(t, err)
				assert.Equal(t, tt.wantCode, connect.CodeOf(err))
				assert.False(t, handlerCalled, "handler must not be called on blocked request")
			} else {
				require.NoError(t, err)
				assert.True(t, handlerCalled, "handler must be called on allowed request")
			}
		})
	}
}

func TestEmailVerificationInterceptor_WrapStreamingClient_IsNoOp(t *testing.T) {
	t.Parallel()

	interceptor := auth.EmailVerificationInterceptor{}

	var nextCalled bool
	next := func(ctx context.Context, spec connect.Spec) connect.StreamingClientConn {
		nextCalled = true
		return nil
	}

	wrapped := interceptor.WrapStreamingClient(next)
	wrapped(context.Background(), connect.Spec{})

	assert.True(t, nextCalled, "WrapStreamingClient must be a pass-through no-op")
}
