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
// Webhook JWTs are signed by the same Zitadel JWKS that signs end-user
// access tokens, so signature verification alone proves the JWT
// originated from our Zitadel instance (only Zitadel holds the private
// key). The decisive boundary that distinguishes a webhook payload from
// a user-facing access token is `expectedAudience` — each webhook
// Target is registered with a distinct `aud` value (e.g.
// `urn:liverty-music:webhook:pre-access-token`), and the validator
// rejects any JWT whose audience list does not include that value.
//
// `iss` is intentionally NOT enforced. Empirically, Zitadel v4 webhook
// JWTs do not include an `iss` claim (or include it as an empty
// string), and the upstream community Go webhook implementation
// (xianyu-one/zitadel-mapping) also relies on signature + custom
// validation without checking `iss`. Adding an issuer check here is
// redundant once signature verification has succeeded — there is no
// other Zitadel instance that could have signed a token verifiable
// against our JWKS.
//
// The JWKS cache is shared with the end-user JWTValidator rather than
// duplicated, so there is exactly one refresh goroutine per instance.
type WebhookValidator struct {
	jwks             *jwk.Cache
	jwksURL          string
	expectedAudience string
}

// NewWebhookValidator returns a validator that shares the receiver's JWKS
// cache but pins `expectedAudience` (the `aud` claim) to distinguish
// webhook JWTs from end-user access tokens.
func (v *JWTValidator) NewWebhookValidator(expectedAudience string) *WebhookValidator {
	return &WebhookValidator{
		jwks:             v.jwks,
		jwksURL:          v.jwksURL,
		expectedAudience: expectedAudience,
	}
}

// ValidateWebhookToken verifies the JWT string and returns the parsed
// token (claims included). Unlike end-user access-token validation,
// `sub`, `email`, `name`, and `iss` claims are not required — webhook
// payload user data is extracted from application-specific private
// claims by the caller.
//
// The validator enforces: signature (via JWKS), expiry not past, and
// audience matches `expectedAudience`. See the type-level doc comment
// for why `iss` is intentionally not enforced.
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

	if !slices.Contains(token.Audience(), v.expectedAudience) {
		return nil, fmt.Errorf("webhook token audience %v does not contain expected %q", token.Audience(), v.expectedAudience)
	}

	return token, nil
}
