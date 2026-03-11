package entity

import "context"

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
