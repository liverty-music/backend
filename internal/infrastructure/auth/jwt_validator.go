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

	return &Claims{
		Sub:           sub,
		Email:         emailStr,
		Name:          name,
		EmailVerified: emailVerified,
		Roles:         roles,
	}, nil
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
