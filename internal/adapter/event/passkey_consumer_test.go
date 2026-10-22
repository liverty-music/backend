package event_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/liverty-music/backend/internal/adapter/event"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/messaging"
	"github.com/liverty-music/backend/internal/usecase"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var passkeyChanged = entity.UserPasskeyChangedData{
	ProviderUserID:    "zitadel-fan-1",
	ProviderPasskeyID: "token-1",
	Kind:              "added",
	Name:              "Apple iPhone, iOS, Mobile Safari",
	AAGUID:            "ea9b8d66-4d01-1d21-3ce4-b6b48cb575d4",
	ChangeTime:        time.Date(2026, 10, 22, 3, 4, 5, 0, time.UTC),
}

func makeJSONMsg(t *testing.T, data any) *message.Message {
	t.Helper()
	payload, err := json.Marshal(data)
	require.NoError(t, err)
	return message.NewMessage("msg-1", payload)
}

func newPasskeyConsumer(t *testing.T) (*event.PasskeyConsumer, *ucmocks.MockUserUseCase, *ucmocks.MockPasskeyUseCase) {
	t.Helper()
	users := ucmocks.NewMockUserUseCase(t)
	passkeys := ucmocks.NewMockPasskeyUseCase(t)
	return event.NewPasskeyConsumer(users, passkeys, newTestLogger(t)), users, passkeys
}

func TestPasskeyConsumer_HandleRecoveryRequested(t *testing.T) {
	t.Parallel()

	t.Run("sends the recovery link of the announced user", func(t *testing.T) {
		t.Parallel()
		consumer, users, _ := newPasskeyConsumer(t)
		users.EXPECT().SendPasskeyRecoveryLink(anyCtx, "user-1").Return(nil).Once()

		assert.NoError(t, consumer.HandleRecoveryRequested(makeJSONMsg(t, entity.UserPasskeyRecoveryRequestedData{UserID: "user-1"})))
	})

	t.Run("returns a failed send for a retry", func(t *testing.T) {
		t.Parallel()
		consumer, users, _ := newPasskeyConsumer(t)
		users.EXPECT().SendPasskeyRecoveryLink(anyCtx, "user-1").Return(errors.New("postmark down")).Once()

		assert.Error(t, consumer.HandleRecoveryRequested(makeJSONMsg(t, entity.UserPasskeyRecoveryRequestedData{UserID: "user-1"})))
	})

	t.Run("rejects a malformed payload", func(t *testing.T) {
		t.Parallel()
		consumer, _, _ := newPasskeyConsumer(t)

		assert.Error(t, consumer.HandleRecoveryRequested(message.NewMessage("x", []byte("{"))))
	})
}

func TestPasskeyConsumer_HandlePasskeyChanged(t *testing.T) {
	t.Parallel()

	t.Run("emails the change with its kind, name and time", func(t *testing.T) {
		t.Parallel()
		consumer, users, _ := newPasskeyConsumer(t)
		users.EXPECT().NotifyPasskeyChange(anyCtx, "zitadel-fan-1", entity.PasskeyChange{
			Kind: entity.PasskeyChangeAdded, Name: "Apple iPhone, iOS, Mobile Safari", ChangeTime: passkeyChanged.ChangeTime,
		}).Return(nil).Once()

		assert.NoError(t, consumer.HandlePasskeyChanged(makeJSONMsg(t, passkeyChanged)))
	})

	t.Run("returns a failed email for a retry", func(t *testing.T) {
		t.Parallel()
		consumer, users, _ := newPasskeyConsumer(t)
		users.EXPECT().NotifyPasskeyChange(anyCtx, "zitadel-fan-1", anyCtx).Return(errors.New("postmark down")).Once()

		assert.Error(t, consumer.HandlePasskeyChanged(makeJSONMsg(t, passkeyChanged)))
	})
}

func TestPasskeyConsumer_HandleRecordPasskeyChange(t *testing.T) {
	t.Parallel()

	t.Run("records the change", func(t *testing.T) {
		t.Parallel()
		consumer, _, passkeys := newPasskeyConsumer(t)
		passkeys.EXPECT().RecordChange(anyCtx, passkeyChanged).Return(nil).Once()

		assert.NoError(t, consumer.HandleRecordPasskeyChange(makeJSONMsg(t, passkeyChanged)))
	})

	t.Run("asks for a delayed redelivery while the identity has no user yet", func(t *testing.T) {
		t.Parallel()
		consumer, _, passkeys := newPasskeyConsumer(t)
		passkeys.EXPECT().RecordChange(anyCtx, passkeyChanged).
			Return(fmt.Errorf("%w: not found", usecase.ErrPasskeyAccountPending)).Once()

		err := consumer.HandleRecordPasskeyChange(makeJSONMsg(t, passkeyChanged))

		assert.True(t, messaging.IsRedeliverLater(err))
	})

	t.Run("returns any other failure for the usual retry", func(t *testing.T) {
		t.Parallel()
		consumer, _, passkeys := newPasskeyConsumer(t)
		passkeys.EXPECT().RecordChange(anyCtx, passkeyChanged).Return(errors.New("db down")).Once()

		err := consumer.HandleRecordPasskeyChange(makeJSONMsg(t, passkeyChanged))

		require.Error(t, err)
		assert.False(t, messaging.IsRedeliverLater(err))
	})
}
