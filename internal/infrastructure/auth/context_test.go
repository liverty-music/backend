package auth

import (
	"context"
	"testing"
)

func TestWithClaims(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		claims *Claims
	}{
		{
			name: "set full claims",
			claims: &Claims{
				Sub:   "user-123",
				Email: "test@example.com",
				Name:  "Test User",
			},
		},
		{
			name: "set claims without name",
			claims: &Claims{
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
			newCtx := WithClaims(ctx, tt.claims)

			if newCtx == ctx {
				t.Error("WithClaims should return a new context")
			}

			claims, ok := GetClaims(newCtx)
			if !ok {
				t.Error("GetClaims should return true when claims are set")
			}

			if claims.Sub != tt.claims.Sub {
				t.Errorf("GetClaims().Sub = %q, want %q", claims.Sub, tt.claims.Sub)
			}
			if claims.Email != tt.claims.Email {
				t.Errorf("GetClaims().Email = %q, want %q", claims.Email, tt.claims.Email)
			}
			if claims.Name != tt.claims.Name {
				t.Errorf("GetClaims().Name = %q, want %q", claims.Name, tt.claims.Name)
			}
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
			// WithUserID is deprecated, but we still test backward compatibility
			claims := &Claims{Sub: tt.userID, Email: "test@example.com"}
			newCtx := WithClaims(ctx, claims)

			userID, ok := GetUserID(newCtx)
			if tt.userID == "" {
				// Empty userID case - claims exist but Sub is empty
				if !ok {
					t.Error("GetUserID should return true when claims are set, even if Sub is empty")
				}
			} else {
				if !ok {
					t.Error("GetUserID should return true when user ID is set")
				}
			}

			if userID != tt.userID {
				t.Errorf("GetUserID() = %q, want %q", userID, tt.userID)
			}
		})
	}
}

func TestGetClaims(t *testing.T) {
	t.Parallel()

	t.Run("claims not set", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		claims, ok := GetClaims(ctx)

		if ok {
			t.Error("GetClaims should return false when claims are not set")
		}

		if claims != nil {
			t.Error("GetClaims should return nil when claims are not set")
		}
	})

	t.Run("claims set", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		expectedClaims := &Claims{
			Sub:   "user-456",
			Email: "test@example.com",
			Name:  "Test User",
		}
		ctx = WithClaims(ctx, expectedClaims)

		claims, ok := GetClaims(ctx)
		if !ok {
			t.Error("GetClaims should return true when claims are set")
		}

		if claims.Sub != expectedClaims.Sub {
			t.Errorf("GetClaims().Sub = %q, want %q", claims.Sub, expectedClaims.Sub)
		}
		if claims.Email != expectedClaims.Email {
			t.Errorf("GetClaims().Email = %q, want %q", claims.Email, expectedClaims.Email)
		}
		if claims.Name != expectedClaims.Name {
			t.Errorf("GetClaims().Name = %q, want %q", claims.Name, expectedClaims.Name)
		}
	})

	t.Run("wrong type in context", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		ctx = context.WithValue(ctx, claimsKey, "not-a-claims-object")

		claims, ok := GetClaims(ctx)
		if ok {
			t.Error("GetClaims should return false when value is wrong type")
		}

		if claims != nil {
			t.Error("GetClaims should return nil when value is wrong type")
		}
	})
}

func TestGetUserID(t *testing.T) {
	t.Parallel()

	t.Run("claims not set", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		userID, ok := GetUserID(ctx)

		if ok {
			t.Error("GetUserID should return false when claims are not set")
		}

		if userID != "" {
			t.Errorf("GetUserID() = %q, want empty string", userID)
		}
	})

	t.Run("claims set with sub", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		expectedUserID := "user-456"
		ctx = WithClaims(ctx, &Claims{
			Sub:   expectedUserID,
			Email: "test@example.com",
			Name:  "Test User",
		})

		userID, ok := GetUserID(ctx)
		if !ok {
			t.Error("GetUserID should return true when claims are set")
		}

		if userID != expectedUserID {
			t.Errorf("GetUserID() = %q, want %q", userID, expectedUserID)
		}
	})
}
