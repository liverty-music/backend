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

// ReissueEmailVerificationConsumer handles USER.email_verification_resend_requested
// events by sending the fresh verification code. It runs in the event consumer,
// which holds the identity-provider credential the fan API does not. A returned
// error makes the router redeliver; a redelivery sends a newer code, which is
// harmless.
type ReissueEmailVerificationConsumer struct {
	userUC usecase.UserUseCase
	logger *logging.Logger
}

// NewReissueEmailVerificationConsumer creates a new ReissueEmailVerificationConsumer.
func NewReissueEmailVerificationConsumer(userUC usecase.UserUseCase, logger *logging.Logger) *ReissueEmailVerificationConsumer {
	return &ReissueEmailVerificationConsumer{userUC: userUC, logger: logger}
}

// Handle sends the fresh verification code for one announced resend request.
func (h *ReissueEmailVerificationConsumer) Handle(msg *message.Message) error {
	var data entity.UserEmailVerificationResendRequestedData
	if err := messaging.ParseCloudEventData(msg, &data); err != nil {
		h.logger.Error(msg.Context(), "failed to parse USER.email_verification_resend_requested event", err)
		return fmt.Errorf("parse USER.email_verification_resend_requested event: %w", err)
	}
	h.logger.Info(msg.Context(), "processing email verification resend request",
		slog.String("user_id", data.UserID))
	if err := h.userUC.ReissueEmailVerification(msg.Context(), data.UserID); err != nil {
		return fmt.Errorf("reissue email verification for user %s: %w", data.UserID, err)
	}
	return nil
}
