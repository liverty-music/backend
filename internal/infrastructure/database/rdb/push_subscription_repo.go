package rdb

import (
	"context"
	"errors"
	"log/slog"

	"github.com/jackc/pgx/v5"

	"github.com/liverty-music/backend/internal/adapter/pb"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/messaging"
)

// PushSubscriptionRepository implements entity.PushSubscriptionRepository for PostgreSQL.
type PushSubscriptionRepository struct {
	db     *Database
	outbox Announcer
}

// Compile-time interface compliance check.
var _ entity.PushSubscriptionRepository = (*PushSubscriptionRepository)(nil)

const (
	upsertPushSubscriptionQuery = `
		INSERT INTO push_subscriptions (id, user_id, endpoint, p256dh, auth)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (endpoint) DO UPDATE SET
			user_id = EXCLUDED.user_id,
			p256dh  = EXCLUDED.p256dh,
			auth    = EXCLUDED.auth
		WHERE (push_subscriptions.user_id, push_subscriptions.p256dh, push_subscriptions.auth)
			IS DISTINCT FROM (EXCLUDED.user_id, EXCLUDED.p256dh, EXCLUDED.auth)
		RETURNING id
	`
	// getPushSubscriptionIDByEndpointQuery reads the id of the subscription
	// an unchanged upsert left as it was.
	getPushSubscriptionIDByEndpointQuery = `
		SELECT id FROM push_subscriptions WHERE endpoint = $1
	`
	getPushSubscriptionQuery = `
		SELECT id, user_id, endpoint, p256dh, auth
		FROM push_subscriptions
		WHERE user_id = $1 AND endpoint = $2
	`
	deletePushSubscriptionQuery = `
		DELETE FROM push_subscriptions
		WHERE user_id = $1 AND endpoint = $2
		RETURNING id, user_id, endpoint, p256dh, auth
	`
	listPushSubscriptionsByUserIDsQuery = `
		SELECT id, user_id, endpoint, p256dh, auth
		FROM push_subscriptions
		WHERE user_id = ANY($1::uuid[])
	`
)

// NewPushSubscriptionRepository creates a new push subscription repository instance.
func NewPushSubscriptionRepository(db *Database, outbox Announcer) *PushSubscriptionRepository {
	return &PushSubscriptionRepository{db: db, outbox: outbox}
}

// Create stores the subscription, or updates the one with the same endpoint
// in place (its keys and owner replaced, its id kept); one that already has
// the given owner and keys is left unchanged. sub.ID is set to the stored id.
//
// # Announces
//
// NOTIFICATION.subscribed with the PushSubscription after the change,
// without its push address and keys, in the transaction of the change, when
// it stored a new subscription or changed one. Nothing when nothing changed.
func (r *PushSubscriptionRepository) Create(ctx context.Context, sub *entity.PushSubscription) error {
	if sub.ID == "" {
		sub.ID = entity.NewID()
	}
	attrs := []slog.Attr{slog.String("user_id", sub.UserID)}
	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return toAppErr(err, "failed to begin push subscription upsert", attrs...)
	}
	// Rollback is a no-op after a successful Commit.
	defer func() { _ = tx.Rollback(ctx) }()

	err = tx.QueryRow(ctx, upsertPushSubscriptionQuery, sub.ID, sub.UserID, sub.Endpoint, sub.P256dh, sub.Auth).Scan(&sub.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		if err := tx.QueryRow(ctx, getPushSubscriptionIDByEndpointQuery, sub.Endpoint).Scan(&sub.ID); err != nil {
			return toAppErr(err, "failed to read the unchanged push subscription", attrs...)
		}
		return nil
	}
	if err != nil {
		return toAppErr(err, "failed to upsert push subscription", attrs...)
	}
	if err := announce(ctx, r.outbox, tx, entity.SubjectNotificationSubscribed, messaging.Announcement{Payload: pb.FromPushSubscriptionForAnnouncement(sub)}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return toAppErr(err, "failed to commit push subscription upsert", attrs...)
	}
	return nil
}

// Get retrieves the push subscription uniquely identified by the (userID, endpoint) pair.
// Returns a NotFound application error when no row matches.
func (r *PushSubscriptionRepository) Get(ctx context.Context, userID, endpoint string) (*entity.PushSubscription, error) {
	var s entity.PushSubscription
	err := r.db.Pool.QueryRow(ctx, getPushSubscriptionQuery, userID, endpoint).
		Scan(&s.ID, &s.UserID, &s.Endpoint, &s.P256dh, &s.Auth)
	if err != nil {
		return nil, toAppErr(err, "failed to get push subscription",
			slog.String("user_id", userID),
		)
	}
	return &s, nil
}

// Delete removes the push subscription uniquely identified by the (userID,
// endpoint) pair. Deleting a subscription that does not exist succeeds.
//
// # Announces
//
// NOTIFICATION.unsubscribed with the PushSubscription as it was, without its
// push address and keys, in the transaction of the removal. Nothing when it
// removed nothing.
func (r *PushSubscriptionRepository) Delete(ctx context.Context, userID, endpoint string) error {
	attrs := []slog.Attr{slog.String("user_id", userID)}
	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return toAppErr(err, "failed to begin push subscription delete", attrs...)
	}
	// Rollback is a no-op after a successful Commit.
	defer func() { _ = tx.Rollback(ctx) }()

	var s entity.PushSubscription
	err = tx.QueryRow(ctx, deletePushSubscriptionQuery, userID, endpoint).Scan(&s.ID, &s.UserID, &s.Endpoint, &s.P256dh, &s.Auth)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return toAppErr(err, "failed to delete push subscription", attrs...)
	}
	if err := announce(ctx, r.outbox, tx, entity.SubjectNotificationUnsubscribed, messaging.Announcement{Payload: pb.FromPushSubscriptionForAnnouncement(&s)}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return toAppErr(err, "failed to commit push subscription delete", attrs...)
	}
	return nil
}

// ListByUserIDs retrieves all push subscriptions for a given set of user IDs.
// It returns an empty slice when no subscriptions are found.
func (r *PushSubscriptionRepository) ListByUserIDs(ctx context.Context, userIDs []string) ([]*entity.PushSubscription, error) {
	if len(userIDs) == 0 {
		return []*entity.PushSubscription{}, nil
	}

	rows, err := r.db.Pool.Query(ctx, listPushSubscriptionsByUserIDsQuery, userIDs)
	if err != nil {
		return nil, toAppErr(err, "failed to list push subscriptions by user IDs",
			slog.Int("user_count", len(userIDs)),
		)
	}
	defer rows.Close()

	var subs []*entity.PushSubscription
	for rows.Next() {
		var s entity.PushSubscription
		if err := rows.Scan(&s.ID, &s.UserID, &s.Endpoint, &s.P256dh, &s.Auth); err != nil {
			return nil, toAppErr(err, "failed to scan push subscription row")
		}
		subs = append(subs, &s)
	}
	if err := rows.Err(); err != nil {
		return nil, toAppErr(err, "error iterating push subscription rows")
	}
	return subs, nil
}
