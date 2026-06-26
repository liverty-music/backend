package entity

import (
	"context"
)

// NotificationPayload is the JSON structure delivered as the Web Push message body.
type NotificationPayload struct {
	// Title is the notification title, typically the artist name.
	Title string `json:"title"`
	// Body is the human-readable notification text.
	Body string `json:"body"`
	// URL is the deep-link target opened when the user taps the notification.
	URL string `json:"url"`
	// Tag deduplicates notifications on the browser side; one active notification per tag.
	Tag string `json:"tag"`
	// Data carries key/value metadata delivered alongside the notification. The
	// service worker reads it (e.g. data.notification_id) to correlate user
	// interactions back to the stored notification record. Omitted when empty.
	Data map[string]string `json:"data,omitempty"`
}

// NotificationDataKeyNotificationID is the [NotificationPayload.Data] key under
// which the stored notification record's id is propagated to the client/service
// worker, establishing the end-to-end correlation key.
const NotificationDataKeyNotificationID = "notification_id"

// PushNotificationSender sends Web Push notifications to browser endpoints.
// Implementations handle VAPID signing, encryption, and HTTP delivery.
type PushNotificationSender interface {
	// Send delivers the payload to the given push subscription endpoint.
	//
	// # Possible errors
	//
	//   - NotFound: the subscription endpoint is no longer valid (e.g., HTTP 410 Gone).
	//   - Internal: delivery failure (network error, encryption failure, etc.).
	Send(ctx context.Context, payload []byte, sub *PushSubscription) error
}
