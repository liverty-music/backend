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

// ResaleListingRefundedConsumer handles ORDER.resale_listing_refunded events:
// the sold email to the listing's user. It is idempotent by the listing id,
// so a redelivery sends no second email. A returned error makes the router
// redeliver, and after the redelivery limit the message goes to the poison
// queue.
type ResaleListingRefundedConsumer struct {
	notificationUC usecase.NotificationUseCase
	logger         *logging.Logger
}

// NewResaleListingRefundedConsumer creates a new ResaleListingRefundedConsumer.
func NewResaleListingRefundedConsumer(notificationUC usecase.NotificationUseCase, logger *logging.Logger) *ResaleListingRefundedConsumer {
	return &ResaleListingRefundedConsumer{notificationUC: notificationUC, logger: logger}
}

// Handle sends the listing's sold email.
func (h *ResaleListingRefundedConsumer) Handle(msg *message.Message) error {
	var data entity.ResaleListingRefundedData
	if err := messaging.ParseCloudEventData(msg, &data); err != nil {
		h.logger.Error(msg.Context(), "failed to parse ORDER.resale_listing_refunded event", err)
		return fmt.Errorf("parse ORDER.resale_listing_refunded event: %w", err)
	}
	h.logger.Info(msg.Context(), "processing ORDER.resale_listing_refunded for the sold email",
		slog.String("resale_listing_id", data.ResaleListingID))
	if err := h.notificationUC.SendResaleSold(msg.Context(), data); err != nil {
		return fmt.Errorf("send resale sold email for listing %s: %w", data.ResaleListingID, err)
	}
	return nil
}
