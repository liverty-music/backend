package auth

import (
	"context"
	"fmt"
	"time"

	"github.com/lestrrat-go/jwx/v2/jwk"
	"github.com/lestrrat-go/jwx/v2/jwt"
)

// JWTValidator validates JWT tokens using JWKS.
type JWTValidator struct {
	jwks    *jwk.Cache
	issuer  string
	jwksURL string
}

// NewJWTValidator creates a new JWT validator.
// It initializes a JWKS cache that automatically refreshes from the given JWKS URL.
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
		jwks:    cache,
		issuer:  issuer,
		jwksURL: jwksURL,
	}, nil
}

// ValidateToken validates a JWT token and returns the claims.
func (v *JWTValidator) ValidateToken(ctx context.Context, tokenString string) (*Claims, error) {
	// Get the JWKS for validation
	keySet, err := v.jwks.Get(ctx, v.jwksURL)
	if err != nil {
		return nil, fmt.Errorf("failed to get JWKS: %w", err)
	}

	// Parse and validate the token
	token, err := jwt.Parse(
		[]byte(tokenString),
		jwt.WithKeySet(keySet),
		jwt.WithValidate(true),
		jwt.WithIssuer(v.issuer),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to validate token: %w", err)
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

	return &Claims{
		Sub:   sub,
		Email: emailStr,
		Name:  name,
	}, nil
}
