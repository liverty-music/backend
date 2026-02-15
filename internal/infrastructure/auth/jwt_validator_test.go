package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lestrrat-go/jwx/v2/jwa"
	"github.com/lestrrat-go/jwx/v2/jwk"
	"github.com/lestrrat-go/jwx/v2/jwt"
)

// setupTestJWKS creates a test JWKS server and returns the server, private key, and public key set.
func setupTestJWKS(t *testing.T) (*httptest.Server, *rsa.PrivateKey, jwk.Set) {
	t.Helper()

	// Generate RSA key pair
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate RSA key: %v", err)
	}

	// Create JWK from public key
	publicKey, err := jwk.FromRaw(privateKey.PublicKey)
	if err != nil {
		t.Fatalf("failed to create JWK from public key: %v", err)
	}

	// Set key ID
	err = publicKey.Set(jwk.KeyIDKey, "test-key-id")
	if err != nil {
		t.Fatalf("failed to set key ID: %v", err)
	}

	// Set algorithm
	err = publicKey.Set(jwk.AlgorithmKey, jwa.RS256)
	if err != nil {
		t.Fatalf("failed to set algorithm: %v", err)
	}

	// Create key set
	keySet := jwk.NewSet()
	err = keySet.AddKey(publicKey)
	if err != nil {
		t.Fatalf("failed to add key to set: %v", err)
	}

	// Create test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(keySet)
	}))

	return server, privateKey, keySet
}

// createTestToken creates a signed JWT token for testing.
func createTestToken(t *testing.T, privateKey *rsa.PrivateKey, issuer, subject, email, name string, expiry time.Duration) string {
	t.Helper()

	// Create token
	token := jwt.New()
	err := token.Set(jwt.IssuerKey, issuer)
	if err != nil {
		t.Fatalf("failed to set issuer: %v", err)
	}

	err = token.Set(jwt.SubjectKey, subject)
	if err != nil {
		t.Fatalf("failed to set subject: %v", err)
	}

	err = token.Set("email", email)
	if err != nil {
		t.Fatalf("failed to set email: %v", err)
	}

	if name != "" {
		err = token.Set("name", name)
		if err != nil {
			t.Fatalf("failed to set name: %v", err)
		}
	}

	err = token.Set(jwt.IssuedAtKey, time.Now())
	if err != nil {
		t.Fatalf("failed to set issued at: %v", err)
	}

	err = token.Set(jwt.ExpirationKey, time.Now().Add(expiry))
	if err != nil {
		t.Fatalf("failed to set expiration: %v", err)
	}

	// Create JWK with key ID for signing
	key, err := jwk.FromRaw(privateKey)
	if err != nil {
		t.Fatalf("failed to create JWK: %v", err)
	}

	err = key.Set(jwk.KeyIDKey, "test-key-id")
	if err != nil {
		t.Fatalf("failed to set key ID: %v", err)
	}

	// Sign token with key ID
	signedToken, err := jwt.Sign(token, jwt.WithKey(jwa.RS256, key))
	if err != nil {
		t.Fatalf("failed to sign token: %v", err)
	}

	return string(signedToken)
}

func TestNewJWTValidator(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()

		server, _, _ := setupTestJWKS(t)
		defer server.Close()

		validator, err := NewJWTValidator(server.URL, server.URL+"/.well-known/jwks.json", 15*time.Minute)
		if err != nil {
			t.Fatalf("NewJWTValidator failed: %v", err)
		}

		if validator == nil {
			t.Fatal("validator should not be nil")
		}

		if validator.issuer != server.URL {
			t.Errorf("issuer = %q, want %q", validator.issuer, server.URL)
		}
	})

	t.Run("invalid JWKS URL", func(t *testing.T) {
		t.Parallel()

		validator, err := NewJWTValidator("http://invalid", "http://invalid/.well-known/jwks.json", 15*time.Minute)
		if err == nil {
			t.Error("NewJWTValidator should fail with invalid JWKS URL")
		}

		if validator != nil {
			t.Error("validator should be nil on error")
		}
	})
}

