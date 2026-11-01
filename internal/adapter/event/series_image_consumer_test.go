package event_test

import (
	"encoding/json"
	"fmt"
	"testing"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/liverty-music/backend/internal/adapter/event"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/messaging"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

// makeSeriesImageUploadedMsg builds a SERIES_IMAGE.uploaded message in the
// JSON published before the outbox.
func makeSeriesImageUploadedMsg(t *testing.T, seriesImageID, seriesID string) *message.Message {
	t.Helper()
	payload, err := json.Marshal(entity.SeriesImageUploadedData{
		SeriesImageID: seriesImageID,
		SeriesID:      seriesID,
	})
	require.NoError(t, err)
	msg := message.NewMessage("test-uuid", payload)
	return msg
}

// makeSeriesImageRequestMsg builds a SERIES_IMAGE.uploaded request as
// SeriesImage.Create records it: the SeriesImage in protobuf and the Series in
// ce_subject.
func makeSeriesImageRequestMsg(t *testing.T, seriesImageID, seriesID string) *message.Message {
	t.Helper()
	payload, err := proto.Marshal(&entityv1.SeriesImage{
		Id:          &entityv1.SeriesImageId{Value: seriesImageID},
		OrganizerId: &entityv1.OrganizerId{Value: "org-1"},
	})
	require.NoError(t, err)
	msg := message.NewMessage("test-uuid", payload)
	msg.Metadata.Set("ce_datacontenttype", messaging.ContentTypeProtobuf)
	if seriesID != "" {
		msg.Metadata.Set("ce_subject", seriesID)
	}
	return msg
}

// TestSeriesImageConsumer_Handle verifies that SeriesImageConsumer only decodes the
// message and delegates orchestration to SeriesImageUseCase.Process — the
// orchestration itself (read/transcode/cut-over/cleanup) is covered by the
// SeriesImageUseCase tests in internal/usecase/series_image_uc_test.go now that the logic
// lives there.
func TestSeriesImageConsumer_Handle(t *testing.T) {
	t.Parallel()

	// @spec components/usecase/series-image/process "SeriesImage uploaded"
	t.Run("processes the SeriesImage for the Series the request names", func(t *testing.T) {
		t.Parallel()

		seriesImageUC := ucmocks.NewMockSeriesImageUseCase(t)
		handler := event.NewSeriesImageConsumer(seriesImageUC, newTestLogger(t))

		seriesImageUC.EXPECT().Process(anyCtx, "image-1", "series-1").Return(nil).Once()

		assert.NoError(t, handler.Handle(makeSeriesImageRequestMsg(t, "image-1", "series-1")))
	})

	t.Run("rejects a request that names no Series", func(t *testing.T) {
		t.Parallel()

		handler := event.NewSeriesImageConsumer(ucmocks.NewMockSeriesImageUseCase(t), newTestLogger(t))

		assert.Error(t, handler.Handle(makeSeriesImageRequestMsg(t, "image-1", "")))
	})

	t.Run("delegates a JSON request to Process", func(t *testing.T) {
		t.Parallel()

		seriesImageUC := ucmocks.NewMockSeriesImageUseCase(t)
		handler := event.NewSeriesImageConsumer(seriesImageUC, newTestLogger(t))

		seriesImageUC.EXPECT().Process(anyCtx, "image-1", "series-1").Return(nil).Once()

		err := handler.Handle(makeSeriesImageUploadedMsg(t, "image-1", "series-1"))
		assert.NoError(t, err)
	})

	t.Run("returns error when use case fails", func(t *testing.T) {
		t.Parallel()

		seriesImageUC := ucmocks.NewMockSeriesImageUseCase(t)
		handler := event.NewSeriesImageConsumer(seriesImageUC, newTestLogger(t))

		seriesImageUC.EXPECT().Process(anyCtx, "image-2", "series-2").Return(fmt.Errorf("gcs unavailable")).Once()

		err := handler.Handle(makeSeriesImageUploadedMsg(t, "image-2", "series-2"))
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "process series image image-2")
	})

	t.Run("returns error on invalid payload", func(t *testing.T) {
		t.Parallel()

		seriesImageUC := ucmocks.NewMockSeriesImageUseCase(t)
		handler := event.NewSeriesImageConsumer(seriesImageUC, newTestLogger(t))

		msg := message.NewMessage("bad-id", []byte("not json"))
		err := handler.Handle(msg)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "parse SERIES_IMAGE.uploaded")
	})
}
