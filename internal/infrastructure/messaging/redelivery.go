package messaging

import (
	"errors"
	"fmt"
	"time"

	"github.com/ThreeDotsLabs/watermill/message"
)

// redeliveryDelayKey is the watermill metadata key carrying how long the
// broker waits before delivering a message again (a time.Duration string).
const redeliveryDelayKey = "x-liverty-redelivery-delay"

// redeliveryError marks a handler failure that a later delivery may succeed
// on (e.g. a record another event creates has not been stored yet).
type redeliveryError struct {
	err   error
	delay time.Duration
}

func (e *redeliveryError) Error() string {
	return fmt.Sprintf("redeliver after %s: %v", e.delay, e.err)
}

func (e *redeliveryError) Unwrap() error { return e.err }

// RedeliverLater marks err as a failure to try again in delay, not on the
// spot: the router neither retries it in-process nor moves the message to the
// poison queue, but nacks it, and the JetStream subscriber asks the broker to
// deliver it again after delay. The handler decides when to stop (it returns
// nil to finish). The local in-memory channel redelivers at once.
func RedeliverLater(msg *message.Message, err error, delay time.Duration) error {
	msg.Metadata.Set(redeliveryDelayKey, delay.String())
	return &redeliveryError{err: err, delay: delay}
}

// IsRedeliverLater reports whether err was returned through [RedeliverLater].
func IsRedeliverLater(err error) bool {
	var r *redeliveryError
	return errors.As(err, &r)
}

// redeliveryDelay returns the delay a handler asked for with RedeliverLater,
// or 0 when it asked for none.
func redeliveryDelay(msg *message.Message) time.Duration {
	d, err := time.ParseDuration(msg.Metadata.Get(redeliveryDelayKey))
	if err != nil || d < 0 {
		return 0
	}
	return d
}
