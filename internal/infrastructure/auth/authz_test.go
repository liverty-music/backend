package auth_test

import (
	"context"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/liverty-music/backend/internal/infrastructure/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRequireRoleInterceptor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		ctx      func() context.Context
		wantErr  bool
		wantCode connect.Code
		wantNext bool
	}{
		{
			name: "allow caller holding the required role",
			ctx: func() context.Context {
				return auth.WithClaims(context.Background(), &auth.Claims{
					Sub:   "admin-user",
					Roles: []string{"admin"},
				})
			},
			wantNext: true,
		},
		{
			name: "deny authenticated caller without the role",
			ctx: func() context.Context {
				return auth.WithClaims(context.Background(), &auth.Claims{
					Sub:   "regular-user",
					Roles: []string{"viewer"},
				})
			},
			wantErr:  true,
			wantCode: connect.CodePermissionDenied,
		},
		{
			name: "deny unauthenticated caller (no claims)",
			ctx: func() context.Context {
				return context.Background()
			},
			wantErr:  true,
			wantCode: connect.CodeUnauthenticated,
		},
	}

	// The v1 interceptor checked the role for unary and streaming handlers
	// alike; the v2 interceptor must keep doing so for every stream type.
	for _, streamType := range allStreamTypes {
		for _, tt := range tests {
			t.Run(streamTypeName(streamType)+"/"+tt.name, func(t *testing.T) {
				t.Parallel()

				nextCalled := false
				next := connect.ServerFunc(func(_ context.Context, _ connect.Spec, _ connect.ServerStream) error {
					nextCalled = true
					return nil
				})

				interceptor := auth.NewRequireRoleInterceptor("admin")
				err := interceptor(next)(tt.ctx(), connect.Spec{Procedure: "/test.v1.Service/Method", StreamType: streamType}, nil)

				if tt.wantErr {
					require.Error(t, err)
					var connErr *connect.Error
					require.ErrorAs(t, err, &connErr)
					assert.Equal(t, tt.wantCode, connErr.Code())
					assert.False(t, nextCalled, "handler must not run when authorization fails")
					return
				}
				require.NoError(t, err)
				assert.Equal(t, tt.wantNext, nextCalled)
			})
		}
	}
}

// allStreamTypes lists every Connect stream type a server interceptor wraps.
var allStreamTypes = []connect.StreamType{
	connect.StreamTypeUnary,
	connect.StreamTypeClient,
	connect.StreamTypeServer,
	connect.StreamTypeBidi,
}

// streamTypeName names a stream type for subtest names.
func streamTypeName(st connect.StreamType) string {
	switch st {
	case connect.StreamTypeUnary:
		return "unary"
	case connect.StreamTypeClient:
		return "client_stream"
	case connect.StreamTypeServer:
		return "server_stream"
	default:
		return "bidi_stream"
	}
}
