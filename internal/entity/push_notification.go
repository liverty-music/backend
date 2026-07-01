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
	// Tag deduplicates notifications on the browser side; one active notification per tag.
	Tag string `json:"tag"`
	// Data carries the client-passthrough metadata delivered alongside the
	// notification. The service worker maps it straight into showNotification's
	// options.data, so notificationclick/close read it uniformly — the deep-link
	// target (data.url) and the correlation key (data.notification_id). Omitted
	// when empty.
	Data map[string]string `json:"data,omitempty"`
}

// NotificationDataKeyNotificationID is the [NotificationPayload.Data] key under
// which the stored notification record's id is propagated to the client/service
// worker, establishing the end-to-end correlation key.
const NotificationDataKeyNotificationID = "notification_id"

// NotificationDataKeyURL is the [NotificationPayload.Data] key under which the
// deep-link target opened on notificationclick is propagated to the client.
// Consolidated with notification_id under Data so the service worker maps the
// whole payload.data into showNotification's options.data in one step.
const NotificationDataKeyURL = "url"

// NewNotificationPayload builds a payload with the deep-link url placed under
// the client-passthrough Data map (data.url), where the service worker reads it
// on notificationclick. Title, body, and tag stay top-level as native Web Push
// notification fields.
func NewNotificationPayload(title, body, url, tag string) *NotificationPayload {
	return &NotificationPayload{
		Title: title,
		Body:  body,
		Tag:   tag,
		Data:  map[string]string{NotificationDataKeyURL: url},
	}
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
