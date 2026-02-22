package usecase

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	webpush "github.com/SherClockHolmes/webpush-go"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-logging/logging"
)

// PushNotificationUseCase defines the interface for Web Push notification business logic.
type PushNotificationUseCase interface {
	// Subscribe registers or updates the browser push subscription for the given user.
	//
	// # Possible errors
	//
	//   - Internal: subscription persistence failure.
	Subscribe(ctx context.Context, userID, endpoint, p256dh, auth string) error

	// Unsubscribe removes all push subscriptions associated with the given user.
	//
	// # Possible errors
	//
	//   - Internal: subscription deletion failure.
	Unsubscribe(ctx context.Context, userID string) error

	// NotifyNewConcerts sends a Web Push notification to all followers of the given
	// artist, announcing the newly discovered concerts. Per-subscription delivery
	// errors (including 410 Gone responses) are handled internally and do not cause
	// the method to return an error.
	//
	// # Possible errors
	//
	//   - Internal: failure to look up followers or their subscriptions.
	NotifyNewConcerts(ctx context.Context, artist *entity.Artist, concerts []*entity.Concert) error
}

// pushNotificationPayload is the JSON structure sent as the push message body.
type pushNotificationPayload struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	URL   string `json:"url"`
	Tag   string `json:"tag"`
}

// pushNotificationUseCase implements PushNotificationUseCase.
type pushNotificationUseCase struct {
	artistRepo     entity.ArtistRepository
	pushSubRepo    entity.PushSubscriptionRepository
	logger         *logging.Logger
	vapidPublicKey string
	vapidPrivate   string
	vapidContact   string
}

// Compile-time interface compliance check.
var _ PushNotificationUseCase = (*pushNotificationUseCase)(nil)

// NewPushNotificationUseCase creates a new PushNotificationUseCase.
// vapidContact must be a mailto: URI (e.g., "mailto:admin@example.com").
func NewPushNotificationUseCase(
	artistRepo entity.ArtistRepository,
	pushSubRepo entity.PushSubscriptionRepository,
	logger *logging.Logger,
	vapidPublicKey, vapidPrivateKey, vapidContact string,
) PushNotificationUseCase {
	return &pushNotificationUseCase{
		artistRepo:     artistRepo,
		pushSubRepo:    pushSubRepo,
		logger:         logger,
		vapidPublicKey: vapidPublicKey,
		vapidPrivate:   vapidPrivateKey,
		vapidContact:   vapidContact,
	}
}

// Subscribe registers or updates the push subscription for the given user.
func (uc *pushNotificationUseCase) Subscribe(ctx context.Context, userID, endpoint, p256dh, auth string) error {
	sub := &entity.PushSubscription{
		UserID:   userID,
		Endpoint: endpoint,
		P256dh:   p256dh,
		Auth:     auth,
	}
	if err := uc.pushSubRepo.Create(ctx, sub); err != nil {
		return fmt.Errorf("failed to persist push subscription: %w", err)
	}
	return nil
}

// Unsubscribe removes all push subscriptions for the given user.
func (uc *pushNotificationUseCase) Unsubscribe(ctx context.Context, userID string) error {
	if err := uc.pushSubRepo.DeleteByUserID(ctx, userID); err != nil {
		return fmt.Errorf("failed to delete push subscriptions: %w", err)
	}
	return nil
}

// NotifyNewConcerts sends Web Push notifications to all followers of the artist.
// Individual delivery failures are logged but do not cause the method to return an error.
func (uc *pushNotificationUseCase) NotifyNewConcerts(ctx context.Context, artist *entity.Artist, concerts []*entity.Concert) error {
	// 1. Retrieve all followers of the artist.
	followers, err := uc.artistRepo.ListFollowers(ctx, artist.ID)
	if err != nil {
		return fmt.Errorf("failed to list followers for artist %s: %w", artist.ID, err)
	}
	if len(followers) == 0 {
		return nil
	}

	// 2. Collect user IDs and fetch their push subscriptions.
	userIDs := make([]string, len(followers))
	for i, f := range followers {
		userIDs[i] = f.ID
	}

	subs, err := uc.pushSubRepo.ListByUserIDs(ctx, userIDs)
	if err != nil {
		return fmt.Errorf("failed to list push subscriptions: %w", err)
	}
	if len(subs) == 0 {
		return nil
	}

	// 3. Build the JSON notification payload.
	payload := pushNotificationPayload{
		Title: artist.Name,
		Body:  fmt.Sprintf("%d new concerts found", len(concerts)),
		URL:   fmt.Sprintf("/concerts?artist=%s", artist.ID),
		Tag:   fmt.Sprintf("concert-%s", artist.ID),
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal push notification payload: %w", err)
	}

	// 4. Send a notification to each subscription.
	for _, sub := range subs {
		resp, err := webpush.SendNotification(payloadBytes, &webpush.Subscription{
			Endpoint: sub.Endpoint,
			Keys: webpush.Keys{
				P256dh: sub.P256dh,
				Auth:   sub.Auth,
			},
		}, &webpush.Options{
			VAPIDPublicKey:  uc.vapidPublicKey,
			VAPIDPrivateKey: uc.vapidPrivate,
			Subscriber:      uc.vapidContact,
		})
		if err != nil {
			uc.logger.Error(ctx, "failed to send push notification", err,
				slog.String("user_id", sub.UserID),
				slog.String("artist_id", artist.ID),
			)
			continue
		}
		if closeErr := resp.Body.Close(); closeErr != nil {
			uc.logger.Error(ctx, "failed to close push notification response body", closeErr,
				slog.String("user_id", sub.UserID),
			)
		}

		// A 410 Gone response means the subscription is no longer valid.
		if resp.StatusCode == http.StatusGone {
			if delErr := uc.pushSubRepo.DeleteByEndpoint(ctx, sub.Endpoint); delErr != nil {
				uc.logger.Error(ctx, "failed to delete stale push subscription", delErr,
					slog.String("endpoint", sub.Endpoint),
				)
			}
		}
	}

	return nil
}
