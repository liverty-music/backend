package usecase

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/pkg/analyticslog"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
)

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
func (uc *notificationUseCase) NotifyNewConcerts(ctx context.Context, data ConcertCreatedData) error {
	// A direct call by an admin has no announcement id: each such call
	// notifies again.
	requestID := data.RequestID
	if requestID == "" {
		requestID = entity.NewID()
	}

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
	// belong to multiple artists. This protects against admin mistakes on
	// the debug RPC path and bad publisher state on the event path.
	hasPerformer := make(map[string]bool, len(concerts))
	// orphanConcerts records concerts whose Performers slice is empty after
	// hydration. The new M:N schema allows a structurally valid event row
	// to exist with no concert_artists links (e.g. a race between Create
	// and the natural-key JOIN in insertEventPerformersQuery, or an
	// orphaned data state). Treat these as non-fatal — log + skip — rather
	// than aborting the whole batch and indefinitely retrying the Pub/Sub
	// message, which would block notifications for every other concert.
	orphanConcerts := make(map[string]bool, len(concerts))
	for _, c := range concerts {
		if len(c.Artists) == 0 {
			orphanConcerts[c.ID] = true
			uc.logger.Warn(ctx, "concert has no performers after hydration; skipping membership check",
				slog.String("concert_id", c.ID),
				slog.String("artist_id", data.ArtistID),
			)
		}
		hasPerformer[c.ID] = false
		for _, p := range c.Artists {
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
	// membership was never confirmed (orphan concert_artists state).
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
		uc.logger.Info(ctx, "new-concert fan-out sent nothing: no deliverable concerts", analyticslog.Attrs(analyticslog.TopicNotificationDelivery,
			slog.String("artist_id", data.ArtistID),
			slog.String("reason", "no_deliverable_concerts"),
		)...)
		return nil
	}

	// 1. Retrieve all followers with their hype level and home area.
	followers, err := uc.followRepo.ListFollowers(ctx, artist.ID)
	if err != nil {
		return fmt.Errorf("failed to list followers for artist %s: %w", artist.ID, err)
	}
	if len(followers) == 0 {
		uc.logger.Info(ctx, "new-concert fan-out sent nothing: artist has no followers", analyticslog.Attrs(analyticslog.TopicNotificationDelivery,
			slog.String("artist_id", artist.ID),
			slog.String("reason", "no_followers"),
		)...)
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
	dispatched := 0
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
			// Deep-link to the earliest hype-matched concert.
			concertDeepLink(earliest),
			fmt.Sprintf("concert-%s", artist.ID),
		)
		id := deliverWorkflowID(entity.NotificationTypeNewConcerts, f.User.ID, requestID)
		if err := uc.startDeliver(ctx, id, DeliverRequest{
			UserID:  f.User.ID,
			Type:    entity.NotificationTypeNewConcerts,
			Payload: payload,
		}); err != nil {
			// Surface so the announcement is processed again; the followers
			// already started are not started again (same workflow id).
			return fmt.Errorf("failed to start the notification of user %s of new concerts for artist %s: %w", f.User.ID, artist.ID, err)
		}
		dispatched++
	}

	// The artist has followers, but none was eligible after hype filtering (every
	// recipient's matched subset was empty). Log the empty outcome so "processed
	// the event but sent nothing" is diagnosable from logs alone, rather than
	// returning silently.
	if dispatched == 0 {
		uc.logger.Info(ctx, "new-concert fan-out sent nothing: no eligible recipients after hype filtering", analyticslog.Attrs(analyticslog.TopicNotificationDelivery,
			slog.String("artist_id", artist.ID),
			slog.String("reason", "no_eligible_recipients"),
			slog.Int("follower_count", len(followers)),
		)...)
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

// concertDeepLink returns the fan web path a new-concert notification opens for
// c: the public event page /events/<id> when c's Series is first-party, and
// otherwise /concerts/<id>, which routes to the dashboard and opens the
// concert's detail sheet.
func concertDeepLink(c *entity.Concert) string {
	if c.Series != nil && c.Series.IsFirstParty() {
		return "/events/" + c.ID
	}
	return "/concerts/" + c.ID
}
