package auth

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/lestrrat-go/jwx/v2/jwk"
	"github.com/lestrrat-go/jwx/v2/jwt"
)

// JWTValidator validates JWT tokens using JWKS.
type JWTValidator struct {
	jwks            *jwk.Cache
	issuer          string
	acceptedIssuers []string
	jwksURL         string
}

// NewJWTValidator creates a new JWT validator.
// It initializes a JWKS cache that automatically refreshes from the given JWKS URL.
// issuer is the primary (and only) accepted issuer. Use WithAcceptedIssuers to add
// additional accepted issuers for multi-provider scenarios (e.g., Option C migration).
func NewJWTValidator(issuer, jwksURL string, refreshInterval time.Duration) (*JWTValidator, error) {
	// Create JWKS cache with auto-refresh
	cache := jwk.NewCache(context.Background())

	// Register the JWKS URL for automatic refresh
	err := cache.Register(jwksURL, jwk.WithMinRefreshInterval(refreshInterval))
	if err != nil {
		return nil, fmt.Errorf("failed to register JWKS URL: %w", err)
	}

	// Fetch the keys immediately to verify connectivity
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err = cache.Refresh(ctx, jwksURL)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch JWKS: %w", err)
	}

	return &JWTValidator{
		jwks:            cache,
		issuer:          issuer,
		acceptedIssuers: []string{issuer},
		jwksURL:         jwksURL,
	}, nil
}

// WithAcceptedIssuers returns a copy of the validator that accepts tokens from any of
// the listed issuers. Use this when migrating to a second identity provider (Option C)
// without breaking existing Zitadel-issued tokens.
func (v *JWTValidator) WithAcceptedIssuers(issuers []string) *JWTValidator {
	cp := *v
	cp.acceptedIssuers = issuers
	return &cp
}

// ValidateToken validates a JWT token and returns the claims.
func (v *JWTValidator) ValidateToken(ctx context.Context, tokenString string) (*Claims, error) {
	// Get the JWKS for validation
	keySet, err := v.jwks.Get(ctx, v.jwksURL)
	if err != nil {
		return nil, fmt.Errorf("failed to get JWKS: %w", err)
	}

	// Parse and validate the token.
	// We skip built-in issuer validation here and verify the issuer ourselves below,
	// because jwt.WithIssuer accepts only a single value while acceptedIssuers may
	// contain multiple entries (e.g., during Option C migration).
	token, err := jwt.Parse(
		[]byte(tokenString),
		jwt.WithKeySet(keySet),
		jwt.WithValidate(true),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to validate token: %w", err)
	}

	// Verify the issuer against the accepted list.
	tokenIssuer := token.Issuer()
	issuerOK := slices.Contains(v.acceptedIssuers, tokenIssuer)
	if !issuerOK {
		return nil, fmt.Errorf("token issuer %q is not in the accepted issuers list", tokenIssuer)
	}

	// Extract claims from the token
	sub := token.Subject()
	if sub == "" {
		return nil, fmt.Errorf("token missing subject claim")
	}

	// Extract email from private claims
	email, ok := token.Get("email")
	if !ok {
		return nil, fmt.Errorf("token missing email claim")
	}
	emailStr, ok := email.(string)
	if !ok {
		return nil, fmt.Errorf("email claim is not a string")
	}

	// Extract name from private claims (optional - may be empty)
	name := ""
	if nameVal, ok := token.Get("name"); ok {
		if nameStr, ok := nameVal.(string); ok {
			name = nameStr
		}
	}

	// Extract email_verified from private claims (set by a Zitadel Action).
	// Defaults to false when the claim is absent (fail-closed).
	emailVerified := false
	if evVal, ok := token.Get("email_verified"); ok {
		if evBool, ok := evVal.(bool); ok {
			emailVerified = evBool
		}
	}

	// Extract Zitadel project role names from the token.
	//
	// Zitadel includes role grants in up to two claim keys:
	//   - "urn:zitadel:iam:org:project:roles"          (global, all projects)
	//   - "urn:zitadel:iam:org:project:{projectId}:roles" (project-scoped)
	//
	// Each claim value is a JSON object whose keys are role names and whose
	// values are org-id→domain maps (not used here). We collect every unique
	// key across all matching claim keys into a deduplicated slice.
	roles := extractZitadelRoles(token)

	// Extract the audience claim for organizer-console project id verification.
	audiences := extractAudiences(token)

	// Extract the org-scoped role grants, preserving the inner orgId.
	// The login-scope org is inferred from the unique inner orgId across all
	// project-scoped role grants (see Claims.LoginScopeOrgID for the full
	// assumption). The org-scoped interceptor uses these to authorize
	// organizer RPCs.
	roleOrgIDs, loginScopeOrgID := extractRoleOrgIDs(token)

	return &Claims{
		Sub:             sub,
		Email:           emailStr,
		Name:            name,
		EmailVerified:   emailVerified,
		Roles:           roles,
		Audiences:       audiences,
		LoginScopeOrgID: loginScopeOrgID,
		RoleOrgIDs:      roleOrgIDs,
	}, nil
}

