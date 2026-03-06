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

	// SubjectConcertDiscovered is the NATS subject for newly discovered concert data.
	SubjectConcertDiscovered = "CONCERT.discovered"
	// SubjectConcertCreated is the NATS subject for persisted concert entities.
	SubjectConcertCreated = "CONCERT.created"
	// SubjectVenueCreated is the NATS subject for newly created venue entities that need enrichment.
	SubjectVenueCreated = "VENUE.created"
)

// NewEvent creates a Watermill message with structured metadata.
// The data payload is JSON-encoded into the message body.
func NewEvent(data any) (*message.Message, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("generate event ID: %w", err)
	}

	payload, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("marshal event data: %w", err)
	}

	msg := message.NewMessage(id.String(), payload)

	msg.Metadata.Set("ce_specversion", specVersion)
	msg.Metadata.Set("ce_source", source)
	msg.Metadata.Set("ce_id", id.String())
	msg.Metadata.Set("ce_time", time.Now().UTC().Format(time.RFC3339))
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
