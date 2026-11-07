package rdb

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"

	"github.com/liverty-music/backend/internal/entity"
)

// NotificationRepository implements entity.NotificationRepository for PostgreSQL.
type NotificationRepository struct {
	db *Database
}

// Compile-time interface compliance check.
var _ entity.NotificationRepository = (*NotificationRepository)(nil)

const (
	createNotificationQuery = `
		INSERT INTO notifications (id, user_id, type, subject)
		VALUES ($1, $2, $3, $4)
		RETURNING created_at
	`
	markNotificationReadQuery = `
		UPDATE notifications
		SET read_at = NOW()
		WHERE id = $1 AND user_id = $2 AND read_at IS NULL
	`
	getNotificationQuery = `
		SELECT id, user_id, type, subject, created_at, read_at
		FROM notifications
		WHERE id = $1
	`
	listNotificationsByUserQuery = `
		SELECT id, user_id, type, subject, created_at, read_at
		FROM notifications
		WHERE user_id = $1
		ORDER BY created_at DESC, id DESC
		LIMIT $2
	`
)

// NewNotificationRepository creates a new notification repository instance.
func NewNotificationRepository(db *Database) *NotificationRepository {
	return &NotificationRepository{db: db}
}

// Create stores n unread, minting a UUIDv7 id and writing back the
// server-assigned create time.
func (r *NotificationRepository) Create(ctx context.Context, n *entity.Notification) error {
	if err := n.Subject.Validate(n.Type); err != nil {
		return err
	}
	subject, err := json.Marshal(n.Subject)
	if err != nil {
		return toAppErr(err, "failed to marshal notification subject",
			slog.String("user_id", n.UserID),
		)
	}
	n.ID = entity.NewID()
	err = r.db.Pool.QueryRow(ctx, createNotificationQuery,
		n.ID, n.UserID, string(n.Type), subject,
	).Scan(&n.CreateTime)
	if err != nil {
		return toAppErr(err, "failed to create notification",
			slog.String("user_id", n.UserID),
			slog.String("type", string(n.Type)),
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

// ListByUser returns a user's notifications newest first, at most limit.
func (r *NotificationRepository) ListByUser(ctx context.Context, userID string, limit int) ([]*entity.Notification, error) {
	if limit < 1 {
		return nil, apperr.New(codes.InvalidArgument, "limit must be at least 1", slog.Int("limit", limit))
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

// scannable is satisfied by both pgx.Row (single-row query) and pgx.Rows
// (multi-row cursor), allowing scan helpers to accept either without
// duplicating code.
type scannable interface {
	Scan(dest ...any) error
}

// scanNotification scans a single row into a Notification entity, decoding the
// jsonb subject and the nullable read time.
func scanNotification(row scannable) (*entity.Notification, error) {
	var (
		n         entity.Notification
		notifType string
		subject   json.RawMessage
		createdAt time.Time
		readAt    *time.Time
	)
	if err := row.Scan(&n.ID, &n.UserID, &notifType, &subject, &createdAt, &readAt); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(subject, &n.Subject); err != nil {
		return nil, err
	}
	n.Type = entity.NotificationType(notifType)
	n.CreateTime = createdAt
	n.ReadTime = readAt
	return &n, nil
}
