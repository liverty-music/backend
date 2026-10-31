package event_test

import (
	"encoding/json"
	"errors"
	"testing"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/liverty-music/backend/internal/adapter/event"
	"github.com/liverty-music/backend/internal/entity"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
)

func TestUserConsumer_Handle(t *testing.T) {
	t.Parallel()

	const userID = "019a0000-0000-7000-8000-000000000001"
	newUser := &entityv1.User{Id: &entityv1.UserId{Value: userID}}

	t.Run("starts the new User's verification email named after the announcement", func(t *testing.T) {
		t.Parallel()
		users := ucmocks.NewMockUserUseCase(t)
		users.EXPECT().StartSendVerification(anyCtx, "announcement-1", userID).Return(nil).Once()

		assert.NoError(t, event.NewUserConsumer(users, newTestLogger(t)).Handle(makeAnnouncementMsg(t, "announcement-1", "", newUser)))
	})

	t.Run("an announcement delivered twice starts the same email", func(t *testing.T) {
		t.Parallel()
		users := ucmocks.NewMockUserUseCase(t)
		// The second start under the same id starts nothing new
		// (UserUseCase.StartSendVerification names the run after it).
		users.EXPECT().StartSendVerification(anyCtx, "announcement-1", userID).Return(nil).Twice()
		consumer := event.NewUserConsumer(users, newTestLogger(t))

		require.NoError(t, consumer.Handle(makeAnnouncementMsg(t, "announcement-1", "", newUser)))
		require.NoError(t, consumer.Handle(makeAnnouncementMsg(t, "announcement-1", "", newUser)))
	})

	t.Run("reads the JSON of a USER.created published before the outbox", func(t *testing.T) {
		t.Parallel()
		users := ucmocks.NewMockUserUseCase(t)
		users.EXPECT().StartSendVerification(anyCtx, "json-1", userID).Return(nil).Once()
		payload, err := json.Marshal(entity.UserCreatedData{UserID: userID, ProviderUserID: "zitadel-user-001"})
		require.NoError(t, err)

		assert.NoError(t, event.NewUserConsumer(users, newTestLogger(t)).Handle(message.NewMessage("json-1", payload)))
	})

	t.Run("returns a failed start for a retry", func(t *testing.T) {
		t.Parallel()
		users := ucmocks.NewMockUserUseCase(t)
		users.EXPECT().StartSendVerification(anyCtx, "announcement-1", userID).Return(errors.New("database down")).Once()

		assert.Error(t, event.NewUserConsumer(users, newTestLogger(t)).Handle(makeAnnouncementMsg(t, "announcement-1", "", newUser)))
	})

	t.Run("rejects a malformed payload", func(t *testing.T) {
		t.Parallel()
		consumer := event.NewUserConsumer(ucmocks.NewMockUserUseCase(t), newTestLogger(t))

		assert.Error(t, consumer.Handle(message.NewMessage("x", []byte("not json"))))
	})
}
