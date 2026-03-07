package event

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/messaging"
	"github.com/pannpers/go-logging/logging"
)

// ArtistNameConsumer handles artist.created events by resolving the canonical
// artist name from MusicBrainz and updating it in the database if it differs.
type ArtistNameConsumer struct {
	artistRepo entity.ArtistRepository
	idManager  entity.ArtistIdentityManager
	logger     *logging.Logger
}

// NewArtistNameConsumer creates a new ArtistNameConsumer.
func NewArtistNameConsumer(
	artistRepo entity.ArtistRepository,
	idManager entity.ArtistIdentityManager,
	logger *logging.Logger,
) *ArtistNameConsumer {
	return &ArtistNameConsumer{
		artistRepo: artistRepo,
		idManager:  idManager,
		logger:     logger,
	}
}

// Handle processes an artist.created event by resolving the canonical name
// from MusicBrainz and updating the artist record if the name differs.
func (h *ArtistNameConsumer) Handle(msg *message.Message) error {
	ctx := context.Background()

	var data messaging.ArtistCreatedData
	if err := messaging.ParseCloudEventData(msg, &data); err != nil {
		return fmt.Errorf("parse artist.created event: %w", err)
	}

	h.logger.Info(ctx, "processing artist.created event",
		slog.String("artist_id", data.ArtistID),
		slog.String("artist_name", data.ArtistName),
		slog.String("mbid", data.MBID),
	)

	canonical, err := h.idManager.GetArtist(ctx, data.MBID)
	if err != nil {
		return fmt.Errorf("resolve canonical name: %w", err)
	}

	if canonical.Name == data.ArtistName {
		return nil
	}

	if err := h.artistRepo.UpdateName(ctx, data.ArtistID, canonical.Name); err != nil {
		return fmt.Errorf("update artist name: %w", err)
	}

	h.logger.Info(ctx, "artist name updated to canonical",
		slog.String("artist_id", data.ArtistID),
		slog.String("old_name", data.ArtistName),
		slog.String("new_name", canonical.Name),
	)

	return nil
}
