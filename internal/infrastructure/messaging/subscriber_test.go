package messaging_test

import (
	"testing"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/pubsub/gochannel"
	"github.com/liverty-music/backend/internal/infrastructure/messaging"
	"github.com/liverty-music/backend/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewSubscriber_GoChannelFallback(t *testing.T) {
	t.Parallel()

	cfg := config.NATSConfig{URL: ""}
	logger := watermill.NopLogger{}
	ch := gochannel.NewGoChannel(gochannel.Config{}, logger)

	sub, err := messaging.NewSubscriber(cfg, logger, ch)

	require.NoError(t, err)
	assert.NotNil(t, sub)
	// The returned subscriber should be the GoChannel itself.
	assert.Equal(t, ch, sub)
}

func TestNewSubscriber_NilGoChannelWithEmptyURLReturnsError(t *testing.T) {
	t.Parallel()

	cfg := config.NATSConfig{URL: ""}
	logger := watermill.NopLogger{}

	sub, err := messaging.NewSubscriber(cfg, logger, nil)

	require.Error(t, err)
	assert.Nil(t, sub)
	assert.Contains(t, err.Error(), "GoChannel is required")
}
