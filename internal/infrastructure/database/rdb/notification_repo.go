package rdb

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/liverty-music/backend/internal/entity"
)

// NotificationRepository implements entity.NotificationRepository for PostgreSQL.
type NotificationRepository struct {
	db *Database
}

// Compile-time interface compliance check.
var _ entity.NotificationRepository = (*NotificationRepository)(nil)

// defaultListByUserLimit bounds an inbox query when the caller passes a
// non-positive limit.
const defaultListByUserLimit = 50

const (
	createNotificationQuery = `
		INSERT INTO notifications (id, user_id, type, payload, delivery_status)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING queued_at
	`
	updateNotificationDeliveryQuery = `
		UPDATE notifications
		SET delivery_status = $2,
		    delivered_at = $3,
		    failure_reason = $4
		WHERE id = $1
	`
	markNotificationReadQuery = `
		UPDATE notifications
		SET read_at = NOW()
		WHERE id = $1 AND user_id = $2 AND read_at IS NULL
	`
	markNotificationDismissedQuery = `
		UPDATE notifications
		SET dismissed_at = NOW()
		WHERE id = $1 AND user_id = $2 AND dismissed_at IS NULL
	`
	getNotificationQuery = `
		SELECT id, user_id, type, payload, delivery_status, failure_reason, queued_at, delivered_at, read_at, dismissed_at
		FROM notifications
		WHERE id = $1
	`
	listNotificationsByUserQuery = `
		SELECT id, user_id, type, payload, delivery_status, failure_reason, queued_at, delivered_at, read_at, dismissed_at
		FROM notifications
		WHERE user_id = $1
		ORDER BY queued_at DESC
		LIMIT $2
	`
)

// NewNotificationRepository creates a new notification repository instance.
func NewNotificationRepository(db *Database) *NotificationRepository {
	return &NotificationRepository{db: db}
}

// Create persists a new notification in the queued state, minting a UUIDv7 id
// when n.ID is empty and writing back the server-assigned created_at.
func (r *NotificationRepository) Create(ctx context.Context, n *entity.Notification) error {
	if n.ID == "" {
		id, err := uuid.NewV7()
		if err != nil {
			return toAppErr(err, "failed to generate UUIDv7 for notification")
		}
		n.ID = id.String()
	}
	if n.DeliveryStatus == "" {
		n.DeliveryStatus = entity.NotificationDeliveryStatusQueued
	}

	payloadBytes, err := json.Marshal(n.Payload)
	if err != nil {
		return toAppErr(err, "failed to marshal notification payload",
			slog.String("user_id", n.UserID),
		)
	}

	err = r.db.Pool.QueryRow(ctx, createNotificationQuery,
		n.ID, n.UserID, string(n.Type), payloadBytes, string(n.DeliveryStatus),
	).Scan(&n.QueueTime)
	if err != nil {
		return toAppErr(err, "failed to create notification",
			slog.String("user_id", n.UserID),
			slog.String("type", string(n.Type)),
		)
	}
	return nil
}

// UpdateDelivery records the terminal delivery outcome for a notification.
// failureReason is stored as NULL when empty so a delivered record carries no
// spurious reason.
func (r *NotificationRepository) UpdateDelivery(ctx context.Context, id string, status entity.NotificationDeliveryStatus, deliveredAt *time.Time, failureReason string) error {
	var reason *string
	if failureReason != "" {
		reason = &failureReason
	}

	_, err := r.db.Pool.Exec(ctx, updateNotificationDeliveryQuery,
		id, string(status), deliveredAt, reason,
	)
	if err != nil {
		return toAppErr(err, "failed to update notification delivery",
			slog.String("id", id),
			slog.String("delivery_status", string(status)),
		)
	}
	return nil
}

// MarkRead sets read_at for the (userID, id) notification. The read_at IS NULL
// guard makes a repeat call a no-op, so the operation is idempotent.
func (r *NotificationRepository) MarkRead(ctx context.Context, userID, id string) error {
	_, err := r.db.Pool.Exec(ctx, markNotificationReadQuery, id, userID)
	if err != nil {
		return toAppErr(err, "failed to mark notification read",
			slog.String("id", id),
			slog.String("user_id", userID),
		)
	}
	return nil
}

// MarkDismissed sets dismissed_at for the (userID, id) notification. The
// dismissed_at IS NULL guard makes a repeat call a no-op (idempotent).
func (r *NotificationRepository) MarkDismissed(ctx context.Context, userID, id string) error {
	_, err := r.db.Pool.Exec(ctx, markNotificationDismissedQuery, id, userID)
	if err != nil {
		return toAppErr(err, "failed to mark notification dismissed",
			slog.String("id", id),
			slog.String("user_id", userID),
		)
	}
	return nil
}

// Get retrieves a notification by id. Returns a NotFound application error when
// no row matches.
func (r *NotificationRepository) Get(ctx context.Context, id string) (*entity.Notification, error) {
	row := r.db.Pool.QueryRow(ctx, getNotificationQuery, id)
	n, err := scanNotification(row)
	if err != nil {
		return nil, toAppErr(err, "failed to get notification", slog.String("id", id))
	}
	return n, nil
}

// ListByUser returns a user's notifications most-recent-first, capped at limit.
func (r *NotificationRepository) ListByUser(ctx context.Context, userID string, limit int) ([]*entity.Notification, error) {
	if limit <= 0 {
		limit = defaultListByUserLimit
	}

	rows, err := r.db.Pool.Query(ctx, listNotificationsByUserQuery, userID, limit)
	if err != nil {
		return nil, toAppErr(err, "failed to list notifications by user",
			slog.String("user_id", userID),
		)
	}
	defer rows.Close()

	notifications := make([]*entity.Notification, 0)
	for rows.Next() {
		n, err := scanNotification(rows)
		if err != nil {
			return nil, toAppErr(err, "failed to scan notification row")
		}
		notifications = append(notifications, n)
	}
	if err := rows.Err(); err != nil {
		return nil, toAppErr(err, "error iterating notification rows")
	}
	return notifications, nil
}

// scanNotification scans a single row into a Notification entity, decoding the
// jsonb payload and the nullable delivery/read/dismiss columns.
func scanNotification(row scannable) (*entity.Notification, error) {
	var (
		id             string
		userID         string
		notifType      string
		payloadBytes   json.RawMessage
		deliveryStatus string
		failureReason  *string
		queuedAt       time.Time
		deliveredAt    *time.Time
		readAt         *time.Time
		dismissedAt    *time.Time
	)

	if err := row.Scan(
		&id, &userID, &notifType, &payloadBytes, &deliveryStatus,
		&failureReason, &queuedAt, &deliveredAt, &readAt, &dismissedAt,
	); err != nil {
		return nil, err
	}

	var payload *entity.NotificationPayload
	if len(payloadBytes) > 0 {
		payload = &entity.NotificationPayload{}
		if err := json.Unmarshal(payloadBytes, payload); err != nil {
			return nil, err
		}
	}

	n := &entity.Notification{
		ID:             id,
		UserID:         userID,
		Type:           entity.NotificationType(notifType),
		Payload:        payload,
		DeliveryStatus: entity.NotificationDeliveryStatus(deliveryStatus),
		QueueTime:      queuedAt,
		DeliverTime:    deliveredAt,
		ReadTime:       readAt,
		DismissTime:    dismissedAt,
	}
	if failureReason != nil {
		n.FailureReason = *failureReason
	}
	return n, nil
}
