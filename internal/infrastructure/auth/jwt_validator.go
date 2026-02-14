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
	jwks   *jwk.Cache
	issuer string
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
		jwks:   cache,
		issuer: issuer,
	}, nil
}

// ValidateToken validates a JWT token and returns the user ID (sub claim).
func (v *JWTValidator) ValidateToken(tokenString string) (string, error) {
	// Get the JWKS for validation
	ctx := context.Background()
	keySet, err := v.jwks.Get(ctx, v.issuer+"/.well-known/jwks.json")
	if err != nil {
		return "", fmt.Errorf("failed to get JWKS: %w", err)
	}

	// Parse and validate the token
	token, err := jwt.Parse(
		[]byte(tokenString),
		jwt.WithKeySet(keySet),
		jwt.WithValidate(true),
		jwt.WithIssuer(v.issuer),
	)
	if err != nil {
		return "", fmt.Errorf("failed to validate token: %w", err)
	}

	// Extract the subject (user ID) from the token
	userID := token.Subject()
	if userID == "" {
		return "", fmt.Errorf("token missing subject claim")
	}

	return userID, nil
}
