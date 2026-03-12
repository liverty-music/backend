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

// ArtistNameConsumer handles artist.created events by delegating canonical
// name resolution to the ArtistNameResolutionUseCase.
type ArtistNameConsumer struct {
	nameResolutionUC usecase.ArtistNameResolutionUseCase
	logger           *logging.Logger
}

// NewArtistNameConsumer creates a new ArtistNameConsumer.
func NewArtistNameConsumer(
	nameResolutionUC usecase.ArtistNameResolutionUseCase,
	logger *logging.Logger,
) *ArtistNameConsumer {
	return &ArtistNameConsumer{
		nameResolutionUC: nameResolutionUC,
		logger:           logger,
	}
}

// Handle processes an artist.created event by parsing the payload and
// delegating canonical name resolution to the use case layer.
func (h *ArtistNameConsumer) Handle(msg *message.Message) error {
	ctx := msg.Context()

	var data entity.ArtistCreatedData
	if err := messaging.ParseCloudEventData(msg, &data); err != nil {
		return fmt.Errorf("parse artist.created event: %w", err)
	}

	h.logger.Info(ctx, "processing artist.created event",
		slog.String("artist_id", data.ArtistID),
		slog.String("artist_name", data.ArtistName),
		slog.String("mbid", data.MBID),
	)

	if err := h.nameResolutionUC.ResolveCanonicalName(ctx, data.ArtistID, data.MBID, data.ArtistName); err != nil {
		return fmt.Errorf("handle ARTIST.created event: %w", err)
	}

	return nil
}
