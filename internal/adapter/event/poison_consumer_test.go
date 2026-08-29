package event_test

import (
	"bytes"
	"testing"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/ThreeDotsLabs/watermill/message/router/middleware"
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
		name      string
		msgUUID   string
		metadata  map[string]string
		wantInLog []string
	}{
		{
			name:    "emits ERROR log with uuid, topic, handler and reason",
			msgUUID: "dead-beef-1234",
			metadata: map[string]string{
				middleware.PoisonedTopicKey:     "USER.created",
				middleware.PoisonedHandlerKey:   "sync-artist-image",
				middleware.ReasonForPoisonedKey: "resolve artist images: deadline exceeded",
			},
			wantInLog: []string{
				"message routed to poison queue", "dead-beef-1234", "USER.created",
				"sync-artist-image", "deadline exceeded",
			},
		},
		{
			name:      "uses unknown topic when metadata is absent",
			msgUUID:   "no-topic-msg",
			metadata:  nil,
			wantInLog: []string{"message routed to poison queue", "no-topic-msg", "unknown"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			logger, buf := newTestLoggerWithBuf(t)
			handler := event.NewPoisonConsumer(logger)

			msg := message.NewMessage(tt.msgUUID, []byte("{}"))
			for k, v := range tt.metadata {
				msg.Metadata.Set(k, v)
			}

			err := handler.Handle(msg)

			assert.NoError(t, err)
			for _, want := range tt.wantInLog {
				assert.Contains(t, buf.String(), want)
			}
		})
	}
}
