package event

import (
	"errors"
	"fmt"
	"log/slog"
	"time"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/messaging"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-logging/logging"
)

// PasskeyConsumer handles the Passkeys Passkey.Create announces as added
// (USER.passkey_added) and Passkey.Delete as removed (USER.passkey_removed):
// it starts the change email of each, in a workflow named after the
// announcement id. A returned error makes the router retry, and after the
// retry limit the message goes to the poison queue.
type PasskeyConsumer struct {
	userUC usecase.UserUseCase
	logger *logging.Logger
}

// NewPasskeyConsumer creates a new PasskeyConsumer.
func NewPasskeyConsumer(userUC usecase.UserUseCase, logger *logging.Logger) *PasskeyConsumer {
	return &PasskeyConsumer{userUC: userUC, logger: logger}
}

// HandlePasskeyAdded starts the added email of a USER.passkey_added
// announcement, with the Passkey's name and its create time as the time of
// the change.
func (h *PasskeyConsumer) HandlePasskeyAdded(msg *message.Message) error {
	return h.handle(msg, entity.PasskeyChangeAdded, entity.SubjectUserPasskeyAdded)
}

// HandlePasskeyRemoved starts the removed email of a USER.passkey_removed
// announcement, with the time the removal was announced as the time of the
// change.
func (h *PasskeyConsumer) HandlePasskeyRemoved(msg *message.Message) error {
	return h.handle(msg, entity.PasskeyChangeRemoved, entity.SubjectUserPasskeyRemoved)
}

func (h *PasskeyConsumer) handle(msg *message.Message, kind entity.PasskeyChangeKind, subject string) error {
	ctx := msg.Context()
	notice, err := parsePasskeyAnnouncement(msg, kind)
	if err != nil {
		h.logger.Error(ctx, "failed to parse "+subject+" announcement", err)
		return fmt.Errorf("parse %s announcement: %w", subject, err)
	}
	h.logger.Info(ctx, "starting the passkey change email",
		slog.String("user_id", notice.UserID), slog.String("kind", string(kind)))
	if err := h.userUC.StartNotifyPasskeyChange(ctx, announcementID(msg), notice); err != nil {
		return fmt.Errorf("start the passkey change email of user %s: %w", notice.UserID, err)
	}
	return nil
}

// parsePasskeyAnnouncement reads the Passkey of an announcement as the change
// to email.
func parsePasskeyAnnouncement(msg *message.Message, kind entity.PasskeyChangeKind) (usecase.PasskeyChangeNotice, error) {
	var passkey entityv1.Passkey
	if err := messaging.ParseProtobufData(msg, &passkey); err != nil {
		return usecase.PasskeyChangeNotice{}, err
	}
	userID := passkey.GetUserId().GetValue()
	if userID == "" {
		return usecase.PasskeyChangeNotice{}, errors.New("the Passkey names no User")
	}
	change := entity.PasskeyChange{Kind: kind}
	switch kind {
	case entity.PasskeyChangeAdded:
		change.Name = passkey.GetName().GetValue()
		change.ChangeTime = passkey.GetCreateTime().AsTime()
	case entity.PasskeyChangeRemoved:
		announced, err := time.Parse(time.RFC3339Nano, msg.Metadata.Get("ce_time"))
		if err != nil {
			return usecase.PasskeyChangeNotice{}, fmt.Errorf("read the time of the removal: %w", err)
		}
		change.ChangeTime = announced
	}
	return usecase.PasskeyChangeNotice{UserID: userID, Change: change}, nil
}
