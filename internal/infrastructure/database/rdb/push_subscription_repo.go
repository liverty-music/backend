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
			user_id = $2,
			p256dh  = $3,
			auth    = $4
	`
	deletePushSubscriptionByEndpointQuery = `
		DELETE FROM push_subscriptions
		WHERE endpoint = $1
	`
	listPushSubscriptionsByUserIDsQuery = `
		SELECT id, user_id, endpoint, p256dh, auth
		FROM push_subscriptions
		WHERE user_id = ANY($1::uuid[])
	`
	deletePushSubscriptionsByUserIDQuery = `
		DELETE FROM push_subscriptions
		WHERE user_id = $1
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

// DeleteByEndpoint removes the push subscription identified by the given endpoint URL.
func (r *PushSubscriptionRepository) DeleteByEndpoint(ctx context.Context, endpoint string) error {
	_, err := r.db.Pool.Exec(ctx, deletePushSubscriptionByEndpointQuery, endpoint)
	if err != nil {
		return toAppErr(err, "failed to delete push subscription by endpoint",
			slog.String("endpoint", endpoint),
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

// DeleteByUserID removes all push subscriptions associated with the given user.
func (r *PushSubscriptionRepository) DeleteByUserID(ctx context.Context, userID string) error {
	_, err := r.db.Pool.Exec(ctx, deletePushSubscriptionsByUserIDQuery, userID)
	if err != nil {
		return toAppErr(err, "failed to delete push subscriptions by user ID",
			slog.String("user_id", userID),
		)
	}
	return nil
}
