package event_test

import (
	"fmt"
	"testing"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	"github.com/liverty-music/backend/internal/adapter/event"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/stretchr/testify/assert"
)

func TestFollowSearchConsumer_Handle(t *testing.T) {
	t.Parallel()

	// A failed search is returned (not swallowed) so the router retries and,
	// if retries are exhausted, poison-queues and logs it — the Follow use
	// case has already succeeded and persisted by the time this consumer runs.
	t.Run("returns error when use case fails", func(t *testing.T) {
		t.Parallel()

		concertUC := ucmocks.NewMockConcertUseCase(t)
		handler := event.NewFollowSearchConsumer(concertUC, newTestLogger(t))

		concertUC.EXPECT().SearchNewConcertsOnFirstFollow(anyCtx, "artist-2").Return(fmt.Errorf("gemini unavailable")).Once()

		follow := &entityv1.Follow{UserId: &entityv1.UserId{Value: "user-2"}, ArtistId: &entityv1.ArtistId{Value: "artist-2"}}
		err := handler.Handle(makeAnnouncementMsg(t, "announcement-1", "", follow))
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "search new concerts on first follow")
	})

	t.Run("returns error on invalid payload", func(t *testing.T) {
		t.Parallel()

		concertUC := ucmocks.NewMockConcertUseCase(t)
		handler := event.NewFollowSearchConsumer(concertUC, newTestLogger(t))

		err := handler.Handle(malformedMsg())
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "parse ARTIST.followed event")
	})
}
