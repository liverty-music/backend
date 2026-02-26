package messaging

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/google/uuid"
)

const (
	// CloudEvents spec version.
	specVersion = "1.0"

	// CloudEvents source for all events emitted by this service.
	source = "liverty-music/backend"

	// EventTypeConcertDiscovered is emitted when new concert data is discovered from an external source.
	EventTypeConcertDiscovered = "liverty-music.concert.discovered.v1"
	// EventTypeConcertCreated is emitted when a concert entity is persisted to the database.
	EventTypeConcertCreated = "liverty-music.concert.created.v1"
	// EventTypeVenueCreated is emitted when a new venue entity is created and needs enrichment.
	EventTypeVenueCreated = "liverty-music.venue.created.v1"
)

// NewCloudEvent creates a Watermill message with CloudEvents v1.0 metadata.
// The data payload is JSON-encoded into the message body.
func NewCloudEvent(eventType string, data any) (*message.Message, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("generate event ID: %w", err)
	}

	payload, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("marshal event data: %w", err)
	}

	msg := message.NewMessage(id.String(), payload)

	// CloudEvents required attributes
	msg.Metadata.Set("ce_specversion", specVersion)
	msg.Metadata.Set("ce_type", eventType)
	msg.Metadata.Set("ce_source", source)
	msg.Metadata.Set("ce_id", id.String())
	msg.Metadata.Set("ce_time", time.Now().UTC().Format(time.RFC3339))

	// CloudEvents optional attributes
	msg.Metadata.Set("ce_datacontenttype", "application/json")

	return msg, nil
}

// ParseCloudEventData extracts and unmarshals the JSON data from a Watermill message
// into the provided target struct.
func ParseCloudEventData(msg *message.Message, target any) error {
	if err := json.Unmarshal(msg.Payload, target); err != nil {
		return fmt.Errorf("unmarshal event data: %w", err)
	}
	return nil
}
