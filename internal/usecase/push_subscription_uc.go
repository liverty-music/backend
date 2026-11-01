package usecase

import (
	"context"
	"fmt"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-logging/logging"
)

// PushSubscriptionUseCase manages a user's Web Push subscriptions, one per
// browser.
type PushSubscriptionUseCase interface {
	// Create registers or updates the browser push subscription for the given
	// (userID, endpoint) pair. The subscription is keyed by endpoint: calling
	// Create with an endpoint that already exists updates the record in
	// place and returns it with its existing, stored id. Because an endpoint
	// identifies a browser/device rather than a user, registering an
	// endpoint that belongs to a different userID reassigns it to the given
	// userID; the previous owner no longer has it. PushSubscription.Create
	// announces a new or changed subscription (NOTIFICATION.subscribed).
	//
	// # Possible errors
	//
	//   - Unavailable: the announcement could not be recorded; nothing is
	//     stored.
	//   - Internal: subscription persistence failure.
	Create(ctx context.Context, userID, endpoint, p256dh, auth string) (*entity.PushSubscription, error)

	// Get returns the push subscription uniquely identified by (userID, endpoint).
	//
	// # Possible errors
	//
	//   - NotFound: no subscription exists for the given pair.
	//   - Internal: subscription lookup failure.
	Get(ctx context.Context, userID, endpoint string) (*entity.PushSubscription, error)

	// Delete removes the push subscription uniquely identified by
	// (userID, endpoint). Other browsers registered by the same user remain
	// active. The operation is idempotent. PushSubscription.Delete announces a
	// removed subscription (NOTIFICATION.unsubscribed).
	//
	// # Possible errors
	//
	//   - Unavailable: the announcement could not be recorded; the
	//     subscription remains.
	//   - Internal: subscription deletion failure.
	Delete(ctx context.Context, userID, endpoint string) error
}

// pushSubscriptionUseCase implements PushSubscriptionUseCase.
type pushSubscriptionUseCase struct {
	pushSubRepo entity.PushSubscriptionRepository
	logger      *logging.Logger
}

// Compile-time interface compliance check.
var _ PushSubscriptionUseCase = (*pushSubscriptionUseCase)(nil)

// NewPushSubscriptionUseCase creates a new PushSubscriptionUseCase.
func NewPushSubscriptionUseCase(
	pushSubRepo entity.PushSubscriptionRepository,
	logger *logging.Logger,
) PushSubscriptionUseCase {
	return &pushSubscriptionUseCase{
		pushSubRepo: pushSubRepo,
		logger:      logger,
	}
}

// Create registers or updates the push subscription for the given (userID, endpoint) pair.
func (uc *pushSubscriptionUseCase) Create(ctx context.Context, userID, endpoint, p256dh, auth string) (*entity.PushSubscription, error) {
	sub := &entity.PushSubscription{
		UserID:   userID,
		Endpoint: endpoint,
		P256dh:   p256dh,
		Auth:     auth,
	}
	if err := uc.pushSubRepo.Create(ctx, sub); err != nil {
		return nil, fmt.Errorf("failed to persist push subscription: %w", err)
	}
	return sub, nil
}

// Get retrieves the push subscription matching the (userID, endpoint) pair.
// Returns a NotFound error when no such subscription exists.
func (uc *pushSubscriptionUseCase) Get(ctx context.Context, userID, endpoint string) (*entity.PushSubscription, error) {
	sub, err := uc.pushSubRepo.Get(ctx, userID, endpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to get push subscription: %w", err)
	}
	return sub, nil
}

// Delete removes the push subscription matching the (userID, endpoint) pair.
// Other browsers registered by the same user remain active. Idempotent. The
// auto-cleanup path inside NotifyNewConcerts (410 Gone) does not call this
// method.
func (uc *pushSubscriptionUseCase) Delete(ctx context.Context, userID, endpoint string) error {
	if err := uc.pushSubRepo.Delete(ctx, userID, endpoint); err != nil {
		return fmt.Errorf("failed to delete push subscription: %w", err)
	}
	return nil
}
