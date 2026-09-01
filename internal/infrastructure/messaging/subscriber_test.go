package messaging_test

import (
	"context"
	"testing"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/liverty-music/backend/internal/infrastructure/messaging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewBehaviorSubscriber_Construction verifies that NewBehaviorSubscriber
// succeeds without a live NATS connection (construction is cheap — the actual
// bind happens only when the watermill router calls Subscribe).
func TestNewBehaviorSubscriber_Construction(t *testing.T) {
	t.Parallel()

	// NewBehaviorSubscriber only stores the conn pointer; it does not dial
	// NATS at construction time, so passing nil is safe here.
	sub, err := messaging.NewBehaviorSubscriber(nil, "test-behavior", watermill.NopLogger{}, messaging.NewConsumerHealth())

	require.NoError(t, err)
	assert.NotNil(t, sub)
}

// TestNewBehaviorSubscriber_CloseBeforeSubscribe verifies that Close is safe
// to call before Subscribe (no-op, no panic).
func TestNewBehaviorSubscriber_CloseBeforeSubscribe(t *testing.T) {
	t.Parallel()

	sub, err := messaging.NewBehaviorSubscriber(nil, "test-behavior", watermill.NopLogger{}, messaging.NewConsumerHealth())
	require.NoError(t, err)

	assert.NoError(t, sub.Close())
}

// TestNewBehaviorSubscriber_SubscribeRejectsUncoveredSubject verifies that
// Subscribe returns an error (rather than panicking or silently failing) when
// the topic has no matching JetStream stream in the registry. This is the
// fail-fast behaviour that catches the "added a new subject without its
// stream" class of bug at startup instead of at runtime.
//
// We pass a nil conn intentionally: Subscribe must fail on the stream-lookup
// before it ever touches the conn, so no NATS connection is needed.
func TestNewBehaviorSubscriber_SubscribeRejectsUncoveredSubject(t *testing.T) {
	t.Parallel()

	sub, err := messaging.NewBehaviorSubscriber(nil, "test-behavior", watermill.NopLogger{}, messaging.NewConsumerHealth())
	require.NoError(t, err)

	_, subscribeErr := sub.Subscribe(context.Background(), "UNKNOWN.subject")

	require.Error(t, subscribeErr)
	assert.Contains(t, subscribeErr.Error(), "no JetStream stream covers subject")
}
