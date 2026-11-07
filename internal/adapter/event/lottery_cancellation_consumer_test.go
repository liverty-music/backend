package event_test

import (
	"errors"
	"testing"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	"github.com/liverty-music/backend/internal/adapter/event"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLotteryCancellationConsumer(t *testing.T) {
	t.Parallel()

	cancelled := func(id string) *entityv1.Event { return &entityv1.Event{Id: &entityv1.EventId{Value: id}} }

	t.Run("an announced event reaches the usecase", func(t *testing.T) {
		t.Parallel()
		lottery := ucmocks.NewMockLotteryEntryUseCase(t)
		lottery.EXPECT().EndForCancelledEvent(anyCtx, "event-1").Return(nil).Once()
		consumer := event.NewLotteryCancellationConsumer(lottery, newTestLogger(t))

		require.NoError(t, consumer.HandleEventCancelled(makeAnnouncementMsg(t, "announcement-1", "", cancelled("event-1"))))
	})

	t.Run("a payload with no id is refused", func(t *testing.T) {
		t.Parallel()
		consumer := event.NewLotteryCancellationConsumer(ucmocks.NewMockLotteryEntryUseCase(t), newTestLogger(t))

		assert.Error(t, consumer.HandleEventCancelled(makeAnnouncementMsg(t, "announcement-2", "", &entityv1.Event{})))
		assert.Error(t, consumer.HandleEventCancelled(malformedMsg()))
	})

	t.Run("a usecase failure is returned for redelivery", func(t *testing.T) {
		t.Parallel()
		lottery := ucmocks.NewMockLotteryEntryUseCase(t)
		lottery.EXPECT().EndForCancelledEvent(anyCtx, "event-1").Return(errors.New("database down")).Once()
		consumer := event.NewLotteryCancellationConsumer(lottery, newTestLogger(t))

		err := consumer.HandleEventCancelled(makeAnnouncementMsg(t, "announcement-3", "", cancelled("event-1")))

		assert.Error(t, err)
	})
}
