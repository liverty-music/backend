package rdb

import (
	"context"
	"log/slog"

	"github.com/google/uuid"
	"github.com/liverty-music/backend/internal/entity"
)

// PushSubscriptionRepository implements entity.PushSubscriptionRepository for PostgreSQL.
type PushSubscriptionRepository struct {
	db *Database
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
	`
	getPushSubscriptionQuery = `
		SELECT id, user_id, endpoint, p256dh, auth
		FROM push_subscriptions
		WHERE user_id = $1 AND endpoint = $2
	`
	deletePushSubscriptionQuery = `
		DELETE FROM push_subscriptions
		WHERE user_id = $1 AND endpoint = $2
	`
	listPushSubscriptionsByUserIDsQuery = `
		SELECT id, user_id, endpoint, p256dh, auth
		FROM push_subscriptions
		WHERE user_id = ANY($1::uuid[])
	`
)

// NewPushSubscriptionRepository creates a new push subscription repository instance.
func NewPushSubscriptionRepository(db *Database) *PushSubscriptionRepository {
	return &PushSubscriptionRepository{db: db}
}

// Create persists a new push subscription using an UPSERT on the endpoint column.
// If a subscription with the same endpoint already exists, its user_id, p256dh,
// and auth fields are updated in place.
func (r *PushSubscriptionRepository) Create(ctx context.Context, sub *entity.PushSubscription) error {
	if sub.ID == "" {
		id, _ := uuid.NewV7()
		sub.ID = id.String()
	}

	_, err := r.db.Pool.Exec(ctx, upsertPushSubscriptionQuery,
		sub.ID, sub.UserID, sub.Endpoint, sub.P256dh, sub.Auth,
	)
	if err != nil {
		return toAppErr(err, "failed to upsert push subscription",
			slog.String("user_id", sub.UserID),
		)
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

// Delete removes the push subscription uniquely identified by the (userID, endpoint) pair.
// The operation is idempotent: deleting a subscription that does not exist returns nil.
func (r *PushSubscriptionRepository) Delete(ctx context.Context, userID, endpoint string) error {
	_, err := r.db.Pool.Exec(ctx, deletePushSubscriptionQuery, userID, endpoint)
	if err != nil {
		return toAppErr(err, "failed to delete push subscription",
			slog.String("user_id", userID),
		)
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
