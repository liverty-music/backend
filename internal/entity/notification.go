package entity

import (
	"context"
	"time"
)

// Notification represents a notification sent to users about concerts.
type Notification struct {
	// ID is the unique identifier for the notification.
	ID          string
	// UserID is the ID of the user receiving the notification.
	UserID      string
	// ArtistID is the ID of the artist related to the notification.
	ArtistID    string
	// ConcertID is the ID of the concert related to the notification.
	ConcertID   string
	// Type indicates the category of the notification.
	Type        NotificationType
	// Title is the brief summary of the notification.
	Title       string
	// Message is the detailed content of the notification.
	Message     string
	// Language is the language code for the notification content.
	Language    string
	// Status tracks the delivery state of the notification.
	Status      NotificationStatus
	// ScheduledAt is when the notification should be sent.
	ScheduledAt time.Time
	// SentAt is when the notification was actually sent.
	SentAt      *time.Time
	// CreateTime is the timestamp when the notification was created.
	CreateTime  time.Time
	// UpdateTime is the timestamp when the notification was last updated.
	UpdateTime  time.Time
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
	// UserID is the target user's ID.
	UserID      string
	// ArtistID is the related artist's ID.
	ArtistID    string
	// ConcertID is the related concert's ID.
	ConcertID   string
	// Type indicates the category of the notification.
	Type        NotificationType
	// Title is the brief summary.
	Title       string
	// Message is the detailed content.
	Message     string
	// Language is the language code.
	Language    string
	// ScheduledAt is when to send the notification.
	ScheduledAt time.Time
}

// NotificationRepository defines the interface for notification data access.
type NotificationRepository interface {
	// Create creates a new notification.
	Create(ctx context.Context, params *NewNotification) (*Notification, error)
	// Get retrieves a notification by ID.
	Get(ctx context.Context, id string) (*Notification, error)
	// GetByUser retrieves notifications for a specific user with pagination.
	GetByUser(ctx context.Context, userID string, limit, offset int) ([]*Notification, error)
	// GetPending retrieves notifications that are scheduled but not yet sent.
	GetPending(ctx context.Context, limit, offset int) ([]*Notification, error)
	// UpdateStatus updates the status of a notification.
	UpdateStatus(ctx context.Context, id string, status NotificationStatus) error
	// MarkAsSent marks a notification as sent and records the timestamp.
	MarkAsSent(ctx context.Context, id string, sentAt time.Time) error
	// Delete removes a notification.
	Delete(ctx context.Context, id string) error
	// List returns all notifications with pagination.
	List(ctx context.Context, limit, offset int) ([]*Notification, error)
}
