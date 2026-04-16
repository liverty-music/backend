package event_test

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/liverty-music/backend/internal/adapter/event"
	"github.com/liverty-music/backend/internal/usecase"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// anyCtx matches any context.Context argument.
var anyCtx = mock.MatchedBy(func(any) bool { return true })

func makeCreatedMsg(t *testing.T, data usecase.ConcertCreatedData) *message.Message {
	t.Helper()
	payload, err := json.Marshal(data)
	require.NoError(t, err)
	return message.NewMessage("test-id", payload)
}

func TestNotificationConsumer_Handle(t *testing.T) {
	t.Parallel()

	t.Run("sends notifications on concert.created event", func(t *testing.T) {
		t.Parallel()

		pushUC := ucmocks.NewMockPushNotificationUseCase(t)
		handler := event.NewNotificationConsumer(pushUC, newTestLogger(t))

		data := usecase.ConcertCreatedData{
			ArtistID:   "artist-1",
			ConcertIDs: []string{"concert-1", "concert-2"},
		}
		pushUC.EXPECT().NotifyNewConcerts(anyCtx, data).Return(nil).Once()

		msg := makeCreatedMsg(t, data)
		err := handler.Handle(msg)
		assert.NoError(t, err)
	})

	t.Run("returns error when notification fails", func(t *testing.T) {
		t.Parallel()

		pushUC := ucmocks.NewMockPushNotificationUseCase(t)
		handler := event.NewNotificationConsumer(pushUC, newTestLogger(t))

		data := usecase.ConcertCreatedData{
			ArtistID:   "artist-2",
			ConcertIDs: []string{"concert-3"},
		}
		pushUC.EXPECT().NotifyNewConcerts(anyCtx, data).Return(fmt.Errorf("push service unavailable")).Once()

		msg := makeCreatedMsg(t, data)
		err := handler.Handle(msg)
		assert.Error(t, err)
	})

	t.Run("returns error on invalid payload", func(t *testing.T) {
		t.Parallel()

		pushUC := ucmocks.NewMockPushNotificationUseCase(t)
		handler := event.NewNotificationConsumer(pushUC, newTestLogger(t))

		msg := message.NewMessage("bad-id", []byte("not json"))
		err := handler.Handle(msg)
		assert.Error(t, err)
	})
}
