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
}

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
