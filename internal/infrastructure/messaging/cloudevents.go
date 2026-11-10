package messaging

import (
	"fmt"

	"github.com/ThreeDotsLabs/watermill/message"
	"google.golang.org/protobuf/proto"
)

const (
	// CloudEvents spec version.
	specVersion = "1.0"

	// CloudEvents source for all events emitted by this service.
	source = "liverty-music/backend"
)

// ParseProtobufData unmarshals the binary protobuf payload of msg into
// target.
func ParseProtobufData(msg *message.Message, target proto.Message) error {
	if err := proto.Unmarshal(msg.Payload, target); err != nil {
		return fmt.Errorf("unmarshal protobuf event data: %w", err)
	}
	return nil
}

// CloudEventType returns the ce_type attribute of msg: the subject the
// announcement was recorded under (outbox), so a receiver of several subjects
// of one stream tells the event from it.
func CloudEventType(msg *message.Message) string {
	return msg.Metadata.Get("ce_type")
}

// CloudEventSubject returns the ce_subject attribute of msg: the entity the
// message is about when the payload does not name it, or empty.
func CloudEventSubject(msg *message.Message) string {
	return msg.Metadata.Get("ce_subject")
}
