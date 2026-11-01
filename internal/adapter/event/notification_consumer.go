package event

import (
	"errors"
	"fmt"
	"log/slog"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/liverty-music/backend/internal/infrastructure/messaging"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-logging/logging"
)

// NotificationConsumer handles concert.created.v1 events by delegating to the
// push notification use case. It is a thin adapter: parse the CloudEvent and
// hand off to the use case — no repository lookups or business logic here.
type NotificationConsumer struct {
	notificationUC usecase.NotificationUseCase
	logger         *logging.Logger
}

// NewNotificationConsumer creates a new NotificationConsumer.
func NewNotificationConsumer(
	notificationUC usecase.NotificationUseCase,
	logger *logging.Logger,
) *NotificationConsumer {
	return &NotificationConsumer{
		notificationUC: notificationUC,
		logger:         logger,
	}
}

// Handle processes a concert.created.v1 event by notifying all followers of the artist.
func (h *NotificationConsumer) Handle(msg *message.Message) error {
	ctx := msg.Context()

	data, err := parseConcertCreated(msg)
	if err != nil {
		h.logger.Error(ctx, "failed to parse concert.created event", err)
		return fmt.Errorf("parse concert.created event: %w", err)
	}

	h.logger.Info(ctx, "processing concert.created event for notifications",
		slog.String("artist_id", data.ArtistID),
		slog.Int("concert_count", len(data.ConcertIDs)),
	)

	if err := h.notificationUC.NotifyNewConcerts(ctx, data); err != nil {
		return fmt.Errorf("notify for artist %s: %w", data.ArtistID, err)
	}

	return nil
}

// parseConcertCreated reads a CONCERT.created announcement: the Concerts of
// its ConcertList for the Artist named by ce_subject, notified under the
// announcement id, or the JSON of one published before the outbox.
func parseConcertCreated(msg *message.Message) (usecase.ConcertCreatedData, error) {
	if !messaging.IsProtobuf(msg) {
		var data usecase.ConcertCreatedData
		err := messaging.ParseCloudEventData(msg, &data)
		return data, err
	}
	var list entityv1.ConcertList
	if err := messaging.ParseProtobufData(msg, &list); err != nil {
		return usecase.ConcertCreatedData{}, err
	}
	data := usecase.ConcertCreatedData{ArtistID: messaging.CloudEventSubject(msg), RequestID: announcementID(msg)}
	for _, c := range list.GetConcerts() {
		data.ConcertIDs = append(data.ConcertIDs, c.GetEvent().GetId().GetValue())
	}
	if data.ArtistID == "" || len(data.ConcertIDs) == 0 {
		return usecase.ConcertCreatedData{}, errors.New("the announcement names no Artist or no Concert")
	}
	return data, nil
}
