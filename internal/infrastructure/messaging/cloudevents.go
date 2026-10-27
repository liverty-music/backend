package messaging

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/google/uuid"
)

const (
	// CloudEvents spec version.
	specVersion = "1.0"

	// CloudEvents source for all events emitted by this service.
	source = "liverty-music/backend"
)

// eventIDNamespace is the UUIDv5 namespace of every CloudEvent id. It never
// changes: a step that publishes again after a restart must get the id it got
// the first time.
var eventIDNamespace = uuid.MustParse("faecddd1-297c-47bb-9be5-a0f66d97d444")

// publishOrigin names the publishes of one workflow step run. next counts the
// publishes of the run, so two publishes in one step get two ids and the same
// publish gets the same id when the step runs again.
type publishOrigin struct {
	name string
	next atomic.Int64
}

type publishOriginKey struct{}

// WithPublishOrigin returns a context whose publishes take their event ids
// from name, "<workflow id>/<step id>" of a workflow step. A step that runs
// again after a stop publishes with the same ids, so JetStream and product
// analytics see one event (OpenSpec change introduce-durable-workflows, design
// "One id per announcement"). The workflow runtime sets it on every step.
func WithPublishOrigin(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, publishOriginKey{}, &publishOrigin{name: name})
}

// eventID returns the CloudEvent id of the next publish under ctx: a UUIDv5
// named after the step and the publish's position in it inside a workflow step,
// and named after a fresh random value anywhere else, so each such publish gets
// its own id.
func eventID(ctx context.Context) string {
	name := uuid.NewString()
	if origin, ok := ctx.Value(publishOriginKey{}).(*publishOrigin); ok {
		name = fmt.Sprintf("%s/%d", origin.name, origin.next.Add(1)-1)
	}
	return uuid.NewSHA1(eventIDNamespace, []byte(name)).String()
}

// NewEvent creates a Watermill message with structured metadata, using one id
// as both the CloudEvent id and the Watermill message UUID (see eventID). The
// caller's trace context is attached to the message so that downstream
// consumers can continue the same distributed trace. The data payload is
// JSON-encoded into the message body.
func NewEvent(ctx context.Context, data any) (*message.Message, error) {
	id := eventID(ctx)
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
