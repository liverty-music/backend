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

func TestNewPublisher_GoChannelFallback(t *testing.T) {
	t.Parallel()

	cfg := config.NATSConfig{URL: ""}
	logger := watermill.NopLogger{}
	ch := gochannel.NewGoChannel(gochannel.Config{}, logger)

	pub, err := messaging.NewPublisher(cfg, logger, ch)

	require.NoError(t, err)
	assert.NotNil(t, pub)
}

func TestNewPublisher_NilGoChannelWithEmptyURLReturnsError(t *testing.T) {
	t.Parallel()

	cfg := config.NATSConfig{URL: ""}
	logger := watermill.NopLogger{}

	pub, err := messaging.NewPublisher(cfg, logger, nil)

	require.Error(t, err)
	assert.Nil(t, pub)
	assert.Contains(t, err.Error(), "GoChannel is required")
}
