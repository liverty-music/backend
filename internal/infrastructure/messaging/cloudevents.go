package messaging

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/liverty-music/backend/internal/entity"
)

const (
	// CloudEvents spec version.
	specVersion = "1.0"

	// CloudEvents source for all events emitted by this service.
	source = "liverty-music/backend"
)

// NewEvent creates a Watermill message with structured metadata, minting a
// fresh random id for it. The caller's trace context is attached to the
// message so that downstream consumers can continue the same distributed
// trace. The data payload is JSON-encoded into the message body.
func NewEvent(ctx context.Context, data any) (*message.Message, error) {
	return NewEventWithID(ctx, entity.NewID(), data)
}

// NewEventWithID is [NewEvent] with a caller-supplied id instead of a freshly
// minted one. The id becomes both the CloudEvent id and the Watermill message
// UUID; on NATS, the message UUID is what TrackMsgId tracks as the Nats-Msg-Id
// header for broker-side de-duplication (see publisher.go), so passing the
// same id again within the target stream's Duplicates window is deduplicated
// rather than delivered twice.
func NewEventWithID(ctx context.Context, id string, data any) (*message.Message, error) {
	payload, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("marshal event data: %w", err)
	}

	msg := message.NewMessage(id, payload)
	msg.SetContext(ctx)

	msg.Metadata.Set("ce_specversion", specVersion)
	msg.Metadata.Set("ce_source", source)
	msg.Metadata.Set("ce_id", id)
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
