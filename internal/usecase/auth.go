package usecase

import (
	"context"
	"fmt"

	"github.com/liverty-music/backend/internal/entity"
)

// resolveUserID maps an external identity (Zitadel sub claim) to the internal user UUID.
// Returns the repository's error unchanged (preserving its status code) with added context.
func resolveUserID(ctx context.Context, userRepo entity.UserRepository, externalID string) (string, error) {
	user, err := userRepo.GetByExternalID(ctx, externalID)
	if err != nil {
		return "", fmt.Errorf("resolve user by external ID: %w", err)
	}
	return user.ID, nil
}
