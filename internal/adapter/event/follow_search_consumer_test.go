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

func makeArtistFollowedMsg(t *testing.T, data entity.ArtistFollowedData) *message.Message {
	t.Helper()
	payload, err := json.Marshal(data)
	require.NoError(t, err)
	return message.NewMessage("test-id", payload)
}

func TestFollowSearchConsumer_Handle(t *testing.T) {
	t.Parallel()

	// @spec components/usecase/concert/search-new-concerts-on-first-follow "Follow announced"
	t.Run("delegates to SearchNewConcertsOnFirstFollow", func(t *testing.T) {
		t.Parallel()

		concertUC := ucmocks.NewMockConcertUseCase(t)
		handler := event.NewFollowSearchConsumer(concertUC, newTestLogger(t))

		concertUC.EXPECT().SearchNewConcertsOnFirstFollow(anyCtx, "artist-1").Return(nil).Once()

		msg := makeArtistFollowedMsg(t, entity.ArtistFollowedData{
			UserID:   "user-1",
			ArtistID: "artist-1",
		})

		err := handler.Handle(msg)
		assert.NoError(t, err)
	})

	// A failed search is returned (not swallowed) so the router retries and,
	// if retries are exhausted, poison-queues and logs it — the Follow use
	// case has already succeeded and persisted by the time this consumer runs.
	t.Run("returns error when use case fails", func(t *testing.T) {
		t.Parallel()

		concertUC := ucmocks.NewMockConcertUseCase(t)
		handler := event.NewFollowSearchConsumer(concertUC, newTestLogger(t))

		concertUC.EXPECT().SearchNewConcertsOnFirstFollow(anyCtx, "artist-2").Return(fmt.Errorf("gemini unavailable")).Once()

		msg := makeArtistFollowedMsg(t, entity.ArtistFollowedData{
			UserID:   "user-2",
			ArtistID: "artist-2",
		})

		err := handler.Handle(msg)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "search new concerts on first follow")
	})

	t.Run("returns error on invalid payload", func(t *testing.T) {
		t.Parallel()

		concertUC := ucmocks.NewMockConcertUseCase(t)
		handler := event.NewFollowSearchConsumer(concertUC, newTestLogger(t))

		msg := message.NewMessage("bad-id", []byte("not json"))
		err := handler.Handle(msg)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "parse ARTIST.followed event")
	})
}
