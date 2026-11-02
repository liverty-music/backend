package event_test

import (
	"fmt"
	"testing"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	"github.com/liverty-music/backend/internal/adapter/event"
	"github.com/liverty-music/backend/internal/usecase"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// anyCtx matches any context.Context argument.
var anyCtx = mock.MatchedBy(func(any) bool { return true })

func TestNotificationConsumer_Handle(t *testing.T) {
	t.Parallel()

	t.Run("returns error when notification fails", func(t *testing.T) {
		t.Parallel()

		pushUC := ucmocks.NewMockNotificationUseCase(t)
		handler := event.NewNotificationConsumer(pushUC, newTestLogger(t))

		data := usecase.ConcertCreatedData{
			ArtistID:   "artist-2",
			ConcertIDs: []string{"concert-3"},
			RequestID:  "announcement-1",
		}
		pushUC.EXPECT().NotifyNewConcerts(anyCtx, data).Return(fmt.Errorf("push service unavailable")).Once()

		list := &entityv1.ConcertList{Concerts: []*entityv1.Concert{{Event: &entityv1.Event{Id: &entityv1.EventId{Value: "concert-3"}}}}}
		err := handler.Handle(makeAnnouncementMsg(t, "announcement-1", "artist-2", list))
		assert.Error(t, err)
	})

	t.Run("returns error on invalid payload", func(t *testing.T) {
		t.Parallel()

		pushUC := ucmocks.NewMockNotificationUseCase(t)
		handler := event.NewNotificationConsumer(pushUC, newTestLogger(t))

		err := handler.Handle(malformedMsg())
		assert.Error(t, err)
	})
}
