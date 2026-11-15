package usecase

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/pkg/analyticslog"
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
// A recipient is notified only when their subset is non-empty. The subject names
// the artist and the subset; the Notification entity renders the count and the
// link to the earliest concert of the subset from it.
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

	// Every requested concert must exist and feature the artist; a concert
	// with no performers is a data anomaly that is skipped rather than
	// failing (and redelivering) the whole batch.
	concerts, orphanIDs, err := entity.NewConcertsOfArtist(data.ArtistID, data.ConcertIDs, concerts)
	if err != nil {
		return err
	}
	for _, id := range orphanIDs {
		uc.logger.Warn(ctx, "concert has no performers after hydration; skipping membership check",
			slog.String("concert_id", id),
			slog.String("artist_id", data.ArtistID),
		)
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

	dispatched, err := uc.notifyFollowers(ctx, artist.ID, followers, concerts, requestID)
	if err != nil {
		return err
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

// notifyFollowers narrows the new-concert set to each follower's hype-matched
// subset, then starts one Deliver per eligible recipient, which records and
// dispatches the notification: the notification service resolves each
// recipient's push subscriptions, performs the send and cleans up gone (410)
// endpoints. It returns how many it started.
//
// The subject names the per-recipient subset — never the unfiltered
// new-concert set — so a home-hype fan sees an area-accurate count and lands
// on a concert that actually matched their hype tier.
func (uc *notificationUseCase) notifyFollowers(ctx context.Context, artistID string, followers []*entity.Follower, concerts []*entity.Concert, requestID string) (int, error) {
	dispatched := 0
	for _, f := range followers {
		// Honour context cancellation before each recipient.
		if err := ctx.Err(); err != nil {
			return dispatched, err
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

		eventIDs := make([]string, len(subset))
		for i, c := range subset {
			eventIDs[i] = c.ID
		}
		id := deliverWorkflowID(entity.NotificationTypeNewConcerts, f.User.ID, requestID)
		if err := uc.startDeliver(ctx, id, DeliverRequest{
			UserID:  f.User.ID,
			Type:    entity.NotificationTypeNewConcerts,
			Subject: entity.NotificationSubject{ArtistID: artistID, EventIDs: eventIDs},
		}); err != nil {
			// Surface so the announcement is processed again; the followers
			// already started are not started again (same workflow id).
			return dispatched, fmt.Errorf("failed to start the notification of user %s of new concerts for artist %s: %w", f.User.ID, artistID, err)
		}
		dispatched++
	}
	return dispatched, nil
}
