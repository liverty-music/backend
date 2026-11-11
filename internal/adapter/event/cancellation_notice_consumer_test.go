package event_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	"github.com/liverty-music/backend/internal/adapter/event"
	"github.com/liverty-music/backend/internal/entity"
	entitymocks "github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/usecase"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// startedOnce records each Start by workflow id. Workflows.Start starts an
// id once, so a repeated delivery starts nothing new exactly when it asks for
// the same ids as the first.
type startedOnce struct {
	ucmocks.MockWorkflows
	mu       sync.Mutex
	requests map[string]int
}

func (w *startedOnce) Start(_ context.Context, _, id string, _ any, _ ...usecase.StartOption) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.requests == nil {
		w.requests = map[string]int{}
	}
	w.requests[id]++
	return nil
}

// newNoticeConsumer wires the consumer to a NotificationUseCase that lists
// the Paid Orders order-a and order-b of event-1 and starts its workflows on
// workflows.
func newNoticeConsumer(t *testing.T, workflows usecase.Workflows) *event.CancellationNoticeConsumer {
	t.Helper()
	orders := entitymocks.NewMockOrderRepository(t)
	orders.EXPECT().ListByEvent(mock.Anything, "event-1").Return([]*entity.Order{
		{ID: "order-a", Status: entity.OrderStatusPaid},
		{ID: "order-b", Status: entity.OrderStatusPaid},
	}, nil).Maybe()
	notifications := usecase.NewNotificationUseCase(entitymocks.NewMockNotificationRepository(t), entitymocks.NewMockPushSubscriptionRepository(t),
		entitymocks.NewMockPushNotificationSender(t), workflows, anyPushMetrics(t), newTestLogger(t),
		usecase.OrderConfirmationDeps{Now: time.Now, Orders: orders}, usecase.NewConcertsDeps{}, usecase.SubjectDeps{Now: time.Now})
	return event.NewCancellationNoticeConsumer(notifications, newTestLogger(t))
}

func TestCancellationNoticeConsumer(t *testing.T) {
	t.Parallel()

	t.Run("a repeated cancellation starts each order's notice once", func(t *testing.T) {
		t.Parallel()
		workflows := &startedOnce{}
		consumer := newNoticeConsumer(t, workflows)
		msg := func() *entityv1.Event { return &entityv1.Event{Id: &entityv1.EventId{Value: "event-1"}} }

		require.NoError(t, consumer.HandleEventCancelled(makeAnnouncementMsg(t, "announcement-1", "", msg())))
		require.NoError(t, consumer.HandleEventCancelled(makeAnnouncementMsg(t, "announcement-1", "", msg())))

		// Both deliveries name the same two workflows, so each runs once.
		assert.Equal(t, map[string]int{
			"notify-event-cancelled:order-a": 2,
			"notify-event-cancelled:order-b": 2,
		}, workflows.requests)
	})

	t.Run("a repeated refund starts the order's notice once", func(t *testing.T) {
		t.Parallel()
		workflows := &startedOnce{}
		consumer := newNoticeConsumer(t, workflows)
		msg := func() *entityv1.Order { return &entityv1.Order{Id: &entityv1.OrderId{Value: "order-a"}} }

		require.NoError(t, consumer.HandleOrderRefunded(makeAnnouncementMsg(t, "announcement-2", "", msg())))
		require.NoError(t, consumer.HandleOrderRefunded(makeAnnouncementMsg(t, "announcement-2", "", msg())))

		// Both deliveries name the same workflow, so it runs once.
		assert.Equal(t, map[string]int{"notify-order-refunded:order-a": 2}, workflows.requests)
	})

	t.Run("a failure to start is returned for redelivery", func(t *testing.T) {
		t.Parallel()
		notifications := ucmocks.NewMockNotificationUseCase(t)
		notifications.EXPECT().SendOrderRefunded(anyCtx, entity.OrderID("order-a")).Return(errors.New("database down"))
		consumer := event.NewCancellationNoticeConsumer(notifications, newTestLogger(t))

		err := consumer.HandleOrderRefunded(makeAnnouncementMsg(t, "announcement-3", "", &entityv1.Order{Id: &entityv1.OrderId{Value: "order-a"}}))

		assert.Error(t, err)
	})

	t.Run("a malformed payload is rejected", func(t *testing.T) {
		t.Parallel()
		consumer := event.NewCancellationNoticeConsumer(ucmocks.NewMockNotificationUseCase(t), newTestLogger(t))

		assert.Error(t, consumer.HandleEventCancelled(malformedMsg()))
		assert.Error(t, consumer.HandleOrderRefunded(malformedMsg()))
	})
}
