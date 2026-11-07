package event_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
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

// orderPaid is what the receivers read from paidOrder: the event is the one
// of its TicketStock.
var orderPaid = entity.OrderPaidData{
	OrderID: "order-1", UserID: "fan-1", EventID: "event-1", TicketCount: 2,
	Amount: 6000, Currency: "JPY",
}

// paidOrder is the ORDER.paid announcement of orderPaid.
func paidOrder() *entityv1.Order {
	return &entityv1.Order{
		Id:            &entityv1.OrderId{Value: orderPaid.OrderID},
		UserId:        &entityv1.UserId{Value: orderPaid.UserID},
		TicketStockId: &entityv1.TicketStockId{Value: "stock-1"},
		TicketCount:   int32(orderPaid.TicketCount),
		Amount:        orderPaid.Amount,
		Currency:      orderPaid.Currency,
	}
}

func makeOrderPaidMsg(t *testing.T) *message.Message {
	t.Helper()
	return makeAnnouncementMsg(t, "announcement-1", "", paidOrder())
}

func TestOrderPaidConsumer(t *testing.T) {
	t.Parallel()

	t.Run("sends the confirmation of the announced Order with the event of its TicketStock", func(t *testing.T) {
		t.Parallel()
		notifications := ucmocks.NewMockNotificationUseCase(t)
		notifications.EXPECT().SendOrderConfirmation(anyCtx, orderPaid).Return(nil).Once()
		consumer := event.NewOrderPaidConsumer(notifications, ucmocks.NewMockTicketJourneyUseCase(t), stubStocks{}, newTestLogger(t))

		assert.NoError(t, consumer.HandleSendOrderConfirmation(makeOrderPaidMsg(t)))
	})

	t.Run("a failed confirmation is returned for redelivery", func(t *testing.T) {
		t.Parallel()
		notifications := ucmocks.NewMockNotificationUseCase(t)
		consumer := event.NewOrderPaidConsumer(notifications, ucmocks.NewMockTicketJourneyUseCase(t), stubStocks{}, newTestLogger(t))
		notifications.EXPECT().SendOrderConfirmation(anyCtx, orderPaid).Return(errors.New("postmark down"))

		assert.Error(t, consumer.HandleSendOrderConfirmation(makeOrderPaidMsg(t)))
	})

	t.Run("marks the fan's journey for the event of its TicketStock paid", func(t *testing.T) {
		t.Parallel()
		journeys := ucmocks.NewMockTicketJourneyUseCase(t)
		journeys.EXPECT().MarkPaid(anyCtx, orderPaid).Return(nil).Once()
		consumer := event.NewOrderPaidConsumer(ucmocks.NewMockNotificationUseCase(t), journeys, stubStocks{}, newTestLogger(t))

		assert.NoError(t, consumer.HandleMarkPaid(makeOrderPaidMsg(t)))
	})

	t.Run("an Order without a TicketStock is an error", func(t *testing.T) {
		t.Parallel()
		consumer := event.NewOrderPaidConsumer(ucmocks.NewMockNotificationUseCase(t), ucmocks.NewMockTicketJourneyUseCase(t), stubStocks{}, newTestLogger(t))
		order := paidOrder()
		order.TicketStockId = nil

		assert.Error(t, consumer.HandleMarkPaid(makeAnnouncementMsg(t, "announcement-1", "", order)))
	})

	t.Run("a malformed message is an error", func(t *testing.T) {
		t.Parallel()
		consumer := event.NewOrderPaidConsumer(ucmocks.NewMockNotificationUseCase(t), ucmocks.NewMockTicketJourneyUseCase(t), stubStocks{}, newTestLogger(t))

		assert.Error(t, consumer.HandleMarkPaid(malformedMsg()))
		assert.Error(t, consumer.HandleSendOrderConfirmation(malformedMsg()))
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
	order := &entity.Order{ID: "order-1", UserID: "fan-1", LotteryEntryID: "entry-1", Amount: 6000, Currency: "JPY", PayTime: new(time.Now())}
	orders := entitymocks.NewMockOrderRepository(t)
	orders.EXPECT().Get(mock.Anything, entity.OrderID("order-1")).RunAndReturn(func(context.Context, entity.OrderID) (*entity.Order, error) {
		mu.Lock()
		defer mu.Unlock()
		copied := *order
		return &copied, nil
	})
	emails := 0
	mailer := entitymocks.NewMockOrderMailer(t)
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
	workflows.EXPECT().Start(mock.Anything, usecase.WorkflowDeliverNotification, "deliver:lottery_win:order-1", mock.Anything).Return(nil).Twice()

	notificationUC := usecase.NewNotificationUseCase(entitymocks.NewMockNotificationRepository(t), entitymocks.NewMockPushSubscriptionRepository(t),
		entitymocks.NewMockPushNotificationSender(t), workflows, noopPushMetrics{}, logger, usecase.OrderConfirmationDeps{
			Orders: orders, Users: users, Concerts: stubConcertReader{}, EventOrganizer: stubEventOrganizer{},
			Organizers: organizers, Mailer: mailer, TimeZone: time.UTC,
		}, usecase.NewConcertsDeps{}, usecase.SubjectDeps{})
	journeys := entitymocks.NewMockTicketJourneyRepository(t)
	paid := &entity.TicketJourney{UserID: "fan-1", EventID: "event-1", Status: entity.TicketJourneyStatusPaid, StatusSetBy: entity.TicketJourneySetByPlatform}
	journeys.EXPECT().Upsert(mock.Anything, paid).Return(nil).Twice()
	// fan-1 has no home (the users mock above), so MarkPaid raises no hype.
	journeyUC := usecase.NewTicketJourneyUseCase(journeys, users, entitymocks.NewMockConcertRepository(t),
		entitymocks.NewMockFollowRepository(t), entitymocks.NewMockLotteryEntryRepository(t), logger)
	consumer := event.NewOrderPaidConsumer(notificationUC, journeyUC, stubStocks{}, logger)

	for range 2 {
		require.NoError(t, consumer.HandleSendOrderConfirmation(makeOrderPaidMsg(t)))
		require.NoError(t, consumer.HandleMarkPaid(makeOrderPaidMsg(t)))
	}

	assert.Equal(t, 1, emails, "a redelivery produces one email")
}

// noopPushMetrics discards push metrics.
type noopPushMetrics struct{}

func (noopPushMetrics) RecordPushSend(context.Context, string)                {}
func (noopPushMetrics) RecordDeliveryOutcome(context.Context, string, string) {}

// stubStocks returns every TicketStock as one of event-1.
type stubStocks struct{}

func (stubStocks) Get(_ context.Context, id entity.TicketStockID, _ time.Time) (*entity.TicketStock, error) {
	return &entity.TicketStock{ID: id, EventID: "event-1"}, nil
}
