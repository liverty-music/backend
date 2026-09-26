package event_test

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/liverty-music/backend/internal/adapter/event"
	"github.com/liverty-music/backend/internal/entity"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// makeMediaUploadedMsg builds a Watermill message carrying a MEDIA.uploaded payload.
func makeMediaUploadedMsg(t *testing.T, mediaID, seriesID string) *message.Message {
	t.Helper()
	payload, err := json.Marshal(entity.MediaUploadedData{
		MediaID:  mediaID,
		SeriesID: seriesID,
	})
	require.NoError(t, err)
	msg := message.NewMessage("test-uuid", payload)
	return msg
}

// TestMediaConsumer_Handle verifies that MediaConsumer only decodes the
// message and delegates orchestration to MediaUseCase.ProcessMedia — the
// orchestration itself (read/transcode/cut-over/cleanup) is covered by the
// MediaUseCase tests in internal/usecase/media_uc_test.go now that the logic
// lives there.
func TestMediaConsumer_Handle(t *testing.T) {
	t.Parallel()

	t.Run("delegates to ProcessMedia", func(t *testing.T) {
		t.Parallel()

		mediaUC := ucmocks.NewMockMediaUseCase(t)
		handler := event.NewMediaConsumer(mediaUC, newTestLogger(t))

		mediaUC.EXPECT().ProcessMedia(anyCtx, "media-1", "series-1").Return(nil).Once()

		err := handler.Handle(makeMediaUploadedMsg(t, "media-1", "series-1"))
		assert.NoError(t, err)
	})

	t.Run("returns error when use case fails", func(t *testing.T) {
		t.Parallel()

		mediaUC := ucmocks.NewMockMediaUseCase(t)
		handler := event.NewMediaConsumer(mediaUC, newTestLogger(t))

		mediaUC.EXPECT().ProcessMedia(anyCtx, "media-2", "series-2").Return(fmt.Errorf("gcs unavailable")).Once()

		err := handler.Handle(makeMediaUploadedMsg(t, "media-2", "series-2"))
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "process media media-2")
	})

	t.Run("returns error on invalid payload", func(t *testing.T) {
		t.Parallel()

		mediaUC := ucmocks.NewMockMediaUseCase(t)
		handler := event.NewMediaConsumer(mediaUC, newTestLogger(t))

		msg := message.NewMessage("bad-id", []byte("not json"))
		err := handler.Handle(msg)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "parse MEDIA.uploaded")
	})
}
