package usecase

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
)

// PushNotificationUseCase defines the interface for Web Push notification business logic.
type PushNotificationUseCase interface {
	// Create registers or updates the browser push subscription for the given
	// (userID, endpoint) pair. The subscription is keyed by endpoint: calling
	// Create with an endpoint that already exists updates the record in place.
	//
	// # Possible errors
	//
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
	// active. The operation is idempotent.
	//
	// # Possible errors
	//
	//   - Internal: subscription deletion failure.
	Delete(ctx context.Context, userID, endpoint string) error

	// NotifyNewConcerts sends Web Push notifications to followers of the given
	// artist for the specified newly created concerts. The delivery pipeline
	// hydrates the artist and concert entities internally, applies hype-level
	// filtering, and dispatches push notifications to all eligible followers.
	//
	// Each eligible recipient is dispatched through the notification service, so
	// a durable record and a delivery outcome exist per recipient. Per-channel
	// delivery errors (including 410 Gone responses) are recorded as failed by
	// the service and do not cause the method to return an error; only a
	// notification record-creation failure (which suppresses the send) is
	// surfaced, so the consumer's at-least-once retry re-drives the batch.
	//
	// # Possible errors
	//
	//   - Internal: failure to look up artist, concerts, or followers, or to
	//     create a notification record.
	NotifyNewConcerts(ctx context.Context, data ConcertCreatedData) error
}

// pushNotificationUseCase implements PushNotificationUseCase.
type pushNotificationUseCase struct {
	artistRepo     entity.ArtistRepository
	concertRepo    entity.ConcertRepository
	followRepo     entity.FollowRepository
	pushSubRepo    entity.PushSubscriptionRepository
	publisher      EventPublisher
	notificationUC NotificationUseCase
	logger         *logging.Logger
}

// Compile-time interface compliance check.
var _ PushNotificationUseCase = (*pushNotificationUseCase)(nil)

// NewPushNotificationUseCase creates a new PushNotificationUseCase.
func NewPushNotificationUseCase(
	artistRepo entity.ArtistRepository,
	concertRepo entity.ConcertRepository,
	followRepo entity.FollowRepository,
	pushSubRepo entity.PushSubscriptionRepository,
	publisher EventPublisher,
	notificationUC NotificationUseCase,
	logger *logging.Logger,
) PushNotificationUseCase {
	return &pushNotificationUseCase{
		artistRepo:     artistRepo,
		concertRepo:    concertRepo,
		followRepo:     followRepo,
		pushSubRepo:    pushSubRepo,
		publisher:      publisher,
		notificationUC: notificationUC,
		logger:         logger,
	}
}

