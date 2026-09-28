// Package event provides Watermill event consumers for the consumer process.
package event

import (
	"fmt"
	"log/slog"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/messaging"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-logging/logging"
)

// MediaConsumer handles MEDIA.uploaded events by delegating orchestration
// (read original, transcode, cut over series_media, reclaim storage) to
// MediaUseCase.ProcessMedia. It only decodes the message.
type MediaConsumer struct {
	mediaUC usecase.MediaUseCase
	logger  *logging.Logger
}

// NewMediaConsumer creates a new MediaConsumer.
func NewMediaConsumer(
	mediaUC usecase.MediaUseCase,
	logger *logging.Logger,
) *MediaConsumer {
	return &MediaConsumer{
		mediaUC: mediaUC,
		logger:  logger,
	}
}

// Handle processes a MEDIA.uploaded event by parsing the payload and
// delegating to MediaUseCase.ProcessMedia. It returns nil to ack (including
// after a permanent-failure cleanup the use case already performed) or a
// wrapped error to nak (retry).
func (h *MediaConsumer) Handle(msg *message.Message) error {
	ctx := msg.Context()

	var data entity.MediaUploadedData
	if err := messaging.ParseCloudEventData(msg, &data); err != nil {
		h.logger.Error(ctx, "failed to parse MEDIA.uploaded event", err)
		return fmt.Errorf("parse MEDIA.uploaded: %w", err)
	}

	h.logger.Info(ctx, "processing MEDIA.uploaded event",
		slog.String("media_id", data.MediaID),
		slog.String("series_id", data.SeriesID),
	)

	if err := h.mediaUC.ProcessMedia(ctx, data.MediaID, data.SeriesID); err != nil {
		return fmt.Errorf("process media %s: %w", data.MediaID, err)
	}

	return nil
}
