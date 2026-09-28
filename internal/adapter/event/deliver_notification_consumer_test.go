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

func makeNotificationRequestedMsg(t *testing.T, data entity.NotificationRequestedData) *message.Message {
	t.Helper()
	payload, err := json.Marshal(data)
	require.NoError(t, err)
	return message.NewMessage("test-id", payload)
}

func TestDeliverNotificationConsumer_Handle(t *testing.T) {
	t.Parallel()

	validData := entity.NotificationRequestedData{
		UserID:  "user-1",
		Type:    entity.NotificationTypeNewConcerts,
		Payload: entity.NewNotificationPayload("Artist", "1 new concert found", "/concerts/c1", "concert-artist-1"),
	}

	t.Run("delegates to NotificationUseCase.Deliver", func(t *testing.T) {
		t.Parallel()

		notificationUC := ucmocks.NewMockNotificationUseCase(t)
		handler := event.NewDeliverNotificationConsumer(notificationUC, newTestLogger(t))

		notificationUC.EXPECT().
			Deliver(anyCtx, validData.UserID, validData.Type, validData.Payload).
			Return(&entity.Notification{ID: "notif-1", DeliveryStatus: entity.NotificationDeliveryStatusDelivered}, nil).
			Once()

		err := handler.Handle(makeNotificationRequestedMsg(t, validData))
		assert.NoError(t, err)
	})

	// A Notification record-creation failure inside Deliver is returned so the
	// router retries (and, if retries are exhausted, poison-queues and logs
	// it) — a failed push send is instead an outcome Deliver already recorded,
	// never an error, so it never reaches this path.
	t.Run("returns error when Deliver fails", func(t *testing.T) {
		t.Parallel()

		notificationUC := ucmocks.NewMockNotificationUseCase(t)
		handler := event.NewDeliverNotificationConsumer(notificationUC, newTestLogger(t))

		notificationUC.EXPECT().
			Deliver(anyCtx, validData.UserID, validData.Type, validData.Payload).
			Return(nil, fmt.Errorf("failed to create notification record: db down")).
			Once()

		err := handler.Handle(makeNotificationRequestedMsg(t, validData))
		assert.Error(t, err)
	})

	t.Run("returns error on invalid payload", func(t *testing.T) {
		t.Parallel()

		notificationUC := ucmocks.NewMockNotificationUseCase(t)
		handler := event.NewDeliverNotificationConsumer(notificationUC, newTestLogger(t))

		msg := message.NewMessage("bad-id", []byte("not json"))
		err := handler.Handle(msg)
		assert.Error(t, err)
	})
}
