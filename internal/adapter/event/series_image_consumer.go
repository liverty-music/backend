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

// SeriesImageConsumer handles SERIES_IMAGE.uploaded events by delegating orchestration
// (read original, transcode, cut over series_cover_images, reclaim storage) to
// SeriesImageUseCase.Process. It only decodes the message.
type SeriesImageConsumer struct {
	seriesImageUC usecase.SeriesImageUseCase
	logger        *logging.Logger
}

// NewSeriesImageConsumer creates a new SeriesImageConsumer.
func NewSeriesImageConsumer(
	seriesImageUC usecase.SeriesImageUseCase,
	logger *logging.Logger,
) *SeriesImageConsumer {
	return &SeriesImageConsumer{
		seriesImageUC: seriesImageUC,
		logger:        logger,
	}
}

// Handle processes a SERIES_IMAGE.uploaded event by parsing the payload and
// delegating to SeriesImageUseCase.Process. It returns nil to ack (including
// after a permanent-failure cleanup the use case already performed) or a
// wrapped error to nak (retry).
func (h *SeriesImageConsumer) Handle(msg *message.Message) error {
	ctx := msg.Context()

	var data entity.SeriesImageUploadedData
	if err := messaging.ParseCloudEventData(msg, &data); err != nil {
		h.logger.Error(ctx, "failed to parse SERIES_IMAGE.uploaded event", err)
		return fmt.Errorf("parse SERIES_IMAGE.uploaded: %w", err)
	}

	h.logger.Info(ctx, "processing SERIES_IMAGE.uploaded event",
		slog.String("series_image_id", data.SeriesImageID),
		slog.String("series_id", data.SeriesID),
	)

	if err := h.seriesImageUC.Process(ctx, data.SeriesImageID, data.SeriesID); err != nil {
		return fmt.Errorf("process series image %s: %w", data.SeriesImageID, err)
	}

	return nil
}
