package event_test

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/liverty-music/backend/internal/adapter/event"
	"github.com/liverty-music/backend/internal/infrastructure/messaging"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func makeArtistCreatedMsg(t *testing.T, data messaging.ArtistCreatedData) *message.Message {
	t.Helper()
	payload, err := json.Marshal(data)
	require.NoError(t, err)
	return message.NewMessage("test-id", payload)
}

func TestArtistNameConsumer_Handle(t *testing.T) {
	t.Run("delegates to use case", func(t *testing.T) {
		nameResolutionUC := ucmocks.NewMockArtistNameResolutionUseCase(t)
		handler := event.NewArtistNameConsumer(nameResolutionUC, newTestLogger(t))

		nameResolutionUC.EXPECT().ResolveCanonicalName(anyCtx, "artist-1", "mbid-001", "Original Name").Return(nil).Once()

		msg := makeArtistCreatedMsg(t, messaging.ArtistCreatedData{
			ArtistID:   "artist-1",
			ArtistName: "Original Name",
			MBID:       "mbid-001",
		})

		err := handler.Handle(msg)
		assert.NoError(t, err)
	})

	t.Run("returns error when use case fails", func(t *testing.T) {
		nameResolutionUC := ucmocks.NewMockArtistNameResolutionUseCase(t)
		handler := event.NewArtistNameConsumer(nameResolutionUC, newTestLogger(t))

		nameResolutionUC.EXPECT().ResolveCanonicalName(anyCtx, "artist-2", "mbid-fail", "Some Artist").Return(fmt.Errorf("rate limited")).Once()

		msg := makeArtistCreatedMsg(t, messaging.ArtistCreatedData{
			ArtistID:   "artist-2",
			ArtistName: "Some Artist",
			MBID:       "mbid-fail",
		})

		err := handler.Handle(msg)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "resolve canonical name")
	})

	t.Run("returns error on invalid payload", func(t *testing.T) {
		nameResolutionUC := ucmocks.NewMockArtistNameResolutionUseCase(t)
		handler := event.NewArtistNameConsumer(nameResolutionUC, newTestLogger(t))

		msg := message.NewMessage("bad-id", []byte("not json"))
		err := handler.Handle(msg)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "parse artist.created event")
	})
}
