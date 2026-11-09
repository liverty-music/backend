package event

import (
	"fmt"
	"log/slog"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-logging/logging"
)

// ArtistRefreshConsumer handles artist.created events by reading the new
// artist again from the music catalog through ArtistUseCase.Refresh.
type ArtistRefreshConsumer struct {
	artistUC usecase.ArtistUseCase
	logger   *logging.Logger
}

// NewArtistRefreshConsumer creates a new ArtistRefreshConsumer.
func NewArtistRefreshConsumer(
	artistUC usecase.ArtistUseCase,
	logger *logging.Logger,
) *ArtistRefreshConsumer {
	return &ArtistRefreshConsumer{
		artistUC: artistUC,
		logger:   logger,
	}
}

// Handle processes an artist.created event by parsing the payload and
// refreshing the announced artist from the catalog.
func (h *ArtistRefreshConsumer) Handle(msg *message.Message) error {
	ctx := msg.Context()

	data, err := parseArtistCreated(msg)
	if err != nil {
		return fmt.Errorf("parse artist.created event: %w", err)
	}

	h.logger.Info(ctx, "processing artist.created event",
		slog.String("artist_id", data.ArtistID),
		slog.String("artist_name", data.ArtistName),
		slog.String("mbid", data.MBID),
	)

	if err := h.artistUC.Refresh(ctx, data.ArtistID, data.MBID, data.ArtistName); err != nil {
		return fmt.Errorf("handle ARTIST.created event: %w", err)
	}

	return nil
}
