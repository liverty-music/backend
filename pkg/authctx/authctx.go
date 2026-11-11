// Package authctx carries the signed-in caller on a request context. The
// authentication interceptors (internal/infrastructure/auth) write it, and
// the request handlers (internal/adapter) read it.
package authctx

import (
	"context"
	"slices"
)

// Claims represents JWT claims extracted from the token.
type Claims struct {
	// Sub is the subject claim (provider_user_id/user ID from identity provider).
	Sub string
	// Email is the user's email address.
	Email string
	// Name is the user's display name.
	Name string
	// EmailVerified indicates whether the user's email address has been verified.
	// This is extracted from the email_verified private claim set by a Zitadel Action.
	// Defaults to false when the claim is absent (fail-closed).
	EmailVerified bool
	// Roles is the list of Zitadel project role names the caller holds.
	// Populated from both the global claim
	// "urn:zitadel:iam:org:project:roles" and any project-scoped claim
	// "urn:zitadel:iam:org:project:{id}:roles". Each claim is a JSON object
	// whose keys are role names; the values (org-id→domain maps) are ignored.
	// An empty slice means the token carries no role grants.
	Roles []string

	// Audiences is the list of audience values from the token's "aud" claim.
	// Used by the org-scoped interceptor to verify that the organizer-console
	// project id is present before authorizing organizer RPCs.
	Audiences []string

	// LoginScopeOrgID is the Zitadel org id extracted from the login-scope
	// scope claim "urn:zitadel:iam:org:id:<orgId>".
	//
	// Zitadel encodes the login-scope org (the org the user logged in against)
	// in the roles claim rather than as a separate top-level claim: when a
	// member scopes their session to org A with
	// "urn:zitadel:iam:org:id:<A>", Zitadel filters the role grants to only
	// those held within org A, so the roles claim contains exactly one unique
	// inner orgId — the login-scope org. This field captures that org id so the
	// org-scoped interceptor does not have to re-derive it from the roles map.
	//
	// ASSUMPTION: Zitadel does not surface a separate "urn:zitadel:iam:org:id:<id>"
	// claim in the token body; the login-scope org is inferred by reading
	// the unique inner orgId across all role grants in the project-scoped
	// roles claim. If exactly one unique inner orgId is present, it is the
	// login-scope org. Zero or multiple → LoginScopeOrgID is empty, and the
	// org-scoped interceptor rejects the request.
	// VERIFICATION: Test with a real Zitadel token to confirm. If Zitadel does
	// surface a dedicated scope claim in the token body, update extractLoginScopeOrgID
	// to read it directly and remove the roles-claim inference.
	LoginScopeOrgID string

	// RoleOrgIDs is the set of Zitadel org ids under which the member holds
	// at least one role. Populated from the project-scoped roles claim
	// "urn:zitadel:iam:org:project:{projectId}:roles". The inner orgId values
	// are preserved here (unlike the flattened Roles slice) so the org-scoped
	// interceptor can verify the caller holds a role specifically for the
	// login-scope org.
	RoleOrgIDs map[string]struct{}
}

// HasRole reports whether the caller holds the named Zitadel project role.
func (c *Claims) HasRole(role string) bool {
	return slices.Contains(c.Roles, role)
}

// Subject returns the subject claim (external user ID from identity provider).
// This satisfies the ratelimit.SubjectProvider interface.
func (c *Claims) Subject() string {
	return c.Sub
}

// claimsKey is the context key under which the caller's claims are stored.
type claimsKey struct{}

// WithClaims returns a new context with the given JWT claims.
func WithClaims(ctx context.Context, claims *Claims) context.Context {
	return context.WithValue(ctx, claimsKey{}, claims)
}

// GetClaims retrieves the JWT claims from the context.
// Returns the claims and true if found, or nil and false if not found.
func GetClaims(ctx context.Context) (*Claims, bool) {
	claims, ok := ctx.Value(claimsKey{}).(*Claims)
	return claims, ok
}

// callerOrgIDKey is the context key under which the resolved caller Zitadel
// org id is stored after the org-scoped interceptor validates the token.
type callerOrgIDKey struct{}

// WithCallerOrgID returns a new context carrying the resolved caller Zitadel
// org id. Called by the org-scoped interceptor after successful validation.
func WithCallerOrgID(ctx context.Context, orgID string) context.Context {
	return context.WithValue(ctx, callerOrgIDKey{}, orgID)
}

// GetCallerOrgID retrieves the caller's Zitadel org id that was resolved and
// stored by the org-scoped interceptor. Returns ("", false) when the value is
// absent (i.e. the request did not pass through the interceptor or failed
// validation).
func GetCallerOrgID(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(callerOrgIDKey{}).(string)
	return v, ok && v != ""
}
