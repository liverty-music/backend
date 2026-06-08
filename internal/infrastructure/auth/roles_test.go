package auth_test

import (
	"context"
	"crypto/rsa"
	"testing"
	"time"

	"github.com/lestrrat-go/jwx/v2/jwa"
	"github.com/lestrrat-go/jwx/v2/jwk"
	"github.com/lestrrat-go/jwx/v2/jwt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/liverty-music/backend/internal/infrastructure/auth"
)

// createTokenWithRoles creates a signed JWT token that carries the given Zitadel
// role claims. claimKey selects which claim name to use:
//   - "global" → "urn:zitadel:iam:org:project:roles"
//   - "project" → "urn:zitadel:iam:org:project:123456789:roles"
func createTokenWithRoles(t *testing.T, privateKey *rsa.PrivateKey, issuer string, claimKey string, roles []string) string {
	t.Helper()

	token := jwt.New()
	require.NoError(t, token.Set(jwt.IssuerKey, issuer))
	require.NoError(t, token.Set(jwt.SubjectKey, "user-123"))
	require.NoError(t, token.Set("email", "admin@example.com"))
	require.NoError(t, token.Set(jwt.IssuedAtKey, time.Now()))
	require.NoError(t, token.Set(jwt.ExpirationKey, time.Now().Add(time.Hour)))

	if len(roles) > 0 {
		roleMap := make(map[string]any, len(roles))
		for _, r := range roles {
			roleMap[r] = map[string]string{"org-id-1": "example.com"}
		}

		var claim string
		switch claimKey {
		case "project":
			claim = "urn:zitadel:iam:org:project:123456789:roles"
		default:
			claim = "urn:zitadel:iam:org:project:roles"
		}
		require.NoError(t, token.Set(claim, roleMap))
	}

	key, err := jwk.FromRaw(privateKey)
	require.NoError(t, err)
	require.NoError(t, key.Set(jwk.KeyIDKey, "test-key-id"))

	signed, err := jwt.Sign(token, jwt.WithKey(jwa.RS256, key))
	require.NoError(t, err)
	return string(signed)
}

func TestValidateToken_Roles(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		setup     func(t *testing.T) (tokenString string, validator *auth.JWTValidator)
		wantRoles []string
	}{
		{
			name: "populate Roles from global claim",
			setup: func(t *testing.T) (string, *auth.JWTValidator) {
				t.Helper()
				server, privateKey, _ := setupTestJWKS(t)
				t.Cleanup(server.Close)
				v, err := auth.NewJWTValidator(server.URL, server.URL+"/.well-known/jwks.json", 15*time.Minute)
				require.NoError(t, err)
				tok := createTokenWithRoles(t, privateKey, server.URL, "global", []string{"admin"})
				return tok, v
			},
			wantRoles: []string{"admin"},
		},
		{
			name: "populate Roles from project-scoped claim",
			setup: func(t *testing.T) (string, *auth.JWTValidator) {
				t.Helper()
				server, privateKey, _ := setupTestJWKS(t)
				t.Cleanup(server.Close)
				v, err := auth.NewJWTValidator(server.URL, server.URL+"/.well-known/jwks.json", 15*time.Minute)
				require.NoError(t, err)
				tok := createTokenWithRoles(t, privateKey, server.URL, "project", []string{"admin", "reviewer"})
				return tok, v
			},
			wantRoles: []string{"admin", "reviewer"},
		},
		{
			name: "return empty Roles when claim is absent",
			setup: func(t *testing.T) (string, *auth.JWTValidator) {
				t.Helper()
				server, privateKey, _ := setupTestJWKS(t)
				t.Cleanup(server.Close)
				v, err := auth.NewJWTValidator(server.URL, server.URL+"/.well-known/jwks.json", 15*time.Minute)
				require.NoError(t, err)
				// createTestToken from jwt_validator_test.go — no role opts → no claim set.
				tok := createTestToken(t, privateKey, server.URL, "user-456", "user@example.com", "User", time.Hour)
				return tok, v
			},
			wantRoles: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tokenString, validator := tt.setup(t)
			claims, err := validator.ValidateToken(context.Background(), tokenString)

			require.NoError(t, err)
			assert.ElementsMatch(t, tt.wantRoles, claims.Roles)
		})
	}
}

func TestRequireRole(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		ctx     func() context.Context
		role    string
		wantErr bool
	}{
		{
			name: "allow caller holding the role",
			ctx: func() context.Context {
				return auth.WithClaims(context.Background(), &auth.Claims{
					Sub:   "admin-user",
					Roles: []string{"admin"},
				})
			},
			role:    "admin",
			wantErr: false,
		},
		{
			name: "deny caller without the role",
			ctx: func() context.Context {
				return auth.WithClaims(context.Background(), &auth.Claims{
					Sub:   "regular-user",
					Roles: []string{"viewer"},
				})
			},
			role:    "admin",
			wantErr: true,
		},
		{
			name: "deny caller with no roles at all",
			ctx: func() context.Context {
				return auth.WithClaims(context.Background(), &auth.Claims{
					Sub: "regular-user",
				})
			},
			role:    "admin",
			wantErr: true,
		},
		{
			name: "deny unauthenticated context (no claims)",
			ctx: func() context.Context {
				return context.Background()
			},
			role:    "admin",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := auth.RequireRole(tt.ctx(), tt.role)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
