package event_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/liverty-music/backend/internal/adapter/event"
	"github.com/liverty-music/backend/internal/entity"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func makeResendRequestedMsg(t *testing.T, data entity.UserEmailVerificationResendRequestedData) *message.Message {
	t.Helper()
	payload, err := json.Marshal(data)
	require.NoError(t, err)
	return message.NewMessage("test-id", payload)
}

func TestReissueEmailVerificationConsumer_Handle(t *testing.T) {
	t.Parallel()

	data := entity.UserEmailVerificationResendRequestedData{UserID: "user-1"}

	t.Run("runs ReissueEmailVerification for the announced User", func(t *testing.T) {
		t.Parallel()
		userUC := ucmocks.NewMockUserUseCase(t)
		consumer := event.NewReissueEmailVerificationConsumer(userUC, newTestLogger(t))
		userUC.EXPECT().ReissueEmailVerification(anyCtx, "user-1").Return(nil).Once()

		assert.NoError(t, consumer.Handle(makeResendRequestedMsg(t, data)))
	})

	t.Run("a failure is returned for redelivery", func(t *testing.T) {
		t.Parallel()
		userUC := ucmocks.NewMockUserUseCase(t)
		consumer := event.NewReissueEmailVerificationConsumer(userUC, newTestLogger(t))
		userUC.EXPECT().ReissueEmailVerification(anyCtx, "user-1").Return(errors.New("zitadel down")).Once()

		assert.Error(t, consumer.Handle(makeResendRequestedMsg(t, data)))
	})

	t.Run("a malformed message is an error", func(t *testing.T) {
		t.Parallel()
		consumer := event.NewReissueEmailVerificationConsumer(ucmocks.NewMockUserUseCase(t), newTestLogger(t))

		assert.Error(t, consumer.Handle(message.NewMessage("x", []byte("not json"))))
	})
}
