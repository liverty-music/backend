package messaging_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/ThreeDotsLabs/watermill/pubsub/gochannel"
	"github.com/liverty-music/backend/internal/infrastructure/messaging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordingPublisher records the topics it is asked to publish to.
type recordingPublisher struct {
	mu     sync.Mutex
	topics []string
}

func (p *recordingPublisher) Publish(topic string, _ ...*message.Message) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.topics = append(p.topics, topic)
	return nil
}

func (p *recordingPublisher) Close() error { return nil }

func (p *recordingPublisher) published() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.topics...)
}

func TestRedeliverLater(t *testing.T) {
	t.Parallel()

	t.Run("marks the error and the message's redelivery delay", func(t *testing.T) {
		t.Parallel()
		msg := message.NewMessage("m", nil)
		cause := errors.New("not yet")

		err := messaging.RedeliverLater(msg, cause, 15*time.Second)

		assert.True(t, messaging.IsRedeliverLater(err))
		assert.ErrorIs(t, err, cause)
		assert.Equal(t, 15*time.Second, messaging.RedeliveryDelay(msg))
		assert.False(t, messaging.IsRedeliverLater(cause))
		assert.Zero(t, messaging.RedeliveryDelay(message.NewMessage("other", nil)))
	})

	t.Run("the router nacks it without in-process retries or the poison queue", func(t *testing.T) {
		t.Parallel()
		logger := watermill.NopLogger{}
		pubsub := gochannel.NewGoChannel(gochannel.Config{OutputChannelBuffer: 8}, logger)
		poison := &recordingPublisher{}
		router, err := messaging.NewRouter(logger, poison, "POISON.queue")
		require.NoError(t, err)

		var calls atomic.Int32
		done := make(chan struct{})
		router.AddConsumerHandler("redeliver-later", "TEST.subject", pubsub, func(msg *message.Message) error {
			if calls.Add(1) == 1 {
				return messaging.RedeliverLater(msg, errors.New("not yet"), time.Second)
			}
			close(done)
			return nil
		})
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(func() {
			cancel()
			_ = router.Close()
		})
		go func() { _ = router.Run(ctx) }()
		<-router.Running()

		require.NoError(t, pubsub.Publish("TEST.subject", message.NewMessage("m", []byte("{}"))))

		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("the message was not delivered again")
		}
		// First delivery failed once with no in-process retry, the redelivery
		// succeeded, and nothing went to the poison queue.
		assert.Equal(t, int32(2), calls.Load())
		assert.Empty(t, poison.published())
	})
}