func TestValidateToken(t *testing.T) {
	t.Parallel()

	t.Run("valid token with all claims", func(t *testing.T) {
		t.Parallel()

		server, privateKey, _ := setupTestJWKS(t)
		defer server.Close()

		validator, err := NewJWTValidator(server.URL, server.URL+"/.well-known/jwks.json", 15*time.Minute)
		if err != nil {
			t.Fatalf("NewJWTValidator failed: %v", err)
		}

		tokenString := createTestToken(t, privateKey, server.URL, "user-123", "test@example.com", "Test User", 1*time.Hour)

		claims, err := validator.ValidateToken(context.Background(), tokenString)
		if err != nil {
			t.Fatalf("ValidateToken failed: %v", err)
		}

		if claims.Sub != "user-123" {
			t.Errorf("claims.Sub = %q, want %q", claims.Sub, "user-123")
		}
		if claims.Email != "test@example.com" {
			t.Errorf("claims.Email = %q, want %q", claims.Email, "test@example.com")
		}
		if claims.Name != "Test User" {
			t.Errorf("claims.Name = %q, want %q", claims.Name, "Test User")
		}
	})

	t.Run("valid token without name", func(t *testing.T) {
		t.Parallel()

		server, privateKey, _ := setupTestJWKS(t)
		defer server.Close()

		validator, err := NewJWTValidator(server.URL, server.URL+"/.well-known/jwks.json", 15*time.Minute)
		if err != nil {
			t.Fatalf("NewJWTValidator failed: %v", err)
		}

		tokenString := createTestToken(t, privateKey, server.URL, "user-456", "another@example.com", "", 1*time.Hour)

		claims, err := validator.ValidateToken(context.Background(), tokenString)
		if err != nil {
			t.Fatalf("ValidateToken failed: %v", err)
		}

		if claims.Sub != "user-456" {
			t.Errorf("claims.Sub = %q, want %q", claims.Sub, "user-456")
		}
		if claims.Email != "another@example.com" {
			t.Errorf("claims.Email = %q, want %q", claims.Email, "another@example.com")
		}
		if claims.Name != "" {
			t.Errorf("claims.Name = %q, want empty string", claims.Name)
		}
	})

	t.Run("expired token", func(t *testing.T) {
		t.Parallel()

		server, privateKey, _ := setupTestJWKS(t)
		defer server.Close()

		validator, err := NewJWTValidator(server.URL, server.URL+"/.well-known/jwks.json", 15*time.Minute)
		if err != nil {
			t.Fatalf("NewJWTValidator failed: %v", err)
		}

		tokenString := createTestToken(t, privateKey, server.URL, "user-123", "test@example.com", "Test User", -1*time.Hour)

		_, err = validator.ValidateToken(context.Background(), tokenString)
		if err == nil {
			t.Error("ValidateToken should fail with expired token")
		}
	})

	t.Run("wrong issuer", func(t *testing.T) {
		t.Parallel()

		server, privateKey, _ := setupTestJWKS(t)
		defer server.Close()

		validator, err := NewJWTValidator(server.URL, server.URL+"/.well-known/jwks.json", 15*time.Minute)
		if err != nil {
			t.Fatalf("NewJWTValidator failed: %v", err)
		}

		tokenString := createTestToken(t, privateKey, "https://wrong-issuer.com", "user-123", "test@example.com", "Test User", 1*time.Hour)

		_, err = validator.ValidateToken(context.Background(), tokenString)
		if err == nil {
			t.Error("ValidateToken should fail with wrong issuer")
		}
	})

	t.Run("malformed token", func(t *testing.T) {
		t.Parallel()

		server, _, _ := setupTestJWKS(t)
		defer server.Close()

		validator, err := NewJWTValidator(server.URL, server.URL+"/.well-known/jwks.json", 15*time.Minute)
		if err != nil {
			t.Fatalf("NewJWTValidator failed: %v", err)
		}

		_, err = validator.ValidateToken(context.Background(), "invalid.token.here")
		if err == nil {
			t.Error("ValidateToken should fail with malformed token")
		}
	})
}
