package messaging

import (
	"context"
	"fmt"

	"github.com/ThreeDotsLabs/watermill/message"
)

// EventPublisherImpl implements usecase.EventPublisher using Watermill.
type EventPublisherImpl struct {
	publisher message.Publisher
}

// NewEventPublisher wraps a Watermill publisher with CloudEvent serialization.
func NewEventPublisher(publisher message.Publisher) *EventPublisherImpl {
	return &EventPublisherImpl{publisher: publisher}
}

// PublishEvent serializes data as a CloudEvent and publishes it to subject.
func (p *EventPublisherImpl) PublishEvent(ctx context.Context, subject string, data any) error {
	msg, err := NewEvent(ctx, data)
	if err != nil {
		return fmt.Errorf("create event: %w", err)
	}
	return p.publisher.Publish(subject, msg)
}
