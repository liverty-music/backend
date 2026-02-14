package auth

import (
	"context"
	"testing"
)

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
			newCtx := WithUserID(ctx, tt.userID)

			if newCtx == ctx {
				t.Error("WithUserID should return a new context")
			}

			userID, ok := GetUserID(newCtx)
			if !ok {
				t.Error("GetUserID should return true when user ID is set")
			}

			if userID != tt.userID {
				t.Errorf("GetUserID() = %q, want %q", userID, tt.userID)
			}
		})
	}
}

func TestGetUserID(t *testing.T) {
	t.Parallel()

	t.Run("user ID not set", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		userID, ok := GetUserID(ctx)

		if ok {
			t.Error("GetUserID should return false when user ID is not set")
		}

		if userID != "" {
			t.Errorf("GetUserID() = %q, want empty string", userID)
		}
	})

	t.Run("user ID set", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		expectedUserID := "user-456"
		ctx = WithUserID(ctx, expectedUserID)

		userID, ok := GetUserID(ctx)
		if !ok {
			t.Error("GetUserID should return true when user ID is set")
		}

		if userID != expectedUserID {
			t.Errorf("GetUserID() = %q, want %q", userID, expectedUserID)
		}
	})

	t.Run("wrong type in context", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		ctx = context.WithValue(ctx, userIDKey, 12345) // wrong type

		userID, ok := GetUserID(ctx)
		if ok {
			t.Error("GetUserID should return false when value is wrong type")
		}

		if userID != "" {
			t.Errorf("GetUserID() = %q, want empty string", userID)
		}
	})
}
