package authctx_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/liverty-music/backend/pkg/authctx"
)

func TestWithClaims(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		claims *authctx.Claims
	}{
		{
			name: "set full claims",
			claims: &authctx.Claims{
				Sub:   "user-123",
				Email: "test@example.com",
				Name:  "Test User",
			},
		},
		{
			name: "set claims without name",
			claims: &authctx.Claims{
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
			newCtx := authctx.WithClaims(ctx, tt.claims)

			assert.NotEqual(t, ctx, newCtx, "WithClaims should return a new context")

			claims, ok := authctx.GetClaims(newCtx)
			assert.True(t, ok, "GetClaims should return true when claims are set")
			assert.Equal(t, tt.claims.Sub, claims.Sub)
			assert.Equal(t, tt.claims.Email, claims.Email)
			assert.Equal(t, tt.claims.Name, claims.Name)
		})
	}
}

func TestGetClaims(t *testing.T) {
	t.Parallel()

	t.Run("claims not set", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		claims, ok := authctx.GetClaims(ctx)

		assert.False(t, ok, "GetClaims should return false when claims are not set")
		assert.Nil(t, claims)
	})

	t.Run("claims set", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		expectedClaims := &authctx.Claims{
			Sub:   "user-456",
			Email: "test@example.com",
			Name:  "Test User",
		}
		ctx = authctx.WithClaims(ctx, expectedClaims)

		claims, ok := authctx.GetClaims(ctx)
		assert.True(t, ok, "GetClaims should return true when claims are set")
		assert.Equal(t, expectedClaims.Sub, claims.Sub)
		assert.Equal(t, expectedClaims.Email, claims.Email)
		assert.Equal(t, expectedClaims.Name, claims.Name)
	})
}

// TestGetCallerOrgID verifies that WithCallerOrgID / GetCallerOrgID round-trip correctly.
func TestGetCallerOrgID(t *testing.T) {
	t.Parallel()

	t.Run("return org id when set", func(t *testing.T) {
		t.Parallel()
		ctx := authctx.WithCallerOrgID(context.Background(), "my-org")
		id, ok := authctx.GetCallerOrgID(ctx)
		assert.True(t, ok)
		assert.Equal(t, "my-org", id)
	})

	t.Run("return false when not set", func(t *testing.T) {
		t.Parallel()
		_, ok := authctx.GetCallerOrgID(context.Background())
		assert.False(t, ok)
	})

	t.Run("return false when set to empty string", func(t *testing.T) {
		t.Parallel()
		ctx := authctx.WithCallerOrgID(context.Background(), "")
		_, ok := authctx.GetCallerOrgID(ctx)
		assert.False(t, ok)
	})
}
