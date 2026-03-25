package usecase

import "context"

// EventPublisher publishes domain events to the messaging infrastructure.
// It abstracts event serialization and delivery behind a single method,
// keeping the usecase layer free of infrastructure dependencies.
type EventPublisher interface {
	// PublishEvent serializes data as a CloudEvent and publishes it to subject.
	// Returns an error if serialization or delivery fails.
	PublishEvent(ctx context.Context, subject string, data any) error
}
