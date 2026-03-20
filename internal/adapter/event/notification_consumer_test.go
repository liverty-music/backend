package event_test

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/liverty-music/backend/internal/adapter/event"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/entity/mocks"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// anyCtx matches any context.Context argument.
var anyCtx = mock.MatchedBy(func(interface{}) bool { return true })

func makeCreatedMsg(t *testing.T, data entity.ConcertCreatedData) *message.Message {
	t.Helper()
	payload, err := json.Marshal(data)
	require.NoError(t, err)
	return message.NewMessage("test-id", payload)
}

func TestNotificationConsumer_Handle(t *testing.T) {
	t.Parallel()

	t.Run("sends notifications on concert.created event", func(t *testing.T) {
		t.Parallel()

		artistRepo := mocks.NewMockArtistRepository(t)
		concertRepo := mocks.NewMockConcertRepository(t)
		pushUC := ucmocks.NewMockPushNotificationUseCase(t)
		handler := event.NewNotificationConsumer(artistRepo, concertRepo, pushUC, newTestLogger(t))

		artist := &entity.Artist{ID: "artist-1", Name: "Test Artist"}
		concerts := []*entity.Concert{{Event: entity.Event{ID: "concert-1"}}}

		artistRepo.EXPECT().Get(anyCtx, "artist-1").Return(artist, nil).Once()
		concertRepo.EXPECT().ListByArtist(anyCtx, "artist-1", true).Return(concerts, nil).Once()
		pushUC.EXPECT().NotifyNewConcerts(anyCtx, artist, concerts).Return(nil).Once()

		data := entity.ConcertCreatedData{
			ArtistID:     "artist-1",
			ArtistName:   "Test Artist",
			ConcertCount: 3,
		}

		msg := makeCreatedMsg(t, data)
		err := handler.Handle(msg)
		assert.NoError(t, err)
	})

	t.Run("returns error when artist not found", func(t *testing.T) {
		t.Parallel()

		artistRepo := mocks.NewMockArtistRepository(t)
		concertRepo := mocks.NewMockConcertRepository(t)
		pushUC := ucmocks.NewMockPushNotificationUseCase(t)
		handler := event.NewNotificationConsumer(artistRepo, concertRepo, pushUC, newTestLogger(t))

		artistRepo.EXPECT().Get(anyCtx, "nonexistent").Return(nil, apperr.ErrNotFound).Once()

		data := entity.ConcertCreatedData{
			ArtistID:     "nonexistent",
			ArtistName:   "Unknown",
			ConcertCount: 1,
		}

		msg := makeCreatedMsg(t, data)
		err := handler.Handle(msg)
		assert.Error(t, err)
	})

	t.Run("returns error when notification fails", func(t *testing.T) {
		t.Parallel()

		artistRepo := mocks.NewMockArtistRepository(t)
		concertRepo := mocks.NewMockConcertRepository(t)
		pushUC := ucmocks.NewMockPushNotificationUseCase(t)
		handler := event.NewNotificationConsumer(artistRepo, concertRepo, pushUC, newTestLogger(t))

		artist := &entity.Artist{ID: "artist-2", Name: "Another Artist"}
		concerts := []*entity.Concert{}

		artistRepo.EXPECT().Get(anyCtx, "artist-2").Return(artist, nil).Once()
		concertRepo.EXPECT().ListByArtist(anyCtx, "artist-2", true).Return(concerts, nil).Once()
		pushUC.EXPECT().NotifyNewConcerts(anyCtx, artist, concerts).Return(fmt.Errorf("push service unavailable")).Once()

		data := entity.ConcertCreatedData{
			ArtistID:     "artist-2",
			ArtistName:   "Another Artist",
			ConcertCount: 1,
		}

		msg := makeCreatedMsg(t, data)
		err := handler.Handle(msg)
		assert.Error(t, err)
	})

	t.Run("returns error on invalid payload", func(t *testing.T) {
		t.Parallel()

		artistRepo := mocks.NewMockArtistRepository(t)
		concertRepo := mocks.NewMockConcertRepository(t)
		pushUC := ucmocks.NewMockPushNotificationUseCase(t)
		handler := event.NewNotificationConsumer(artistRepo, concertRepo, pushUC, newTestLogger(t))

		msg := message.NewMessage("bad-id", []byte("not json"))
		err := handler.Handle(msg)
		assert.Error(t, err)
	})
}
