package event

import (
	"log/slog"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/pannpers/go-logging/logging"
)

// PoisonConsumer logs every message that has been routed to the Poison Queue
// after exhausting all Watermill retries. It emits an ERROR log so that the
// existing workload alert policies detect the failure. Messages are always
// acked — they are not re-processed.
type PoisonConsumer struct {
	logger *logging.Logger
}

// NewPoisonConsumer creates a new PoisonConsumer.
func NewPoisonConsumer(logger *logging.Logger) *PoisonConsumer {
	return &PoisonConsumer{logger: logger}
}

// Handle logs an ERROR for the poisoned message and returns nil to ack it.
func (h *PoisonConsumer) Handle(msg *message.Message) error {
	ctx := msg.Context()

	topic := msg.Metadata.Get("topic")
	if topic == "" {
		topic = "unknown"
	}

	h.logger.Error(ctx, "message routed to poison queue", nil,
		slog.String("uuid", msg.UUID),
		slog.String("topic", topic),
	)

	return nil
}
