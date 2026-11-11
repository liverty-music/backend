// Package eventmsg is the contract between the code that delivers event
// messages (internal/infrastructure/messaging) and the code that handles them
// (internal/adapter/event): how a payload is encoded, which CloudEvents
// attributes a handler reads, and how a handler asks for a later delivery.
package eventmsg

import (
	"fmt"

	"github.com/ThreeDotsLabs/watermill/message"
	"google.golang.org/protobuf/proto"
)

// ContentTypeProtobuf is the ce_datacontenttype of an announcement whose
// payload is a binary protobuf message.
const ContentTypeProtobuf = "application/protobuf"

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
