package usecase

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
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

	// NotifyNewConcerts sends Web Push notifications to followers of the given
	// artist, filtered by each follower's hype level. WATCH followers are skipped,
	// HOME followers receive notifications only when a concert venue matches their
	// home area, NEARBY followers receive notifications when a venue is within 200km,
	// and AWAY followers always receive them.
	// Per-subscription delivery errors (including 410 Gone responses) are handled
	// internally and do not cause the method to return an error.
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
	followRepo  entity.FollowRepository
	pushSubRepo entity.PushSubscriptionRepository
	sender      entity.PushNotificationSender
	logger      *logging.Logger
}

// Compile-time interface compliance check.
var _ PushNotificationUseCase = (*pushNotificationUseCase)(nil)

// NewPushNotificationUseCase creates a new PushNotificationUseCase.
func NewPushNotificationUseCase(
	followRepo entity.FollowRepository,
	pushSubRepo entity.PushSubscriptionRepository,
	sender entity.PushNotificationSender,
	logger *logging.Logger,
) PushNotificationUseCase {
	return &pushNotificationUseCase{
		followRepo:  followRepo,
		pushSubRepo: pushSubRepo,
		sender:      sender,
		logger:      logger,
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

// NotifyNewConcerts sends Web Push notifications to followers of the artist,
// filtered by each follower's hype level.
//
// Filtering rules:
//   - WATCH: no notification.
//   - HOME: notify only when at least one concert venue adminArea matches the follower's home.
//   - NEARBY: notify only when at least one concert venue is within 200km of the follower's home centroid.
//   - AWAY: always notify.
//
// Individual delivery failures are logged but do not cause the method to return an error.
func (uc *pushNotificationUseCase) NotifyNewConcerts(ctx context.Context, artist *entity.Artist, concerts []*entity.Concert) error {
	// 1. Retrieve all followers with their hype level and home area.
	followers, err := uc.followRepo.ListFollowers(ctx, artist.ID)
	if err != nil {
		return fmt.Errorf("failed to list followers for artist %s: %w", artist.ID, err)
	}
	if len(followers) == 0 {
		return nil
	}

	// 2. Collect unique venue admin areas from concerts for HOME filtering.
	venueAreas := make(map[string]struct{})
	for _, c := range concerts {
		if c.Venue != nil && c.Venue.AdminArea != nil {
			venueAreas[*c.Venue.AdminArea] = struct{}{}
		}
	}

	// 3. Filter followers by hype level and collect eligible user IDs.
	var userIDs []string
	for _, f := range followers {
		switch f.Hype {
		case entity.HypeWatch:
			// No notification for WATCH followers.
			continue
		case entity.HypeHome:
			// Notify only if any concert venue matches the follower's home area.
			homeLevel1 := ""
			if f.User != nil && f.User.Home != nil {
				homeLevel1 = f.User.Home.Level1
			}
			if homeLevel1 == "" {
				// No home area set: skip notification.
				continue
			}
			if _, ok := venueAreas[homeLevel1]; !ok {
				continue
			}
		case entity.HypeNearby:
			// Notify only if any concert venue is within the nearby threshold.
			if f.User == nil || f.User.Home == nil {
				continue
			}
			if !hasNearbyConcert(f.User.Home, concerts) {
				continue
			}
		case entity.HypeAway:
			// Always notify.
		default:
			// Unknown hype level: skip to be safe.
			uc.logger.Warn(ctx, "unknown hype level, skipping notification",
				slog.String("user_id", f.User.ID),
				slog.String("hype", string(f.Hype)),
			)
			continue
		}
		userIDs = append(userIDs, f.User.ID)
	}
	if len(userIDs) == 0 {
		return nil
	}

	subs, err := uc.pushSubRepo.ListByUserIDs(ctx, userIDs)
	if err != nil {
		return fmt.Errorf("failed to list push subscriptions: %w", err)
	}
	if len(subs) == 0 {
		return nil
	}

	// 4. Build the JSON notification payload.
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

	// 5. Send a notification to each subscription.
	for _, sub := range subs {
		// Honour context cancellation before each outbound request.
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if err := uc.sender.Send(ctx, payloadBytes, sub); err != nil {
			if errors.Is(err, apperr.ErrNotFound) {
				if delErr := uc.pushSubRepo.DeleteByEndpoint(ctx, sub.Endpoint); delErr != nil {
					uc.logger.Error(ctx, "failed to delete stale push subscription", delErr,
						slog.String("endpoint", sub.Endpoint),
					)
				}
			} else {
				uc.logger.Error(ctx, "failed to send push notification", err,
					slog.String("user_id", sub.UserID),
					slog.String("artist_id", artist.ID),
				)
			}
		}
	}

	return nil
}

// hasNearbyConcert returns true if at least one concert venue is classified as
// HOME or NEARBY relative to the given home area.
func hasNearbyConcert(home *entity.Home, concerts []*entity.Concert) bool {
	for _, c := range concerts {
		p := c.ProximityTo(home)
		if p == entity.ProximityHome || p == entity.ProximityNearby {
			return true
		}
	}
	return false
}
