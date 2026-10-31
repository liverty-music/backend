package event

import (
	"errors"
	"fmt"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/messaging"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-logging/logging"
)

// UserConsumer handles USER.created announcements by starting the new User's
// verification email (UserUseCase.StartSendVerification) in a workflow named
// after the announcement id, so an announcement delivered twice sends one
// email. The workflow worker sends it.
type UserConsumer struct {
	users  usecase.UserUseCase
	logger *logging.Logger
}

// NewUserConsumer creates a new UserConsumer.
func NewUserConsumer(users usecase.UserUseCase, logger *logging.Logger) *UserConsumer {
	return &UserConsumer{users: users, logger: logger}
}

// Handle starts the verification email of a USER.created announcement.
func (h *UserConsumer) Handle(msg *message.Message) error {
	ctx := msg.Context()
	userID, err := parseUserCreated(msg)
	if err != nil {
		h.logger.Error(ctx, "failed to parse user.created event", err)
		return fmt.Errorf("parse user.created event: %w", err)
	}
	if err := h.users.StartSendVerification(ctx, announcementID(msg), userID); err != nil {
		return fmt.Errorf("start the verification email of user %s: %w", userID, err)
	}
	return nil
}

// parseUserCreated returns the new User of a USER.created announcement, or of
// the JSON of one published before the outbox.
func parseUserCreated(msg *message.Message) (string, error) {
	if !messaging.IsProtobuf(msg) {
		var data entity.UserCreatedData
		if err := messaging.ParseCloudEventData(msg, &data); err != nil {
			return "", err
		}
		return data.UserID, nil
	}
	var user entityv1.User
	if err := messaging.ParseProtobufData(msg, &user); err != nil {
		return "", err
	}
	if user.GetId().GetValue() == "" {
		return "", errors.New("the User has no id")
	}
	return user.GetId().GetValue(), nil
}
