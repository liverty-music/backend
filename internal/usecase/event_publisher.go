package usecase

import "context"

// EventPublisher publishes domain events to the messaging infrastructure.
// It abstracts event serialization and delivery behind a single method,
// keeping the usecase layer free of infrastructure dependencies.
type EventPublisher interface {
	// PublishEvent serializes data as a CloudEvent and publishes it to subject.
	// Returns an error if serialization or delivery fails.
	PublishEvent(ctx context.Context, subject string, data any) error

	// PublishEventWithID serializes data as a CloudEvent and publishes it to
	// subject, using id as both the CloudEvent id and the message's stable
	// identifier. On NATS, the messaging infrastructure tracks that identifier
	// as the Nats-Msg-Id header (see infrastructure/messaging.Publisher's
	// TrackMsgId option), so publishing the same id again within the target
	// stream's Duplicates window is deduplicated broker-side rather than
	// delivered twice. Callers that need at-least-once-retry-safe fan-out —
	// e.g. one publish per notification recipient, where the same triggering
	// event may be redelivered — derive id deterministically from stable
	// business keys (recipient + notification type + correlation key) so a
	// retry re-publishes the identical id instead of a fresh random one.
	//
	// Returns an error if serialization or delivery fails.
	PublishEventWithID(ctx context.Context, subject, id string, data any) error
}
