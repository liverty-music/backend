package event

import (
	"fmt"
	"log/slog"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/messaging"
	"github.com/pannpers/go-logging/logging"
)

// UserConsumer handles user.created events by triggering email verification
// via the Zitadel API.
type UserConsumer struct {
	emailVerifier entity.EmailVerifier
	logger        *logging.Logger
}

// NewUserConsumer creates a new UserConsumer.
// If emailVerifier is nil, the consumer logs a warning and acknowledges
// messages without processing (local dev without Zitadel key).
func NewUserConsumer(
	emailVerifier entity.EmailVerifier,
	logger *logging.Logger,
) *UserConsumer {
	return &UserConsumer{
		emailVerifier: emailVerifier,
		logger:        logger,
	}
}

// Handle processes a user.created event by sending a verification email.
func (h *UserConsumer) Handle(msg *message.Message) error {
	ctx := msg.Context()

	var data entity.UserCreatedData
	if err := messaging.ParseCloudEventData(msg, &data); err != nil {
		h.logger.Error(ctx, "failed to parse user.created event", err)
		return fmt.Errorf("parse user.created event: %w", err)
	}

	if h.emailVerifier == nil {
		h.logger.Warn(ctx, "email verifier not configured, skipping verification",
			slog.String("provider_user_id", data.ProviderUserID),
		)
		return nil
	}

	h.logger.Info(ctx, "sending email verification",
		slog.String("provider_user_id", data.ProviderUserID),
	)

	if err := h.emailVerifier.SendVerification(ctx, data.ProviderUserID); err != nil {
		h.logger.Error(ctx, "failed to send email verification", err,
			slog.String("provider_user_id", data.ProviderUserID),
		)
		return fmt.Errorf("send email verification: %w", err)
	}

	return nil
}
