package event_test

import (
	"fmt"
	"testing"

	"github.com/liverty-music/backend/internal/adapter/event"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/stretchr/testify/assert"
)

func TestArtistImageConsumer_Handle(t *testing.T) {
	t.Parallel()

	t.Run("returns error when use case fails", func(t *testing.T) {
		t.Parallel()

		imageSyncUC := ucmocks.NewMockArtistImageSyncUseCase(t)
		handler := event.NewArtistImageConsumer(imageSyncUC, newTestLogger(t))

		imageSyncUC.EXPECT().SyncArtistImage(anyCtx, "artist-2", "mbid-fail").Return(fmt.Errorf("fanarttv unavailable")).Once()

		msg := makeAnnouncementMsg(t, "announcement-1", "", newArtistAnnouncement("artist-2", "Failing Artist", "mbid-fail"))

		err := handler.Handle(msg)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "handle ARTIST.created event for image sync")
	})

	t.Run("returns error on invalid payload", func(t *testing.T) {
		t.Parallel()

		imageSyncUC := ucmocks.NewMockArtistImageSyncUseCase(t)
		handler := event.NewArtistImageConsumer(imageSyncUC, newTestLogger(t))

		err := handler.Handle(malformedMsg())
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "parse artist.created event")
	})
}
