package event_test

import (
	"errors"
	"testing"
	"time"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/liverty-music/backend/internal/adapter/event"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/messaging"
	"github.com/liverty-music/backend/internal/usecase"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var (
	passkeyCreateTime = time.Date(2026, 10, 22, 3, 4, 5, 0, time.UTC)
	passkeyRemoveTime = time.Date(2026, 10, 23, 8, 0, 0, 0, time.UTC)
)

// makePasskeyMsg builds a Passkey announcement as the outbox records it.
func makePasskeyMsg(t *testing.T, id string, passkey *entityv1.Passkey, announced time.Time) *message.Message {
	t.Helper()
	payload, err := proto.Marshal(passkey)
	require.NoError(t, err)
	msg := message.NewMessage(id, payload)
	msg.Metadata.Set("ce_id", id)
	msg.Metadata.Set("ce_time", announced.Format(time.RFC3339Nano))
	msg.Metadata.Set("ce_datacontenttype", messaging.ContentTypeProtobuf)
	return msg
}

func fanPasskey() *entityv1.Passkey {
	return &entityv1.Passkey{
		Id:         &entityv1.PasskeyId{Value: "token-1"},
		UserId:     &entityv1.UserId{Value: "fan-1"},
		Name:       &entityv1.PasskeyName{Value: "Google Pixel 9, Android, Chrome"},
		CreateTime: timestamppb.New(passkeyCreateTime),
	}
}

func TestPasskeyConsumer(t *testing.T) {
	t.Parallel()

	t.Run("starts the added email with the Passkey's name and create time, named after the announcement", func(t *testing.T) {
		t.Parallel()
		users := ucmocks.NewMockUserUseCase(t)
		users.EXPECT().StartNotifyPasskeyChange(anyCtx, "announcement-1", usecase.PasskeyChangeNotice{
			UserID: "fan-1",
			Change: entity.PasskeyChange{Kind: entity.PasskeyChangeAdded, Name: "Google Pixel 9, Android, Chrome", ChangeTime: passkeyCreateTime},
		}).Return(nil).Once()

		consumer := event.NewPasskeyConsumer(users, newTestLogger(t))

		assert.NoError(t, consumer.HandlePasskeyAdded(makePasskeyMsg(t, "announcement-1", fanPasskey(), passkeyCreateTime.Add(time.Second))))
	})

	t.Run("starts the removed email with the time the removal was announced", func(t *testing.T) {
		t.Parallel()
		users := ucmocks.NewMockUserUseCase(t)
		users.EXPECT().StartNotifyPasskeyChange(anyCtx, "announcement-2", usecase.PasskeyChangeNotice{
			UserID: "fan-1",
			Change: entity.PasskeyChange{Kind: entity.PasskeyChangeRemoved, ChangeTime: passkeyRemoveTime},
		}).Return(nil).Once()

		consumer := event.NewPasskeyConsumer(users, newTestLogger(t))

		assert.NoError(t, consumer.HandlePasskeyRemoved(makePasskeyMsg(t, "announcement-2", fanPasskey(), passkeyRemoveTime)))
	})

	t.Run("returns a failed start for a retry", func(t *testing.T) {
		t.Parallel()
		users := ucmocks.NewMockUserUseCase(t)
		users.EXPECT().StartNotifyPasskeyChange(anyCtx, "announcement-1", anyCtx).Return(errors.New("database down")).Once()

		consumer := event.NewPasskeyConsumer(users, newTestLogger(t))

		assert.Error(t, consumer.HandlePasskeyAdded(makePasskeyMsg(t, "announcement-1", fanPasskey(), passkeyCreateTime)))
	})

	t.Run("rejects a Passkey without a User", func(t *testing.T) {
		t.Parallel()
		consumer := event.NewPasskeyConsumer(ucmocks.NewMockUserUseCase(t), newTestLogger(t))
		passkey := fanPasskey()
		passkey.UserId = nil

		assert.Error(t, consumer.HandlePasskeyAdded(makePasskeyMsg(t, "announcement-1", passkey, passkeyCreateTime)))
	})
}
