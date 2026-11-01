package event

import (
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/liverty-music/backend/internal/infrastructure/messaging"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-logging/logging"
)

// ResaleListingRefundedConsumer handles ORDER.resale_listing_refunded events:
// the sold email to the listing's user. It is idempotent by the listing id,
// so a redelivery sends no second email. While the mail service is
// unavailable the message is delivered again after soldEmailRetryDelay, as
// often as it takes: RecordRefund announces the refund once, so this
// delivery is the only way the user is told. Any other error makes the
// router retry, and after the retry limit the message goes to the poison
// queue.
type ResaleListingRefundedConsumer struct {
	notificationUC usecase.NotificationUseCase
	logger         *logging.Logger
}

// soldEmailRetryDelay is how long a sold email waits for the mail service
// after it was unavailable.
const soldEmailRetryDelay = time.Minute

// NewResaleListingRefundedConsumer creates a new ResaleListingRefundedConsumer.
func NewResaleListingRefundedConsumer(notificationUC usecase.NotificationUseCase, logger *logging.Logger) *ResaleListingRefundedConsumer {
	return &ResaleListingRefundedConsumer{notificationUC: notificationUC, logger: logger}
}

// Handle sends the listing's sold email.
func (h *ResaleListingRefundedConsumer) Handle(msg *message.Message) error {
	data, err := parseResaleListingRefunded(msg)
	if err != nil {
		h.logger.Error(msg.Context(), "failed to parse ORDER.resale_listing_refunded event", err)
		return fmt.Errorf("parse ORDER.resale_listing_refunded event: %w", err)
	}
	h.logger.Info(msg.Context(), "processing ORDER.resale_listing_refunded for the sold email",
		slog.String("resale_listing_id", data.ResaleListingID))
	if err := h.notificationUC.SendResaleSold(msg.Context(), data); err != nil {
		if errors.Is(err, apperr.ErrUnavailable) || errors.Is(err, apperr.ErrDeadlineExceeded) {
			return messaging.RedeliverLater(msg, err, soldEmailRetryDelay)
		}
		return fmt.Errorf("send resale sold email for listing %s: %w", data.ResaleListingID, err)
	}
	return nil
}
