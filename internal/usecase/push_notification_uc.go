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
// For each follower the new-concert set is narrowed to that recipient's
// hype-matched subset (see [entity.Hype.MatchingConcerts]):
//   - WATCH: empty subset → no notification.
//   - HOME: concerts whose venue adminArea matches the follower's home area.
//   - NEARBY: concerts within 200km of the follower's home centroid.
//   - AWAY: all concerts.
//
// A recipient is notified only when their subset is non-empty. The body count is
// the subset size, and the deep-link (data.url) targets the earliest concert of
// the subset (local date asc, tie-broken by start time asc).
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
	// stayed in `concerts` they would still feed MatchingConcerts —
	// qualifying a follower for HypeHome / HypeNearby (or padding the
	// HypeAway count and deep-link) on a concert whose performer
	// membership was never confirmed (orphan event_performers state).
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
	// HypeAway.MatchingConcerts would return an empty subset for every
	// recipient (so no push is sent), but short-circuiting here also skips
	// the follower lookup entirely.
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

	// 2. For each follower, narrow the new-concert set to that recipient's
	//    hype-matched subset, then record and dispatch one notification per
	//    eligible recipient through the notification service. Every recipient
	//    gets a durable record and a delivery outcome; the service resolves each
	//    recipient's push subscriptions, performs the send, cleans up gone (410)
	//    endpoints, and records delivered/failed.
	//
	//    Both the body count and the deep-link target are computed from the
	//    per-recipient subset — never from the unfiltered new-concert set — so a
	//    home-hype fan sees an area-accurate count and lands on a concert that
	//    actually matched their hype tier.
	for _, f := range followers {
		// Honour context cancellation before each recipient.
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// f.User may be nil if the join with users dropped a row (e.g. an
		// orphaned follow). Skip the whole follower in that case — the
		// MatchingConcerts call reads f.User.Home and the dispatch reads
		// f.User.ID, both of which would panic on a nil user.
		if f.User == nil {
			continue
		}

		subset := f.Hype.MatchingConcerts(f.User.Home, concerts)
		if len(subset) == 0 {
			continue
		}

		earliest := entity.EarliestConcert(subset)
		if earliest == nil {
			// Defensive: a non-empty subset should always yield an earliest
			// concert. Skip rather than emit a notification with no deep-link.
			continue
		}

		payload := entity.NewNotificationPayload(
			artist.Name,
			concertNotificationBody(len(subset), f.User.PreferredLanguage),
			// Deep-link to the earliest hype-matched concert. The frontend's
			// canonical concert-detail URL routes to the dashboard and opens the
			// concert's detail sheet, filtered to its artist.
			fmt.Sprintf("/concerts/%s", earliest.ID),
			fmt.Sprintf("concert-%s", artist.ID),
		)
		if _, err := uc.notificationUC.Notify(ctx, f.User.ID, entity.NotificationTypeNewConcerts, payload); err != nil {
			// Record-create failure ("no record => no send"): surface so the
			// consumer's at-least-once retry re-drives the batch. Repeat web
			// pushes are deduplicated browser-side by the per-artist Tag.
			return fmt.Errorf("failed to notify user %s of new concerts for artist %s: %w", f.User.ID, artist.ID, err)
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
