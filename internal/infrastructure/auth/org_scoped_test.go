package auth_test

import (
	"context"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/liverty-music/backend/internal/infrastructure/auth"
	"github.com/liverty-music/backend/pkg/authctx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// projectID is the organizer-console project id used in all interceptor tests.
const projectID = "proj-organizer-console-123"

// orgID is the Zitadel org id used in happy-path tests.
const orgID = "org-abc-456"

// buildClaims is a helper that creates Claims with the given overrides applied
// on top of a valid happy-path baseline.
type claimsOption func(*authctx.Claims)

func withAudiences(auds ...string) claimsOption {
	return func(c *authctx.Claims) { c.Audiences = auds }
}
func withLoginScopeOrgID(id string) claimsOption {
	return func(c *authctx.Claims) { c.LoginScopeOrgID = id }
}
func withRoleOrgIDs(ids ...string) claimsOption {
	return func(c *authctx.Claims) {
		m := make(map[string]struct{}, len(ids))
		for _, id := range ids {
			m[id] = struct{}{}
		}
		c.RoleOrgIDs = m
	}
}

func happyClaims(opts ...claimsOption) *authctx.Claims {
	c := &authctx.Claims{
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
func contextWithClaims(claims *authctx.Claims) context.Context {
	return authctx.WithClaims(context.Background(), claims)
}

func TestOrgScopedInterceptor(t *testing.T) {
	t.Parallel()

	type args struct {
		claims    *authctx.Claims // nil = no claims in context
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
			// The caller's org comes from the token alone (its single
			// login-scope org), never from an email domain or a URL hint.
			// @spec components/infrastructure/organizer/web/global/hosted-login "Organization is resolved by org-pinned entry for every member"
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
			// Simulates a multi-org member whose token has roles for two orgs;
			// extractRoleOrgIDs returns an empty LoginScopeOrgID when len > 1.
			// @spec components/adapter/organizer/api/rpc/payout-account "Roles in several Organizations"
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
			// @spec components/infrastructure/organizer/web/global/hosted-login "Organization is resolved by org-pinned entry for every member"
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

	// The v1 interceptor applied the same checks to unary and streaming
	// handlers; the v2 interceptor must keep doing so for every stream type.
	for _, streamType := range allStreamTypes {
		for _, tt := range tests {
			t.Run(streamTypeName(streamType)+"/"+tt.name, func(t *testing.T) {
				t.Parallel()

				var ctx context.Context
				if tt.args.claims != nil {
					ctx = contextWithClaims(tt.args.claims)
				} else {
					ctx = context.Background()
				}

				interceptor := auth.NewOrgScopedInterceptor(tt.args.projectID, nil)

				var capturedCallerOrgID string
				next := connect.ServerFunc(func(innerCtx context.Context, _ connect.Spec, _ connect.ServerStream) error {
					capturedCallerOrgID, _ = authctx.GetCallerOrgID(innerCtx)
					return nil
				})

				err := interceptor(next)(ctx, connect.Spec{Procedure: "/test.v1.Service/Method", StreamType: streamType}, nil)

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
}

// TestOrgScopedInterceptor_PublicProcedures verifies that a public procedure
// passes through without claims and carries no caller org id, while any other
// procedure is still checked.
func TestOrgScopedInterceptor_PublicProcedures(t *testing.T) {
	t.Parallel()

	const publicProcedure = "/test.v1.Service/Public"
	tests := []struct {
		name      string
		procedure string
		wantNext  bool
		wantCode  connect.Code
	}{
		{name: "pass a public procedure through without claims", procedure: publicProcedure, wantNext: true},
		{name: "deny a non-public procedure without claims", procedure: "/test.v1.Service/Private", wantCode: connect.CodePermissionDenied},
	}

	for _, streamType := range allStreamTypes {
		for _, tt := range tests {
			t.Run(streamTypeName(streamType)+"/"+tt.name, func(t *testing.T) {
				t.Parallel()

				interceptor := auth.NewOrgScopedInterceptor(projectID, map[string]bool{publicProcedure: true})

				nextCalled := false
				var hasCallerOrgID bool
				next := connect.ServerFunc(func(innerCtx context.Context, _ connect.Spec, _ connect.ServerStream) error {
					nextCalled = true
					_, hasCallerOrgID = authctx.GetCallerOrgID(innerCtx)
					return nil
				})

				err := interceptor(next)(context.Background(), connect.Spec{Procedure: tt.procedure, StreamType: streamType}, nil)

				assert.Equal(t, tt.wantNext, nextCalled)
				if !tt.wantNext {
					assert.Equal(t, tt.wantCode, connect.CodeOf(err))
					return
				}
				require.NoError(t, err)
				assert.False(t, hasCallerOrgID, "a public procedure must carry no caller org id")
			})
		}
	}
}
