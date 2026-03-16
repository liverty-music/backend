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

// ArtistImageConsumer handles artist.created events by delegating fanart.tv
// image resolution to the ArtistImageSyncUseCase.
type ArtistImageConsumer struct {
	imageSyncUC usecase.ArtistImageSyncUseCase
	logger      *logging.Logger
}

// NewArtistImageConsumer creates a new ArtistImageConsumer.
func NewArtistImageConsumer(
	imageSyncUC usecase.ArtistImageSyncUseCase,
	logger *logging.Logger,
) *ArtistImageConsumer {
	return &ArtistImageConsumer{
		imageSyncUC: imageSyncUC,
		logger:      logger,
	}
}

// Handle processes an artist.created event by fetching and persisting
// fanart.tv image data for the newly created artist.
func (h *ArtistImageConsumer) Handle(msg *message.Message) error {
	ctx := msg.Context()

	var data entity.ArtistCreatedData
	if err := messaging.ParseCloudEventData(msg, &data); err != nil {
		return fmt.Errorf("parse artist.created event: %w", err)
	}

	h.logger.Info(ctx, "processing artist.created event for image sync",
		slog.String("artist_id", data.ArtistID),
		slog.String("artist_name", data.ArtistName),
		slog.String("mbid", data.MBID),
	)

	if err := h.imageSyncUC.SyncArtistImage(ctx, data.ArtistID, data.MBID); err != nil {
		return fmt.Errorf("handle ARTIST.created event for image sync: %w", err)
	}

	return nil
}
