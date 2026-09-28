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

// FollowSearchConsumer handles ARTIST.followed events by triggering concert
// discovery the first time an artist is followed. The never-searched check
// and the search itself live in ConcertUseCase.SearchNewConcertsOnFirstFollow;
// this consumer only decodes the message and calls it.
type FollowSearchConsumer struct {
	concertUC usecase.ConcertUseCase
	logger    *logging.Logger
}

// NewFollowSearchConsumer creates a new FollowSearchConsumer.
func NewFollowSearchConsumer(
	concertUC usecase.ConcertUseCase,
	logger *logging.Logger,
) *FollowSearchConsumer {
	return &FollowSearchConsumer{
		concertUC: concertUC,
		logger:    logger,
	}
}

// Handle processes an ARTIST.followed event by triggering
// SearchNewConcertsOnFirstFollow for the followed artist. A search failure is
// returned so the router retries (and, if retries are exhausted, poison-queues
// and logs it) rather than being swallowed here — the Follow use case has
// already succeeded and persisted by the time this runs, so retry/poison
// semantics here never affect the fan-facing Follow call.
func (h *FollowSearchConsumer) Handle(msg *message.Message) error {
	ctx := msg.Context()

	var data entity.ArtistFollowedData
	if err := messaging.ParseCloudEventData(msg, &data); err != nil {
		h.logger.Error(ctx, "failed to parse ARTIST.followed event", err)
		return fmt.Errorf("parse ARTIST.followed event: %w", err)
	}

	h.logger.Info(ctx, "processing ARTIST.followed event for first-follow search",
		slog.String("user_id", data.UserID),
		slog.String("artist_id", data.ArtistID),
	)

	if err := h.concertUC.SearchNewConcertsOnFirstFollow(ctx, data.ArtistID); err != nil {
		return fmt.Errorf("search new concerts on first follow for artist %s: %w", data.ArtistID, err)
	}

	return nil
}
