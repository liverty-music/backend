package event_test

import (
	"bytes"
	"testing"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/liverty-music/backend/internal/adapter/event"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestLoggerWithBuf(t *testing.T) (*logging.Logger, *bytes.Buffer) {
	t.Helper()
	buf := &bytes.Buffer{}
	logger, err := logging.New(logging.WithWriter(buf))
	require.NoError(t, err)
	return logger, buf
}

func TestPoisonConsumer_Handle(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		msgUUID       string
		topicMetadata string
		wantInLog     []string
	}{
		{
			name:          "emits ERROR log with uuid and topic",
			msgUUID:       "dead-beef-1234",
			topicMetadata: "USER.created",
			wantInLog:     []string{"message routed to poison queue", "dead-beef-1234", "USER.created"},
		},
		{
			name:          "uses unknown topic when metadata is absent",
			msgUUID:       "no-topic-msg",
			topicMetadata: "",
			wantInLog:     []string{"message routed to poison queue", "no-topic-msg", "unknown"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			logger, buf := newTestLoggerWithBuf(t)
			handler := event.NewPoisonConsumer(logger)

			msg := message.NewMessage(tt.msgUUID, []byte("{}"))
			if tt.topicMetadata != "" {
				msg.Metadata.Set("topic", tt.topicMetadata)
			}

			err := handler.Handle(msg)

			assert.NoError(t, err)
			for _, want := range tt.wantInLog {
				assert.Contains(t, buf.String(), want)
			}
		})
	}
}
