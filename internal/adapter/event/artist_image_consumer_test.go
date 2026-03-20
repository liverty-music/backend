package event_test

import (
	"fmt"
	"testing"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/liverty-music/backend/internal/adapter/event"
	"github.com/liverty-music/backend/internal/entity"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/stretchr/testify/assert"
)

func TestArtistImageConsumer_Handle(t *testing.T) {
	t.Parallel()

	t.Run("delegates to use case", func(t *testing.T) {
		t.Parallel()

		imageSyncUC := ucmocks.NewMockArtistImageSyncUseCase(t)
		handler := event.NewArtistImageConsumer(imageSyncUC, newTestLogger(t))

		imageSyncUC.EXPECT().SyncArtistImage(anyCtx, "artist-1", "mbid-001").Return(nil).Once()

		msg := makeArtistCreatedMsg(t, entity.ArtistCreatedData{
			ArtistID:   "artist-1",
			ArtistName: "Test Artist",
			MBID:       "mbid-001",
		})

		err := handler.Handle(msg)
		assert.NoError(t, err)
	})

	t.Run("returns error when use case fails", func(t *testing.T) {
		t.Parallel()

		imageSyncUC := ucmocks.NewMockArtistImageSyncUseCase(t)
		handler := event.NewArtistImageConsumer(imageSyncUC, newTestLogger(t))

		imageSyncUC.EXPECT().SyncArtistImage(anyCtx, "artist-2", "mbid-fail").Return(fmt.Errorf("fanarttv unavailable")).Once()

		msg := makeArtistCreatedMsg(t, entity.ArtistCreatedData{
			ArtistID:   "artist-2",
			ArtistName: "Failing Artist",
			MBID:       "mbid-fail",
		})

		err := handler.Handle(msg)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "handle ARTIST.created event for image sync")
	})

	t.Run("returns error on invalid payload", func(t *testing.T) {
		t.Parallel()

		imageSyncUC := ucmocks.NewMockArtistImageSyncUseCase(t)
		handler := event.NewArtistImageConsumer(imageSyncUC, newTestLogger(t))

		msg := message.NewMessage("bad-id", []byte("not json"))
		err := handler.Handle(msg)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "parse artist.created event")
	})
}
