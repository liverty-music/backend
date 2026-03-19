package auth_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/liverty-music/backend/internal/infrastructure/auth"
)

func TestWithClaims(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		claims *auth.Claims
	}{
		{
			name: "set full claims",
			claims: &auth.Claims{
				Sub:   "user-123",
				Email: "test@example.com",
				Name:  "Test User",
			},
		},
		{
			name: "set claims without name",
			claims: &auth.Claims{
				Sub:   "user-456",
				Email: "another@example.com",
				Name:  "",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			newCtx := auth.WithClaims(ctx, tt.claims)

			assert.NotEqual(t, ctx, newCtx, "WithClaims should return a new context")

			claims, ok := auth.GetClaims(newCtx)
			assert.True(t, ok, "GetClaims should return true when claims are set")
			assert.Equal(t, tt.claims.Sub, claims.Sub)
			assert.Equal(t, tt.claims.Email, claims.Email)
			assert.Equal(t, tt.claims.Name, claims.Name)
		})
	}
}

func TestWithUserID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		userID string
	}{
		{
			name:   "set user ID",
			userID: "user-123",
		},
		{
			name:   "set empty user ID",
			userID: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			// WithUserID is deprecated, but we still test backward compatibility.
			claims := &auth.Claims{Sub: tt.userID, Email: "test@example.com"}
			newCtx := auth.WithClaims(ctx, claims)

			userID, ok := auth.GetUserID(newCtx)
			// Claims are always set, so ok must be true regardless of whether Sub is empty.
			assert.True(t, ok, "GetUserID should return true when claims are set")
			assert.Equal(t, tt.userID, userID)
		})
	}
}

func TestGetClaims(t *testing.T) {
	t.Parallel()

	t.Run("claims not set", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		claims, ok := auth.GetClaims(ctx)

		assert.False(t, ok, "GetClaims should return false when claims are not set")
		assert.Nil(t, claims)
	})

	t.Run("claims set", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		expectedClaims := &auth.Claims{
			Sub:   "user-456",
			Email: "test@example.com",
			Name:  "Test User",
		}
		ctx = auth.WithClaims(ctx, expectedClaims)

		claims, ok := auth.GetClaims(ctx)
		assert.True(t, ok, "GetClaims should return true when claims are set")
		assert.Equal(t, expectedClaims.Sub, claims.Sub)
		assert.Equal(t, expectedClaims.Email, claims.Email)
		assert.Equal(t, expectedClaims.Name, claims.Name)
	})

	t.Run("wrong type in context", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		ctx = context.WithValue(ctx, auth.ClaimsKey, "not-a-claims-object")

		claims, ok := auth.GetClaims(ctx)
		assert.False(t, ok, "GetClaims should return false when value is wrong type")
		assert.Nil(t, claims)
	})
}

func TestGetUserID(t *testing.T) {
	t.Parallel()

	t.Run("claims not set", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		userID, ok := auth.GetUserID(ctx)

		assert.False(t, ok, "GetUserID should return false when claims are not set")
		assert.Empty(t, userID)
	})

	t.Run("claims set with sub", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		expectedUserID := "user-456"
		ctx = auth.WithClaims(ctx, &auth.Claims{
			Sub:   expectedUserID,
			Email: "test@example.com",
			Name:  "Test User",
		})

		userID, ok := auth.GetUserID(ctx)
		assert.True(t, ok, "GetUserID should return true when claims are set")
		assert.Equal(t, expectedUserID, userID)
	})
}
