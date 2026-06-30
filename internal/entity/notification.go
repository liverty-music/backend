package entity

import (
	"context"
	"time"
)

// NotificationType identifies the kind of user-facing notification, mirroring
// the producer that raised it. It is persisted verbatim in notifications.type.
type NotificationType string

const (
	// NotificationTypeNewConcerts is a new-concert alert for a followed artist.
	NotificationTypeNewConcerts NotificationType = "new_concerts"
	// NotificationTypeSalesReminder is a ticket sales-phase reminder for a tracked event.
	NotificationTypeSalesReminder NotificationType = "sales_reminder"
	// NotificationTypeSalesPhaseAnnouncement announces a newly discovered sales phase.
	NotificationTypeSalesPhaseAnnouncement NotificationType = "sales_phase_announcement"
)

// NotificationDeliveryStatus is the per-channel delivery state of a notification.
// Web push provides no separate sent-vs-delivered receipt, so Delivered denotes
// acceptance by the push service rather than confirmed device receipt.
type NotificationDeliveryStatus string

const (
	// NotificationDeliveryStatusQueued is the initial state: the record exists but
	// the channel send has not yet been attempted (or its outcome is unknown).
	NotificationDeliveryStatusQueued NotificationDeliveryStatus = "queued"
	// NotificationDeliveryStatusDelivered means at least one channel send was accepted.
	NotificationDeliveryStatusDelivered NotificationDeliveryStatus = "delivered"
	// NotificationDeliveryStatusFailed means every channel send failed (or there was
	// no channel to deliver to); the record remains for audit and re-dispatch.
	NotificationDeliveryStatusFailed NotificationDeliveryStatus = "failed"
)

// Notification is a durable record of a single user-facing notification: the
// source of truth for delivery auditing and the in-app inbox. It is created in
// the Queued state before the channel send and then transitioned to Delivered
// or Failed with the outcome in hand.
type Notification struct {
	// ID is the unique notification identifier (UUIDv7, application-generated).
	// It is propagated into the dispatched push payload's data.notification_id
	// as the end-to-end correlation key.
	ID string
	// UserID is the recipient user.
	UserID string
	// Type is the notification kind.
	Type NotificationType
	// Payload is the rendered notification content delivered to the channel.
	Payload *NotificationPayload
	// DeliveryStatus is the current per-channel delivery state.
	DeliveryStatus NotificationDeliveryStatus
	// FailureReason is set when DeliveryStatus is Failed; empty otherwise.
	FailureReason string
	// QueueTime is when the record was created, in the queued state.
	QueueTime time.Time
	// DeliverTime is when the channel accepted the send; nil until delivered.
	DeliverTime *time.Time
	// ReadTime is when the user marked the notification read; nil until read.
	ReadTime *time.Time
	// DismissTime is when the user dismissed the notification; nil until dismissed.
	DismissTime *time.Time
}

// NotificationRepository defines persistence operations for the notification log.
//
// Mutations that act on a single user's notification (MarkRead, MarkDismissed)
// are user-scoped so one user cannot alter another user's record.
type NotificationRepository interface {
	// Create persists a new notification record in the Queued state. The ID is
	// minted (UUIDv7) when empty and written back onto n.
	Create(ctx context.Context, n *Notification) error

	// UpdateDelivery records the terminal delivery outcome of a notification's
	// channel send. On Delivered, deliveredAt is set; on Failed, failureReason
	// is recorded. The record is never deleted, so a failure stays auditable and
	// the send is re-dispatchable.
	UpdateDelivery(ctx context.Context, id string, status NotificationDeliveryStatus, deliveredAt *time.Time, failureReason string) error

	// MarkRead sets read_at for the (userID, id) notification. It is idempotent:
	// marking an already-read notification is a no-op success.
	MarkRead(ctx context.Context, userID, id string) error

	// MarkDismissed sets dismissed_at for the (userID, id) notification. It is
	// idempotent: dismissing an already-dismissed notification is a no-op success.
	MarkDismissed(ctx context.Context, userID, id string) error

	// Get retrieves the notification identified by id.
	//
	// # Possible errors
	//
	//   - NotFound: no notification with that id exists.
	Get(ctx context.Context, id string) (*Notification, error)

	// ListByUser returns a user's notifications most-recent-first, capped at limit
	// (a non-positive limit applies a sensible default). Returns an empty slice
	// when the user has no notifications.
	ListByUser(ctx context.Context, userID string, limit int) ([]*Notification, error)
}
