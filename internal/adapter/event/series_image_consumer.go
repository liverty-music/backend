// Package event provides Watermill event consumers for the consumer process.
package event

import (
	"errors"
	"fmt"
	"log/slog"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/messaging"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-logging/logging"
)

// SeriesImageConsumer handles SERIES_IMAGE.uploaded requests by delegating orchestration
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

// Handle processes a SERIES_IMAGE.uploaded request by reading the SeriesImage
// and the Series it names and delegating to SeriesImageUseCase.Process. It
// returns nil to ack (including after a permanent-failure cleanup the use case
// already performed) or a wrapped error to nak (retry).
func (h *SeriesImageConsumer) Handle(msg *message.Message) error {
	ctx := msg.Context()

	seriesImageID, seriesID, err := parseSeriesImageUploaded(msg)
	if err != nil {
		h.logger.Error(ctx, "failed to parse SERIES_IMAGE.uploaded event", err)
		return fmt.Errorf("parse SERIES_IMAGE.uploaded: %w", err)
	}

	h.logger.Info(ctx, "processing SERIES_IMAGE.uploaded event",
		slog.String("series_image_id", seriesImageID),
		slog.String("series_id", seriesID),
	)

	if err := h.seriesImageUC.Process(ctx, seriesImageID, seriesID); err != nil {
		return fmt.Errorf("process series image %s: %w", seriesImageID, err)
	}

	return nil
}

// parseSeriesImageUploaded returns the SeriesImage and the Series of a
// processing request: from the SeriesImage payload and ce_subject when it was
// recorded by SeriesImage.Create, or from the JSON of a request published
// before the outbox.
func parseSeriesImageUploaded(msg *message.Message) (seriesImageID, seriesID string, err error) {
	if !messaging.IsProtobuf(msg) {
		var data entity.SeriesImageUploadedData
		if err := messaging.ParseCloudEventData(msg, &data); err != nil {
			return "", "", err
		}
		return data.SeriesImageID, data.SeriesID, nil
	}
	var img entityv1.SeriesImage
	if err := messaging.ParseProtobufData(msg, &img); err != nil {
		return "", "", err
	}
	seriesImageID, seriesID = img.GetId().GetValue(), messaging.CloudEventSubject(msg)
	if seriesImageID == "" || seriesID == "" {
		return "", "", errors.New("the request names no SeriesImage or no Series")
	}
	return seriesImageID, seriesID, nil
}
