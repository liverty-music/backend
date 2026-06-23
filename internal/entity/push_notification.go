package entity

import (
	"context"
	"fmt"
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

// NewConcertNotificationPayload constructs the push notification payload for newly
// discovered concerts for the given artist. The body is localized to lang; an empty
// or unsupported lang falls back to English. Title, URL, and Tag are language-independent.
func NewConcertNotificationPayload(artist *Artist, concertCount int, lang string) *NotificationPayload {
	return &NotificationPayload{
		Title: artist.Name,
		Body:  concertNotificationBody(concertCount, lang),
		URL:   fmt.Sprintf("/concerts?artist=%s", artist.ID),
		Tag:   fmt.Sprintf("concert-%s", artist.ID),
	}
}

// concertNotificationBody renders the new-concert count in the given language,
// falling back to English for empty or unsupported codes.
func concertNotificationBody(concertCount int, lang string) string {
	switch lang {
	case "ja":
		return fmt.Sprintf("新しいライブが%d件見つかりました", concertCount)
	default:
		if concertCount == 1 {
			return "1 new concert found"
		}
		return fmt.Sprintf("%d new concerts found", concertCount)
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