// extractAudiences returns the audience ("aud") claim values from the token.
// The JWT spec allows "aud" to be either a single string or an array of
// strings; jwx normalises it to []string via Audiences().
func extractAudiences(token jwt.Token) []string {
	auds := token.Audience()
	if len(auds) == 0 {
		return nil
	}
	out := make([]string, len(auds))
	copy(out, auds)
	return out
}

// extractRoleOrgIDs scans the project-scoped role claim on token and collects
// the set of Zitadel org ids under which the caller holds at least one role.
// It returns (roleOrgIDs, loginScopeOrgID).
//
// loginScopeOrgID is the single unique inner orgId across all project-scoped
// role grants — inferred as the login-scope org because Zitadel filters the
// role grants to the org the operator logged in against. When the set of
// unique inner orgIds is not exactly one, loginScopeOrgID is returned empty;
// the org-scoped interceptor will reject such tokens with PERMISSION_DENIED.
//
// Only the project-scoped claim ("urn:zitadel:iam:org:project:{id}:roles") is
// read here. The global claim ("urn:zitadel:iam:org:project:roles") does not
// carry inner org ids and is therefore not useful for org-scoped authorization.
//
// ASSUMPTION: The login-scope org is inferred from the unique inner orgId in
// the project-scoped roles claim. If Zitadel adds a dedicated
// "urn:zitadel:iam:org:id" top-level claim to the token body, update this
// function to read it directly. See Claims.LoginScopeOrgID for details.
func extractRoleOrgIDs(token jwt.Token) (roleOrgIDs map[string]struct{}, loginScopeOrgID string) {
	const projectRolePrefix = "urn:zitadel:iam:org:project:"
	const projectRoleSuffix = ":roles"

	roleOrgIDs = make(map[string]struct{})

	for key, val := range token.PrivateClaims() {
		isProjectScoped := len(key) > len(projectRolePrefix)+len(projectRoleSuffix) &&
			key[:len(projectRolePrefix)] == projectRolePrefix &&
			key[len(key)-len(projectRoleSuffix):] == projectRoleSuffix

		if !isProjectScoped {
			continue
		}

		// The claim value is map[string]any where each key is a role name and
		// the value is map[string]any mapping orgId → primaryDomain.
		roleMap, ok := val.(map[string]any)
		if !ok {
			continue
		}
		for _, orgMap := range roleMap {
			om, ok := orgMap.(map[string]any)
			if !ok {
				continue
			}
			for orgID := range om {
				roleOrgIDs[orgID] = struct{}{}
			}
		}
	}

	if len(roleOrgIDs) == 1 {
		for id := range roleOrgIDs {
			loginScopeOrgID = id
		}
	}

	return roleOrgIDs, loginScopeOrgID
}

// extractZitadelRoles scans all private claims on token for the two Zitadel
// role claim shapes and returns a deduplicated slice of role name strings.
// Returns nil when no role claims are present.
//
// Zitadel encodes role grants under two possible claim keys:
//   - "urn:zitadel:iam:org:project:roles"              (global, all projects)
//   - "urn:zitadel:iam:org:project:{projectId}:roles"  (project-scoped)
//
// Each value is a JSON object whose keys are role names; the inner values
// (org-id → domain maps) are not consumed.
func extractZitadelRoles(token jwt.Token) []string {
	const globalRoleClaim = "urn:zitadel:iam:org:project:roles"
	const projectRolePrefix = "urn:zitadel:iam:org:project:"
	const projectRoleSuffix = ":roles"

	seen := make(map[string]struct{})

	// PrivateClaims returns all non-standard claims as a map keyed by claim name.
	for key, val := range token.PrivateClaims() {
		isGlobal := key == globalRoleClaim
		isProjectScoped := !isGlobal &&
			len(key) > len(projectRolePrefix)+len(projectRoleSuffix) &&
			key[:len(projectRolePrefix)] == projectRolePrefix &&
			key[len(key)-len(projectRoleSuffix):] == projectRoleSuffix

		if !isGlobal && !isProjectScoped {
			continue
		}

		// The claim value is a map[string]interface{} where keys are role names.
		roleMap, ok := val.(map[string]any)
		if !ok {
			continue
		}
		for roleName := range roleMap {
			seen[roleName] = struct{}{}
		}
	}

	if len(seen) == 0 {
		return nil
	}

	roles := make([]string, 0, len(seen))
	for r := range seen {
		roles = append(roles, r)
	}
	return roles
}
