package auth

import (
	"context"
	"fmt"
	"slices"

	"github.com/lestrrat-go/jwx/v2/jwk"
	"github.com/lestrrat-go/jwx/v2/jwt"
)

// WebhookValidator validates Zitadel Actions v2 webhook JWT bodies.
//
// Webhook JWTs are signed by the same Zitadel JWKS that signs end-user access
// tokens, so signature + issuer + expiry checks alone cannot distinguish a
// user-facing access token from a webhook payload. `expectedAudience` is the
// decisive check — each webhook Target is registered with a distinct `aud`
// value (e.g. `urn:liverty-music:webhook:pre-access-token`), and the
// validator rejects any JWT whose audience list does not include that value.
//
// The JWKS cache is shared with the end-user JWTValidator rather than
// duplicated, so there is exactly one refresh goroutine per instance.
type WebhookValidator struct {
	jwks             *jwk.Cache
	jwksURL          string
	acceptedIssuers  []string
	expectedAudience string
}

// NewWebhookValidator returns a validator that shares the receiver's JWKS
// cache and accepted-issuer set but pins `expectedAudience` (the `aud` claim)
// to distinguish webhook JWTs from end-user access tokens.
func (v *JWTValidator) NewWebhookValidator(expectedAudience string) *WebhookValidator {
	return &WebhookValidator{
		jwks:             v.jwks,
		jwksURL:          v.jwksURL,
		acceptedIssuers:  slices.Clone(v.acceptedIssuers),
		expectedAudience: expectedAudience,
	}
}

// ValidateWebhookToken verifies the JWT string and returns the parsed token
// (claims included). Unlike end-user access-token validation, `sub`, `email`,
// and `name` claims are not required — webhook payload user data is extracted
// from application-specific private claims by the caller.
//
// The validator enforces: signature (via JWKS), issuer in accepted set,
// expiry not past, audience matches `expectedAudience`.
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

	if !slices.Contains(v.acceptedIssuers, token.Issuer()) {
		return nil, fmt.Errorf("webhook token issuer %q is not in the accepted issuers list", token.Issuer())
	}

	if !slices.Contains(token.Audience(), v.expectedAudience) {
		return nil, fmt.Errorf("webhook token audience %v does not contain expected %q", token.Audience(), v.expectedAudience)
	}

	return token, nil
}
