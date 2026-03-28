package event_test

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/liverty-music/backend/internal/adapter/event"
	"github.com/liverty-music/backend/internal/entity"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func makeUserCreatedMsg(t *testing.T, data entity.UserCreatedData) *message.Message {
	t.Helper()
	payload, err := json.Marshal(data)
	require.NoError(t, err)
	return message.NewMessage("test-id", payload)
}

func TestUserConsumer_Handle(t *testing.T) {
	t.Parallel()

	t.Run("delegates SendVerification to email verifier", func(t *testing.T) {
		t.Parallel()

		emailVerifier := ucmocks.NewMockEmailVerifier(t)
		handler := event.NewUserConsumer(emailVerifier, newTestLogger(t))

		emailVerifier.EXPECT().SendVerification(anyCtx, "zitadel-user-001").Return(nil).Once()

		msg := makeUserCreatedMsg(t, entity.UserCreatedData{
			ExternalID: "zitadel-user-001",
			Email:      "user@example.com",
		})

		err := handler.Handle(msg)
		assert.NoError(t, err)
	})

	t.Run("returns error when email verifier fails", func(t *testing.T) {
		t.Parallel()

		emailVerifier := ucmocks.NewMockEmailVerifier(t)
		handler := event.NewUserConsumer(emailVerifier, newTestLogger(t))

		emailVerifier.EXPECT().
			SendVerification(anyCtx, "zitadel-user-002").
			Return(fmt.Errorf("zitadel unavailable")).
			Once()

		msg := makeUserCreatedMsg(t, entity.UserCreatedData{
			ExternalID: "zitadel-user-002",
			Email:      "other@example.com",
		})

		err := handler.Handle(msg)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "send email verification")
	})

	t.Run("skips verification when email verifier is nil", func(t *testing.T) {
		t.Parallel()

		// When no Zitadel key is configured (local dev), emailVerifier is nil.
		handler := event.NewUserConsumer(nil, newTestLogger(t))

		msg := makeUserCreatedMsg(t, entity.UserCreatedData{
			ExternalID: "zitadel-user-003",
			Email:      "dev@example.com",
		})

		err := handler.Handle(msg)
		assert.NoError(t, err)
	})

	t.Run("returns error on malformed JSON payload", func(t *testing.T) {
		t.Parallel()

		emailVerifier := ucmocks.NewMockEmailVerifier(t)
		handler := event.NewUserConsumer(emailVerifier, newTestLogger(t))

		msg := message.NewMessage("bad-id", []byte("{not valid json"))

		err := handler.Handle(msg)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "parse user.created event")
	})
}
