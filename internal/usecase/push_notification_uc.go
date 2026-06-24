package usecase

import (
	"context"
	"encoding/json"
	"errors"
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
	// Per-subscription delivery errors (including 410 Gone responses) are handled
	// internally and do not cause the method to return an error.
	//
	// # Possible errors
	//
	//   - Internal: failure to look up artist, concerts, followers, or subscriptions.
	NotifyNewConcerts(ctx context.Context, data ConcertCreatedData) error
}

// pushNotificationUseCase implements PushNotificationUseCase.
type pushNotificationUseCase struct {
	artistRepo  entity.ArtistRepository
	concertRepo entity.ConcertRepository
	followRepo  entity.FollowRepository
	pushSubRepo entity.PushSubscriptionRepository
	sender      entity.PushNotificationSender
	publisher   EventPublisher
	metrics     PushMetrics
	logger      *logging.Logger
}

// Compile-time interface compliance check.
var _ PushNotificationUseCase = (*pushNotificationUseCase)(nil)

// NewPushNotificationUseCase creates a new PushNotificationUseCase.
func NewPushNotificationUseCase(
	artistRepo entity.ArtistRepository,
	concertRepo entity.ConcertRepository,
	followRepo entity.FollowRepository,
	pushSubRepo entity.PushSubscriptionRepository,
	sender entity.PushNotificationSender,
	publisher EventPublisher,
	metrics PushMetrics,
	logger *logging.Logger,
) PushNotificationUseCase {
	return &pushNotificationUseCase{
		artistRepo:  artistRepo,
		concertRepo: concertRepo,
		followRepo:  followRepo,
		pushSubRepo: pushSubRepo,
		sender:      sender,
		publisher:   publisher,
		metrics:     metrics,
		logger:      logger,
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
func (uc *pushNotificationUseCase) Delete(ctx context.Context, userID, endpoint string) error {
	if err := uc.pushSubRepo.Delete(ctx, userID, endpoint); err != nil {
		return fmt.Errorf("failed to delete push subscription: %w", err)
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

	subs, err := uc.pushSubRepo.ListByUserIDs(ctx, userIDs)
	if err != nil {
		return fmt.Errorf("failed to list push subscriptions: %w", err)
	}
	if len(subs) == 0 {
		return nil
	}

	// 4. Lazily build one JSON payload per distinct recipient language so copy is
	//    localized without re-marshalling for every subscription (≤2 marshals for en+ja).
	payloadByLang := make(map[string][]byte)
	payloadFor := func(lang string) ([]byte, error) {
		if b, ok := payloadByLang[lang]; ok {
			return b, nil
		}
		b, err := json.Marshal(&entity.NotificationPayload{
			Title: artist.Name,
			Body:  concertNotificationBody(len(concerts), lang),
			URL:   fmt.Sprintf("/concerts?artist=%s", artist.ID),
			Tag:   fmt.Sprintf("concert-%s", artist.ID),
		})
		if err != nil {
			return nil, err
		}
		payloadByLang[lang] = b
		return b, nil
	}

	// 5. Send a notification to each subscription in the recipient's language.
	for _, sub := range subs {
		// Honour context cancellation before each outbound request.
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		payloadBytes, err := payloadFor(langByUser[sub.UserID])
		if err != nil {
			return fmt.Errorf("failed to marshal push notification payload: %w", err)
		}

		if err := uc.sender.Send(ctx, payloadBytes, sub); err != nil {
			if errors.Is(err, apperr.ErrNotFound) {
				uc.metrics.RecordPushSend(ctx, "gone")
				// Scoped cleanup: delete only the dead (userID, endpoint) pair, not
				// every subscription that happens to share the endpoint URL.
				if delErr := uc.pushSubRepo.Delete(ctx, sub.UserID, sub.Endpoint); delErr != nil {
					uc.logger.Error(ctx, "failed to delete stale push subscription", delErr,
						slog.String("user_id", sub.UserID),
						slog.String("endpoint", sub.Endpoint),
					)
				}
			} else {
				uc.metrics.RecordPushSend(ctx, "error")
				uc.logger.Error(ctx, "failed to send push notification", err,
					slog.String("user_id", sub.UserID),
					slog.String("artist_id", artist.ID),
				)
			}
		} else {
			uc.metrics.RecordPushSend(ctx, "success")
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
