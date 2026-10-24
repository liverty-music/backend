package event

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/messaging"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-logging/logging"
)

// UserConsumer handles user.created events by sending the new User the
// verification email.
type UserConsumer struct {
	users         usecase.UserUseCase
	emailVerifier entity.EmailVerifier
	logger        *logging.Logger
}

// NewUserConsumer creates a new UserConsumer. users reads the new User, whose
// id and preferred language the email needs.
// If emailVerifier is nil, the consumer logs a warning and acknowledges
// messages without processing (local dev without the identity service).
func NewUserConsumer(
	users usecase.UserUseCase,
	emailVerifier entity.EmailVerifier,
	logger *logging.Logger,
) *UserConsumer {
	return &UserConsumer{
		users:         users,
		emailVerifier: emailVerifier,
		logger:        logger,
	}
}

// Handle processes a user.created event by sending a verification email. A
// User deleted since the event, or an address already verified, finishes
// without sending.
func (h *UserConsumer) Handle(msg *message.Message) error {
	ctx := msg.Context()

	var data entity.UserCreatedData
	if err := messaging.ParseCloudEventData(msg, &data); err != nil {
		h.logger.Error(ctx, "failed to parse user.created event", err)
		return fmt.Errorf("parse user.created event: %w", err)
	}
	attrs := slog.String("provider_user_id", data.ProviderUserID)

	if h.emailVerifier == nil {
		h.logger.Warn(ctx, "email verifier not configured, skipping verification", attrs)
		return nil
	}

	user, err := h.users.GetByProviderUserID(ctx, data.ProviderUserID)
	if errors.Is(err, apperr.ErrNotFound) {
		h.logger.Info(ctx, "user deleted since sign-up, skipping verification", attrs)
		return nil
	}
	if err != nil {
		return fmt.Errorf("get user for email verification: %w", err)
	}

	err = h.emailVerifier.SendVerification(ctx, user)
	if errors.Is(err, apperr.ErrFailedPrecondition) {
		// Already verified (e.g. the event was redelivered after the fan
		// verified): nothing to send, and retrying would never succeed.
		h.logger.Info(ctx, "email already verified, skipping verification", slog.String("user_id", user.ID))
		return nil
	}
	if err != nil {
		h.logger.Error(ctx, "failed to send email verification", err, attrs)
		return fmt.Errorf("send email verification: %w", err)
	}

	h.logger.Info(ctx, "email verification sent", slog.String("user_id", user.ID))
	return nil
}
