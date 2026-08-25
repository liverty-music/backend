package auth

import (
	"context"
	"errors"
	"slices"

	"connectrpc.com/connect"
)

// callerOrgIDKey is the context key under which the resolved caller Zitadel
// org id is stored after the org-scoped interceptor validates the token.
type callerOrgIDKey struct{}

// WithCallerOrgID returns a new context carrying the resolved caller Zitadel
// org id. Called by OrgScopedInterceptor after successful validation.
func WithCallerOrgID(ctx context.Context, orgID string) context.Context {
	return context.WithValue(ctx, callerOrgIDKey{}, orgID)
}

// GetCallerOrgID retrieves the caller's Zitadel org id that was resolved and
// stored by OrgScopedInterceptor. Returns ("", false) when the value is absent
// (i.e. the request did not pass through the interceptor or failed validation).
func GetCallerOrgID(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(callerOrgIDKey{}).(string)
	return v, ok && v != ""
}

// OrgScopedInterceptor enforces org-scoped authorization for the organizer
// Connect server. It runs as an extraInterceptor (after ClaimsBridgeInterceptor,
// before the validation interceptor) and performs the following TOKEN-LEVEL
// checks:
//
//  1. The token's "aud" claim includes the configured organizer-console project id.
//  2. Exactly one login-scope org id is present (derived from the unique inner
//     orgId in the project-scoped roles claim). Zero or multiple → denied.
//  3. The login-scope org id equals at least one of the orgIds under which the
//     operator holds a role in the organizer-console project. Any role suffices.
//
// On success it stores the resolved caller Zitadel org id in the context via
// WithCallerOrgID so handlers can call GetCallerOrgID without re-reading claims.
//
// Organizer resolution (GetByZitadelOrgID) and status checks happen in the
// handlers because they require the repository. This interceptor does only the
// token-level checks.
//
// All failures return connect.CodePermissionDenied (non-revealing). The
// UNAUTHENTICATED case is already handled by the upstream authn middleware
// before this interceptor runs.
//
// # Login-scope org assumption
//
// Zitadel does not expose a dedicated top-level claim for the login-scope org.
// When an operator scopes their session to org A via the OIDC scope
// "urn:zitadel:iam:org:id:<A>", Zitadel filters the role grants in the
// project-scoped roles claim to only those held within org A. As a result,
// the inner orgId under the project-scoped roles claim is exactly the
// login-scope org. The interceptor infers the login-scope org by collecting
// the unique inner orgId across all role entries in the project-scoped claim
// — a single unique orgId is the login-scope org; anything else is rejected.
//
// VERIFICATION: Confirm with a real Zitadel token from the organizer-console
// app. If Zitadel introduces a dedicated token claim for the login-scope org
// in a future version, update Claims.LoginScopeOrgID (jwt_validator.go
// extractRoleOrgIDs) to read it directly and add a test against the new shape.
type OrgScopedInterceptor struct {
	// organizerConsoleProjectID is the Zitadel project id for the
	// organizer-console application. It must be present in the token's "aud"
	// claim for every request that reaches this interceptor.
	organizerConsoleProjectID string
}

// NewOrgScopedInterceptor creates an OrgScopedInterceptor that requires the
// given organizerConsoleProjectID in the token's audience claim.
func NewOrgScopedInterceptor(organizerConsoleProjectID string) OrgScopedInterceptor {
	return OrgScopedInterceptor{organizerConsoleProjectID: organizerConsoleProjectID}
}

// WrapUnary enforces org-scoped authorization for unary RPCs.
func (i OrgScopedInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		ctx, err := i.authorize(ctx)
		if err != nil {
			return nil, err
		}
		return next(ctx, req)
	}
}

// WrapStreamingClient is a no-op: the organizer server has no client-streaming RPCs.
func (i OrgScopedInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

// WrapStreamingHandler enforces org-scoped authorization for streaming RPCs.
func (i OrgScopedInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		ctx, err := i.authorize(ctx)
		if err != nil {
			return err
		}
		return next(ctx, conn)
	}
}

// authorize performs the token-level org-scoped checks and, on success,
// returns a context enriched with the caller's Zitadel org id.
func (i OrgScopedInterceptor) authorize(ctx context.Context) (context.Context, error) {
	denied := func() (context.Context, error) {
		return ctx, connect.NewError(connect.CodePermissionDenied, errors.New("permission denied"))
	}

	claims, ok := GetClaims(ctx)
	if !ok || claims == nil {
		// Missing claims should be caught by the authn layer (UNAUTHENTICATED).
		// Returning PERMISSION_DENIED here is a defence-in-depth fallback for
		// cases where the interceptor is misconfigured or invoked directly.
		return denied()
	}

	// Check 1: the organizer-console project id must be in "aud".
	if !slices.Contains(claims.Audiences, i.organizerConsoleProjectID) {
		return denied()
	}

	// Check 2: exactly one login-scope org must be derivable from the
	// project-scoped roles claim (LoginScopeOrgID is empty when zero or
	// multiple unique inner orgIds are found).
	if claims.LoginScopeOrgID == "" {
		return denied()
	}

	// Check 3: the login-scope org must be one for which the operator holds
	// a role (i.e. it appears as an inner orgId in the project-scoped roles
	// claim). When RoleOrgIDs contains exactly one entry and LoginScopeOrgID
	// was derived from it, this check is logically implied — but we perform
	// it explicitly for clarity and safety (future code paths may set
	// LoginScopeOrgID from a different source).
	if _, hasRole := claims.RoleOrgIDs[claims.LoginScopeOrgID]; !hasRole {
		return denied()
	}

	return WithCallerOrgID(ctx, claims.LoginScopeOrgID), nil
}
