package auth_test

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/liverty-music/backend/internal/infrastructure/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// projectID is the organizer-console project id used in all interceptor tests.
const projectID = "proj-organizer-console-123"

// orgID is the Zitadel org id used in happy-path tests.
const orgID = "org-abc-456"

// buildClaims is a helper that creates Claims with the given overrides applied
// on top of a valid happy-path baseline.
type claimsOption func(*auth.Claims)

func withAudiences(auds ...string) claimsOption {
	return func(c *auth.Claims) { c.Audiences = auds }
}
func withLoginScopeOrgID(id string) claimsOption {
	return func(c *auth.Claims) { c.LoginScopeOrgID = id }
}
func withRoleOrgIDs(ids ...string) claimsOption {
	return func(c *auth.Claims) {
		m := make(map[string]struct{}, len(ids))
		for _, id := range ids {
			m[id] = struct{}{}
		}
		c.RoleOrgIDs = m
	}
}

func happyClaims(opts ...claimsOption) *auth.Claims {
	c := &auth.Claims{
		Sub:             "user-sub",
		Email:           "op@example.com",
		Audiences:       []string{projectID},
		LoginScopeOrgID: orgID,
		RoleOrgIDs:      map[string]struct{}{orgID: {}},
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// contextWithClaims injects Claims as if the ClaimsBridgeInterceptor ran.
func contextWithClaims(claims *auth.Claims) context.Context {
	return auth.WithClaims(context.Background(), claims)
}

func TestOrgScopedInterceptor_WrapUnary(t *testing.T) {
	t.Parallel()

	type args struct {
		claims    *auth.Claims // nil = no claims in context
		projectID string
	}
	tests := []struct {
		name            string
		args            args
		wantCode        connect.Code
		wantCallerOrgID string // non-empty on success
		wantErr         bool
	}{
		// ── Happy path ──────────────────────────────────────────────────────
		{
			name: "allow when aud contains project id, exactly one login-scope org, and role matches",
			args: args{
				claims:    happyClaims(),
				projectID: projectID,
			},
			wantCallerOrgID: orgID,
		},

		// ── Aud check ────────────────────────────────────────────────────────
		{
			name: "deny when aud does not contain the project id",
			args: args{
				claims:    happyClaims(withAudiences("other-project")),
				projectID: projectID,
			},
			wantErr:  true,
			wantCode: connect.CodePermissionDenied,
		},
		{
			name: "deny when aud is empty",
			args: args{
				claims:    happyClaims(withAudiences()),
				projectID: projectID,
			},
			wantErr:  true,
			wantCode: connect.CodePermissionDenied,
		},

		// ── Login-scope org cardinality ──────────────────────────────────────
		{
			name: "deny when login-scope org id is empty (zero unique inner orgIds)",
			args: args{
				claims:    happyClaims(withLoginScopeOrgID(""), withRoleOrgIDs()),
				projectID: projectID,
			},
			wantErr:  true,
			wantCode: connect.CodePermissionDenied,
		},
		{
			// Simulates a multi-org operator whose token has roles for two orgs;
			// extractRoleOrgIDs returns an empty LoginScopeOrgID when len > 1.
			name: "deny when multiple login-scope orgs are present (ambiguous)",
			args: args{
				claims:    happyClaims(withLoginScopeOrgID(""), withRoleOrgIDs("org-A", "org-B")),
				projectID: projectID,
			},
			wantErr:  true,
			wantCode: connect.CodePermissionDenied,
		},

		// ── Role cross-check ─────────────────────────────────────────────────
		{
			// LoginScopeOrgID points to org-X but RoleOrgIDs only contains org-Y.
			// This exercises the explicit cross-check in authorize().
			name: "deny when login-scope org does not appear in role-claim org ids",
			args: args{
				claims: happyClaims(
					withLoginScopeOrgID("org-X"),
					withRoleOrgIDs("org-Y"),
				),
				projectID: projectID,
			},
			wantErr:  true,
			wantCode: connect.CodePermissionDenied,
		},
		{
			name: "deny when role-claim org ids is empty",
			args: args{
				claims:    happyClaims(withRoleOrgIDs()),
				projectID: projectID,
			},
			wantErr:  true,
			wantCode: connect.CodePermissionDenied,
		},

		// ── No claims ────────────────────────────────────────────────────────
		{
			name: "deny when no claims in context (defence-in-depth)",
			args: args{
				claims:    nil,
				projectID: projectID,
			},
			wantErr:  true,
			wantCode: connect.CodePermissionDenied,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var ctx context.Context
			if tt.args.claims != nil {
				ctx = contextWithClaims(tt.args.claims)
			} else {
				ctx = context.Background()
			}

			interceptor := auth.NewOrgScopedInterceptor(tt.args.projectID)

			var capturedCallerOrgID string
			next := connect.UnaryFunc(func(innerCtx context.Context, _ connect.AnyRequest) (connect.AnyResponse, error) {
				capturedCallerOrgID, _ = auth.GetCallerOrgID(innerCtx)
				return connect.NewResponse(&struct{}{}), nil
			})

			_, err := interceptor.WrapUnary(next)(ctx, connect.NewRequest(&struct{}{}))

			if tt.wantErr {
				require.Error(t, err)
				var connErr *connect.Error
				require.ErrorAs(t, err, &connErr)
				assert.Equal(t, tt.wantCode, connErr.Code(), "unexpected connect code")
				assert.Empty(t, capturedCallerOrgID, "handler must not run when authorization fails")
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantCallerOrgID, capturedCallerOrgID,
				"caller org id must be propagated to handler context")
		})
	}
}

// TestOrgScopedInterceptor_WrapStreamingHandler verifies the streaming path
// uses the same authorization logic as the unary path.
func TestOrgScopedInterceptor_WrapStreamingHandler(t *testing.T) {
	t.Parallel()

	t.Run("deny when aud missing project id (streaming)", func(t *testing.T) {
		t.Parallel()

		ctx := contextWithClaims(happyClaims(withAudiences("wrong")))
		interceptor := auth.NewOrgScopedInterceptor(projectID)

		nextCalled := false
		next := connect.StreamingHandlerFunc(func(_ context.Context, _ connect.StreamingHandlerConn) error {
			nextCalled = true
			return nil
		})

		err := interceptor.WrapStreamingHandler(next)(ctx, nil)
		require.Error(t, err)
		var connErr *connect.Error
		require.ErrorAs(t, err, &connErr)
		assert.Equal(t, connect.CodePermissionDenied, connErr.Code())
		assert.False(t, nextCalled)
	})

	t.Run("allow valid streaming request", func(t *testing.T) {
		t.Parallel()

		ctx := contextWithClaims(happyClaims())
		interceptor := auth.NewOrgScopedInterceptor(projectID)

		nextCalled := false
		next := connect.StreamingHandlerFunc(func(_ context.Context, _ connect.StreamingHandlerConn) error {
			nextCalled = true
			return nil
		})

		err := interceptor.WrapStreamingHandler(next)(ctx, nil)
		require.NoError(t, err)
		assert.True(t, nextCalled)
	})
}

// TestGetCallerOrgID verifies that WithCallerOrgID / GetCallerOrgID round-trip correctly.
func TestGetCallerOrgID(t *testing.T) {
	t.Parallel()

	t.Run("return org id when set", func(t *testing.T) {
		t.Parallel()
		ctx := auth.WithCallerOrgID(context.Background(), "my-org")
		id, ok := auth.GetCallerOrgID(ctx)
		assert.True(t, ok)
		assert.Equal(t, "my-org", id)
	})

	t.Run("return false when not set", func(t *testing.T) {
		t.Parallel()
		_, ok := auth.GetCallerOrgID(context.Background())
		assert.False(t, ok)
	})

	t.Run("return false when set to empty string", func(t *testing.T) {
		t.Parallel()
		ctx := auth.WithCallerOrgID(context.Background(), "")
		_, ok := auth.GetCallerOrgID(ctx)
		assert.False(t, ok)
	})
}
