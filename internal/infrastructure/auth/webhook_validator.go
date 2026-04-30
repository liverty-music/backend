package auth

import (
	"context"
	"fmt"

	"github.com/lestrrat-go/jwx/v2/jwk"
	"github.com/lestrrat-go/jwx/v2/jwt"
)

// WebhookValidator validates Zitadel Actions v2 webhook JWT bodies.
//
// Empirically, Zitadel v4 webhook JWTs (`PAYLOAD_TYPE_JWT`) do not
// populate the standard OIDC claims `iss` or `aud` — the previous
// validator versions rejected every webhook call because both claims
// came through empty. The signature is what proves authenticity:
// webhooks are signed by the same Zitadel instance JWKS that signs
// end-user access tokens, and only Zitadel holds the corresponding
// private keys. Thus signature verification (via the shared JWKS
// cache) is the security boundary.
//
// Replay risk is mitigated by:
//   - Network isolation: the webhook listener is a private :9090
//     ClusterIP service, not exposed externally.
//   - Per-handler payload-shape checks: each webhook handler decodes
//     application-specific claims (e.g. `request.email.address` for
//     auto-verify-email vs. `user.human.email` for pre-access-token);
//     a JWT minted for a different webhook would fail the handler's
//     payload-shape expectations.
//
// As Zitadel matures the webhook JWT contract, we may re-introduce
// `iss` / `aud` enforcement and/or migrate to per-Target HMAC
// signing-key verification. Until then, signature + network isolation
// is the documented contract.
//
// The JWKS cache is shared with the end-user JWTValidator rather than
// duplicated, so there is exactly one refresh goroutine per instance.
type WebhookValidator struct {
	jwks    *jwk.Cache
	jwksURL string
}

// NewWebhookValidator returns a validator that shares the receiver's
// JWKS cache. No additional configuration is required because Zitadel
// v4 webhook JWTs do not carry `iss`/`aud` claims that we could pin.
//
// The `_ string` parameter is preserved for API compatibility with the
// previous `expectedAudience`-taking signature; callers can pass the
// audience-shaped identifier (e.g. "urn:liverty-music:webhook:...")
// for documentation purposes, but it is not enforced.
func (v *JWTValidator) NewWebhookValidator(_ string) *WebhookValidator {
	return &WebhookValidator{
		jwks:    v.jwks,
		jwksURL: v.jwksURL,
	}
}

// ValidateWebhookToken verifies the JWT signature against the JWKS and
// returns the parsed token (claims included). Unlike end-user
// access-token validation, no standard OIDC claims (`iss`, `aud`,
// `sub`, `email`, `name`) are required — webhook payload data is
// extracted from application-specific private claims by the caller.
//
// The validator enforces only: signature (via JWKS) and expiry not
// past (via `jwt.WithValidate(true)`). See the type-level doc comment
// for why `iss` and `aud` are intentionally not enforced.
func (v *WebhookValidator) ValidateWebhookToken(
	ctx context.Context,
	tokenString string,
) (jwt.Token, error) {
	keySet, err := v.jwks.Get(ctx, v.jwksURL)
	if err != nil {
		return nil, fmt.Errorf("failed to get JWKS: %w", err)
	}

	token, err := jwt.Parse(
		[]byte(tokenString),
		jwt.WithKeySet(keySet),
		jwt.WithValidate(true),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to validate webhook token: %w", err)
	}

	return token, nil
}