// Create registers or updates the push subscription for the given (userID, endpoint) pair.
func (uc *pushNotificationUseCase) Create(ctx context.Context, userID, endpoint, p256dh, auth string) (*entity.PushSubscription, error) {
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
		UserID:     userID,
		DeviceType: entity.DeviceTypeFromEndpoint(endpoint),
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
func (uc *pushNotificationUseCase) Get(ctx context.Context, userID, endpoint string) (*entity.PushSubscription, error) {
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
func (uc *pushNotificationUseCase) Delete(ctx context.Context, userID, endpoint string) error {
	if err := uc.pushSubRepo.Delete(ctx, userID, endpoint); err != nil {
		return fmt.Errorf("failed to delete push subscription: %w", err)
	}

	if err := uc.publisher.PublishEvent(ctx, entity.SubjectNotificationUnsubscribed, entity.NotificationUnsubscribedData{
		UserID:     userID,
		DeviceType: entity.DeviceTypeFromEndpoint(endpoint),
	}); err != nil {
		uc.logger.Error(ctx, "failed to publish NOTIFICATION.unsubscribed event", err,
			slog.String("user_id", userID),
		)
		// Non-fatal: the subscription is already deleted.
	}

	return nil
}

// NotifyNewConcerts sends Web Push notifications to followers of the artist,
// filtered by each follower's hype level. Only the concerts identified in data
// are used for hype filtering and payload computation.
//
// Filtering rules:
//   - WATCH: no notification.
//   - HOME: notify only when at least one concert venue adminArea matches the follower's home.
//   - NEARBY: notify only when at least one concert venue is within 200km of the follower's home centroid.
//   - AWAY: always notify.
//
// Individual delivery failures are logged but do not cause the method to return an error.
func (uc *pushNotificationUseCase) NotifyNewConcerts(ctx context.Context, data ConcertCreatedData) error {
	// 0. Hydrate artist and concerts from their IDs.
	artist, err := uc.artistRepo.Get(ctx, data.ArtistID)
	if err != nil {
		return fmt.Errorf("failed to get artist %s: %w", data.ArtistID, err)
	}

	concerts, err := uc.concertRepo.ListByIDs(ctx, data.ConcertIDs)
	if err != nil {
		return fmt.Errorf("failed to list concerts by IDs: %w", err)
	}

	// Validate that every requested concert exists and that the specified
	// artist is one of its performers. With M:N performers, "belongs to" is
	// "is a performer at" — a single concert (e.g. a festival) may legitimately
	// belong to multiple artists. This protects against operator mistakes on
	// the debug RPC path and bad publisher state on the event path.
	hasPerformer := make(map[string]bool, len(concerts))
	// orphanConcerts records concerts whose Performers slice is empty after
	// hydration. The new M:N schema allows a structurally valid event row
	// to exist with no event_performers links (e.g. a race between Create
	// and the natural-key JOIN in insertEventPerformersQuery, or an
	// orphaned data state). Treat these as non-fatal — log + skip — rather
	// than aborting the whole batch and indefinitely retrying the Pub/Sub
	// message, which would block notifications for every other concert.
	orphanConcerts := make(map[string]bool, len(concerts))
	for _, c := range concerts {
		if len(c.Performers) == 0 {
			orphanConcerts[c.ID] = true
			uc.logger.Warn(ctx, "concert has no performers after hydration; skipping membership check",
				slog.String("concert_id", c.ID),
				slog.String("artist_id", data.ArtistID),
			)
		}
		hasPerformer[c.ID] = false
		for _, p := range c.Performers {
			if p != nil && p.ID == data.ArtistID {
				hasPerformer[c.ID] = true
				break
			}
		}
	}
	for _, id := range data.ConcertIDs {
		performs, exists := hasPerformer[id]
		if !exists {
			return apperr.New(codes.InvalidArgument, "concert_id "+id+" does not exist")
		}
		if orphanConcerts[id] {
			// Already logged above; do not fail the batch on a data anomaly.
			continue
		}
		if !performs {
			return apperr.New(codes.InvalidArgument, "concert_id "+id+" does not feature artist "+data.ArtistID)
		}
	}

	// Drop orphan concerts from the working slice. The membership check
	// skipped them above so they don't abort the batch, but if they
	// stayed in `concerts` they would still feed venueAreas and the
	// ShouldNotify input — qualifying a follower for HypeHome /
	// HypeNearby on a concert whose performer membership was never
	// confirmed (orphan event_performers state).
	if len(orphanConcerts) > 0 {
		kept := concerts[:0]
		for _, c := range concerts {
			if !orphanConcerts[c.ID] {
				kept = append(kept, c)
			}
		}
		concerts = kept
	}

	// If every concert was an orphan, there's nothing real to notify
	// about — short-circuit before the hype loop. Without this guard,
	// HypeAway.ShouldNotify returns true unconditionally and every
	// HypeAway follower receives a push with a "0 new concerts" payload
	// (concertNotificationBody formats the count from len(concerts)).
	if len(concerts) == 0 {
		return nil
	}

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

	// 3. Filter followers by hype level and collect eligible user IDs, recording
	//    each recipient's resolved language for per-user copy localization.
	var userIDs []string
	langByUser := make(map[string]string)
	for _, f := range followers {
		// f.User may be nil if the join with users dropped a row (e.g.
		// orphaned follow). Skip the whole follower in that case — the
		// subsequent f.User.ID dereference would panic for any non-Watch
		// hype tier because HypeAway.ShouldNotify always returns true and
		// HypeHome may return true without ever reading f.User.Home.
		if f.User == nil {
			continue
		}
		if !f.Hype.ShouldNotify(f.User.Home, venueAreas, concerts) {
			continue
		}
		userIDs = append(userIDs, f.User.ID)
		langByUser[f.User.ID] = f.User.PreferredLanguage
	}
	if len(userIDs) == 0 {
		return nil
	}

	// 4. Record and dispatch one notification per eligible recipient through the
	//    notification service, so every recipient gets a durable record and a
	//    delivery outcome. The service resolves each recipient's push
	//    subscriptions, performs the send, cleans up gone (410) endpoints, and
	//    records delivered/failed. Copy is localized per recipient by language.
	for _, userID := range userIDs {
		// Honour context cancellation before each recipient.
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		payload := entity.NewNotificationPayload(
			artist.Name,
			concertNotificationBody(len(concerts), langByUser[userID]),
			fmt.Sprintf("/concerts?artist=%s", artist.ID),
			fmt.Sprintf("concert-%s", artist.ID),
		)
		if _, err := uc.notificationUC.Notify(ctx, userID, entity.NotificationTypeNewConcerts, payload); err != nil {
			// Record-create failure ("no record => no send"): surface so the
			// consumer's at-least-once retry re-drives the batch. Repeat web
			// pushes are deduplicated browser-side by the per-artist Tag.
			return fmt.Errorf("failed to notify user %s of new concerts for artist %s: %w", userID, artist.ID, err)
		}
	}

	return nil
}

// concertNotificationBody renders the new-concert count in the recipient's
// language, falling back to English for empty or unsupported codes.
func concertNotificationBody(concertCount int, lang string) string {
	switch lang {
	case "ja":
		return fmt.Sprintf("新しいライブが%d件見つかりました", concertCount)
	default:
		if concertCount == 1 {
			return "1 new concert found"
		}
		return fmt.Sprintf("%d new concerts found", concertCount)
	}
}
