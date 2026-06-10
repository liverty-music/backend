package auth_test

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/liverty-music/backend/internal/infrastructure/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRequireRoleInterceptor_WrapUnary(t *testing.T) {
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
			wantCode: connect.CodePermissionDenied,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			nextCalled := false
			next := connect.UnaryFunc(func(_ context.Context, _ connect.AnyRequest) (connect.AnyResponse, error) {
				nextCalled = true
				return connect.NewResponse(&struct{}{}), nil
			})

			interceptor := auth.NewRequireRoleInterceptor("admin")
			_, err := interceptor.WrapUnary(next)(tt.ctx(), connect.NewRequest(&struct{}{}))

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
