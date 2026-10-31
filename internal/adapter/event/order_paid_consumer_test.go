package event_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/liverty-music/backend/internal/adapter/event"
	"github.com/liverty-music/backend/internal/entity"
	entitymocks "github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/usecase"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

var orderPaid = entity.OrderPaidData{
	OrderID: "order-1", UserID: "fan-1", EventID: "event-1", TicketCount: 2,
	Amount: 6000, Currency: "JPY", CheckoutID: "checkout-1",
}

func makeOrderPaidMsg(t *testing.T, data entity.OrderPaidData) *message.Message {
	t.Helper()
	payload, err := json.Marshal(data)
	require.NoError(t, err)
	return message.NewMessage(data.OrderID, payload)
}

func TestOrderPaidConsumer(t *testing.T) {
	t.Parallel()

	t.Run("sends the order confirmation", func(t *testing.T) {
		t.Parallel()
		notifications := ucmocks.NewMockNotificationUseCase(t)
		consumer := event.NewOrderPaidConsumer(notifications, ucmocks.NewMockTicketJourneyUseCase(t), newTestLogger(t))
		notifications.EXPECT().SendOrderConfirmation(anyCtx, orderPaid).Return(nil).Once()

		assert.NoError(t, consumer.HandleSendOrderConfirmation(makeOrderPaidMsg(t, orderPaid)))
	})

	t.Run("a failed confirmation is returned for redelivery", func(t *testing.T) {
		t.Parallel()
		notifications := ucmocks.NewMockNotificationUseCase(t)
		consumer := event.NewOrderPaidConsumer(notifications, ucmocks.NewMockTicketJourneyUseCase(t), newTestLogger(t))
		notifications.EXPECT().SendOrderConfirmation(anyCtx, orderPaid).Return(errors.New("postmark down"))

		assert.Error(t, consumer.HandleSendOrderConfirmation(makeOrderPaidMsg(t, orderPaid)))
	})

	t.Run("marks the ticket journey paid", func(t *testing.T) {
		t.Parallel()
		journeys := ucmocks.NewMockTicketJourneyUseCase(t)
		consumer := event.NewOrderPaidConsumer(ucmocks.NewMockNotificationUseCase(t), journeys, newTestLogger(t))
		journeys.EXPECT().MarkPaid(anyCtx, orderPaid).Return(nil).Once()

		assert.NoError(t, consumer.HandleMarkPaid(makeOrderPaidMsg(t, orderPaid)))
	})

	t.Run("a malformed message is an error", func(t *testing.T) {
		t.Parallel()
		consumer := event.NewOrderPaidConsumer(ucmocks.NewMockNotificationUseCase(t), ucmocks.NewMockTicketJourneyUseCase(t), newTestLogger(t))

		assert.Error(t, consumer.HandleMarkPaid(message.NewMessage("x", []byte("not json"))))
		assert.Error(t, consumer.HandleSendOrderConfirmation(message.NewMessage("x", []byte("not json"))))
	})
}

// stubConcertReader returns one concert for any event.
type stubConcertReader struct{}

func (stubConcertReader) ListByIDs(context.Context, []string) ([]*entity.Concert, error) {
	return []*entity.Concert{{ID: "event-1", LocalDate: time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC), Series: &entity.Series{Title: "Live"}}}, nil
}

// stubEventOrganizer resolves every event to org-1.
type stubEventOrganizer struct{}

func (stubEventOrganizer) GetOrganizerID(context.Context, string) (string, error) {
	return "org-1", nil
}

// TestOrderPaidConsumer_Redelivery delivers the same ORDER.paid twice through
// both durables with the real use cases. The order store keeps the
// confirmation-sent time, and the sender sends only while it is unset (as
// the Postmark sender does, contract-tested in the mail package), so one
// email goes out; the journey upsert is idempotent and stays Paid.
func TestOrderPaidConsumer_Redelivery(t *testing.T) {
	t.Parallel()
	logger := newTestLogger(t)

	var mu sync.Mutex
	order := &entity.Order{ID: "order-1", UserID: "fan-1", Amount: 6000, Currency: "JPY", PayTime: new(time.Now())}
	orders := entitymocks.NewMockOrderRepository(t)
	orders.EXPECT().Get(mock.Anything, entity.OrderID("order-1")).RunAndReturn(func(context.Context, entity.OrderID) (*entity.Order, error) {
		mu.Lock()
		defer mu.Unlock()
		copied := *order
		return &copied, nil
	})
	emails := 0
	mailer := entitymocks.NewMockOrderConfirmationSender(t)
	mailer.EXPECT().SendConfirmationEmail(mock.Anything, entity.OrderID("order-1"), "fan@example.com", mock.Anything).RunAndReturn(
		func(context.Context, entity.OrderID, string, entity.OrderConfirmationEmail) error {
			mu.Lock()
			defer mu.Unlock()
			if order.ConfirmationSendTime == nil {
				emails++
				sent := time.Now()
				order.ConfirmationSendTime = &sent
			}
			return nil
		})
	users := entitymocks.NewMockUserRepository(t)
	users.EXPECT().Get(mock.Anything, "fan-1").Return(&entity.User{ID: "fan-1", Email: "fan@example.com"}, nil)
	organizers := entitymocks.NewMockOrganizerRepository(t)
	organizers.EXPECT().Get(mock.Anything, "org-1").Return(&entity.Organizer{ID: "org-1"}, nil)
	// The push is started under the Order's name both times, so the
	// runtime pushes once.
	workflows := ucmocks.NewMockWorkflows(t)
	workflows.EXPECT().Start(mock.Anything, usecase.WorkflowDeliverNotification, "deliver:order_confirmation:order-1", mock.Anything).Return(nil).Twice()

	notificationUC := usecase.NewNotificationUseCase(entitymocks.NewMockNotificationRepository(t), entitymocks.NewMockPushSubscriptionRepository(t),
		entitymocks.NewMockPushNotificationSender(t), workflows, noopPushMetrics{}, logger, usecase.OrderConfirmationDeps{
			Orders: orders, Users: users, Concerts: stubConcertReader{}, EventOrganizer: stubEventOrganizer{},
			Organizers: organizers, Mailer: mailer, TimeZone: time.UTC,
		}, usecase.NewConcertsDeps{})
	journeys := entitymocks.NewMockTicketJourneyRepository(t)
	paid := &entity.TicketJourney{UserID: "fan-1", EventID: "event-1", Status: entity.TicketJourneyStatusPaid}
	journeys.EXPECT().Upsert(mock.Anything, paid).Return(nil).Twice()
	journeyUC := usecase.NewTicketJourneyUseCase(journeys, logger)
	consumer := event.NewOrderPaidConsumer(notificationUC, journeyUC, logger)

	for range 2 {
		require.NoError(t, consumer.HandleSendOrderConfirmation(makeOrderPaidMsg(t, orderPaid)))
		require.NoError(t, consumer.HandleMarkPaid(makeOrderPaidMsg(t, orderPaid)))
	}

	assert.Equal(t, 1, emails, "a redelivery produces one email")
}

// noopPushMetrics discards push metrics.
type noopPushMetrics struct{}

func (noopPushMetrics) RecordPushSend(context.Context, string)                {}
func (noopPushMetrics) RecordDeliveryOutcome(context.Context, string, string) {}
