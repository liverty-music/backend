package entity

import (
	"context"
	"time"
)

// Notification represents a notification sent to users about concerts.
type Notification struct {
	ID          string
	UserID      string
	ArtistID    string
	ConcertID   string
	Type        NotificationType
	Title       string
	Message     string
	Language    string
	Status      NotificationStatus
	ScheduledAt time.Time
	SentAt      *time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// NotificationType represents the type of notification.
type NotificationType string

// Notification type values.
const (
	// NotificationTypeConcertAnnounced indicates a new concert has been announced.
	NotificationTypeConcertAnnounced NotificationType = "concert_announced"
	NotificationTypeTicketsAvailable NotificationType = "tickets_available"
	NotificationTypeConcertReminder  NotificationType = "concert_reminder"
	NotificationTypeConcertCancelled NotificationType = "concert_cancelled"
)

// NotificationStatus represents the status of a notification.
type NotificationStatus string

// Notification status values.
const (
	// NotificationStatusPending indicates the notification has not been sent yet.
	NotificationStatusPending   NotificationStatus = "pending"
	NotificationStatusSent      NotificationStatus = "sent"
	NotificationStatusFailed    NotificationStatus = "failed"
	NotificationStatusCancelled NotificationStatus = "cancelled"
)

// NewNotification represents data for creating a new notification.
type NewNotification struct {
	UserID      string
	ArtistID    string
	ConcertID   string
	Type        NotificationType
	Title       string
	Message     string
	Language    string
	ScheduledAt time.Time
}

// NotificationRepository defines the interface for notification data access.
type NotificationRepository interface {
	Create(ctx context.Context, params *NewNotification) (*Notification, error)
	Get(ctx context.Context, id string) (*Notification, error)
	GetByUser(ctx context.Context, userID string, limit, offset int) ([]*Notification, error)
	GetPending(ctx context.Context, limit, offset int) ([]*Notification, error)
	UpdateStatus(ctx context.Context, id string, status NotificationStatus) error
	MarkAsSent(ctx context.Context, id string, sentAt time.Time) error
	Delete(ctx context.Context, id string) error
	List(ctx context.Context, limit, offset int) ([]*Notification, error)
}
