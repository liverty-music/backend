package usecase

import (
	"context"
	"fmt"
	"log/slog"
	"time"

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
	// userID; the previous owner no longer has it.
	//
	// deviceType is the browser/OS family classifier for the endpoint (e.g.
	// "android", "apple", "firefox", "windows", "other"), derived by the
	// caller (see mapper.DeviceTypeFromEndpoint) so this use case never needs
	// to know about push-vendor host names. It is forwarded, unmodified, into
	// the NOTIFICATION.subscribed analytics event.
	//
	// # Possible errors
	//
	//   - Internal: subscription persistence failure.
	Create(ctx context.Context, userID, endpoint, p256dh, auth, deviceType string) (*entity.PushSubscription, error)

	// Get returns the push subscription uniquely identified by (userID, endpoint).
	//
	// # Possible errors
	//
	//   - NotFound: no subscription exists for the given pair.
	//   - Internal: subscription lookup failure.
	Get(ctx context.Context, userID, endpoint string) (*entity.PushSubscription, error)

	// Delete removes the push subscription uniquely identified by
	// (userID, endpoint). Other browsers registered by the same user remain
	// active. The operation is idempotent.
	//
	// deviceType is the browser/OS family classifier for the endpoint,
	// derived by the caller (see mapper.DeviceTypeFromEndpoint) and forwarded,
	// unmodified, into the NOTIFICATION.unsubscribed analytics event.
	//
	// # Possible errors
	//
	//   - Internal: subscription deletion failure.
	Delete(ctx context.Context, userID, endpoint, deviceType string) error
}

// pushSubscriptionUseCase implements PushSubscriptionUseCase.
type pushSubscriptionUseCase struct {
	pushSubRepo entity.PushSubscriptionRepository
	publisher   EventPublisher
	logger      *logging.Logger
}

// Compile-time interface compliance check.
var _ PushSubscriptionUseCase = (*pushSubscriptionUseCase)(nil)

// NewPushSubscriptionUseCase creates a new PushSubscriptionUseCase.
func NewPushSubscriptionUseCase(
	pushSubRepo entity.PushSubscriptionRepository,
	publisher EventPublisher,
	logger *logging.Logger,
) PushSubscriptionUseCase {
	return &pushSubscriptionUseCase{
		pushSubRepo: pushSubRepo,
		publisher:   publisher,
		logger:      logger,
	}
}

// Create registers or updates the push subscription for the given (userID, endpoint) pair.
func (uc *pushSubscriptionUseCase) Create(ctx context.Context, userID, endpoint, p256dh, auth, deviceType string) (*entity.PushSubscription, error) {
	sub := &entity.PushSubscription{
		UserID:   userID,
		Endpoint: endpoint,
		P256dh:   p256dh,
		Auth:     auth,
	}
	if err := uc.pushSubRepo.Create(ctx, sub); err != nil {
		return nil, fmt.Errorf("failed to persist push subscription: %w", err)
	}

	if err := uc.publisher.PublishEvent(ctx, entity.SubjectNotificationSubscribed, entity.NotificationSubscribedData{
		UserID:        userID,
		DeviceType:    deviceType,
		SubscribeTime: time.Now(),
	}); err != nil {
		uc.logger.Error(ctx, "failed to publish NOTIFICATION.subscribed event", err,
			slog.String("user_id", userID),
		)
		// Non-fatal: the subscription is already persisted.
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
// Other browsers registered by the same user remain active. Idempotent.
// On success, a notification.unsubscribed analytics event is published
// non-fatally — a publish error is logged but does not change the return
// behaviour. The auto-cleanup path inside NotifyNewConcerts (410 Gone) does
// NOT call this method and therefore does NOT emit the analytics event.
func (uc *pushSubscriptionUseCase) Delete(ctx context.Context, userID, endpoint, deviceType string) error {
	if err := uc.pushSubRepo.Delete(ctx, userID, endpoint); err != nil {
		return fmt.Errorf("failed to delete push subscription: %w", err)
	}

	if err := uc.publisher.PublishEvent(ctx, entity.SubjectNotificationUnsubscribed, entity.NotificationUnsubscribedData{
		UserID:          userID,
		DeviceType:      deviceType,
		UnsubscribeTime: time.Now(),
	}); err != nil {
		uc.logger.Error(ctx, "failed to publish NOTIFICATION.unsubscribed event", err,
			slog.String("user_id", userID),
		)
		// Non-fatal: the subscription is already deleted.
	}

	return nil
}
