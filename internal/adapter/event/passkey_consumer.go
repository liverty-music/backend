package event

import (
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/messaging"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-logging/logging"
)

// passkeyRecordRetryDelay is how long the broker waits before delivering a
// passkey change again while its identity has no User yet.
const passkeyRecordRetryDelay = 15 * time.Second

// PasskeyConsumer handles the USER-stream passkey events: the email of a
// passkey change and the record of a passkey change. A returned error makes the
// router retry, and after the retry limit the message goes to the poison
// queue; a passkey change whose identity has no User yet is delivered again
// later instead.
type PasskeyConsumer struct {
	userUC    usecase.UserUseCase
	passkeyUC usecase.PasskeyUseCase
	logger    *logging.Logger
}

// NewPasskeyConsumer creates a new PasskeyConsumer.
func NewPasskeyConsumer(userUC usecase.UserUseCase, passkeyUC usecase.PasskeyUseCase, logger *logging.Logger) *PasskeyConsumer {
	return &PasskeyConsumer{userUC: userUC, passkeyUC: passkeyUC, logger: logger}
}

// HandlePasskeyChanged emails the fan of a USER.passkey_changed event.
func (h *PasskeyConsumer) HandlePasskeyChanged(msg *message.Message) error {
	var data entity.UserPasskeyChangedData
	if err := messaging.ParseCloudEventData(msg, &data); err != nil {
		h.logger.Error(msg.Context(), "failed to parse USER.passkey_changed event", err)
		return fmt.Errorf("parse USER.passkey_changed event: %w", err)
	}
	h.logger.Info(msg.Context(), "processing USER.passkey_changed for the change email",
		slog.String("provider_user_id", data.ProviderUserID), slog.String("kind", data.Kind))
	change := entity.PasskeyChange{
		Kind:       entity.PasskeyChangeKind(data.Kind),
		Name:       data.Name,
		ChangeTime: data.ChangeTime,
	}
	if err := h.userUC.NotifyPasskeyChange(msg.Context(), data.ProviderUserID, change); err != nil {
		return fmt.Errorf("notify passkey change of %s: %w", data.ProviderUserID, err)
	}
	return nil
}

// HandleRecordPasskeyChange records the passkey of a USER.passkey_changed
// event. While the identity has no User yet (a passkey registered at sign-up)
// the message is delivered again after passkeyRecordRetryDelay; the usecase
// stops waiting 10 minutes after the change.
func (h *PasskeyConsumer) HandleRecordPasskeyChange(msg *message.Message) error {
	var data entity.UserPasskeyChangedData
	if err := messaging.ParseCloudEventData(msg, &data); err != nil {
		h.logger.Error(msg.Context(), "failed to parse USER.passkey_changed event", err)
		return fmt.Errorf("parse USER.passkey_changed event: %w", err)
	}
	if err := h.passkeyUC.RecordChange(msg.Context(), data); err != nil {
		if errors.Is(err, usecase.ErrPasskeyAccountPending) {
			h.logger.Info(msg.Context(), "passkey change waits for the identity's user",
				slog.String("provider_user_id", data.ProviderUserID))
			return messaging.RedeliverLater(msg, err, passkeyRecordRetryDelay)
		}
		return fmt.Errorf("record passkey change of %s: %w", data.ProviderUserID, err)
	}
	return nil
}
