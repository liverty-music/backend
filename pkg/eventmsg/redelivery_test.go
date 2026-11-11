package eventmsg_test

import (
	"errors"
	"testing"
	"time"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/liverty-music/backend/pkg/eventmsg"
	"github.com/stretchr/testify/assert"
)

func TestRedeliverLater(t *testing.T) {
	t.Parallel()

	t.Run("marks the error and the message's redelivery delay", func(t *testing.T) {
		msg := message.NewMessage("m", nil)
		cause := errors.New("not yet")

		err := eventmsg.RedeliverLater(msg, cause, 15*time.Second)

		assert.True(t, eventmsg.IsRedeliverLater(err))
		assert.ErrorIs(t, err, cause)
		assert.Equal(t, 15*time.Second, eventmsg.RedeliveryDelay(msg))
		assert.False(t, eventmsg.IsRedeliverLater(cause))
		assert.Zero(t, eventmsg.RedeliveryDelay(message.NewMessage("other", nil)))
	})
}
