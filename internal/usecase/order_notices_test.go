package usecase_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	entitymocks "github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/usecase"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// noticeFlow runs a notice workflow's steps at once, like inlineFlow, and
// records each workflow it starts with its input.
type noticeFlow struct {
	inlineFlow
	inputs map[string]any
}

func (f *noticeFlow) Start(ctx context.Context, workflow, id string, input any, opts ...usecase.StartOption) error {
	if f.inputs == nil {
		f.inputs = map[string]any{}
	}
	f.inputs[id] = input
	return f.inlineFlow.Start(ctx, workflow, id, input, opts...)
}

// onceWorkflows records the order notice workflows a use case starts, once per
// id, as Workflows.Start promises: a start of an existing id starts nothing.
type onceWorkflows struct {
	ucmocks.MockWorkflows
	mu      sync.Mutex
	started []usecase.OrderNotice
	ids     map[string]bool
}

func (w *onceWorkflows) Start(_ context.Context, _, id string, input any, _ ...usecase.StartOption) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.ids == nil {
		w.ids = map[string]bool{}
	}
	if w.ids[id] {
		return nil
	}
	w.ids[id] = true
	w.started = append(w.started, input.(usecase.OrderNotice))
	return nil
}

// noticeFixture holds the mocks of the order notices under test: the Orders
// of event-1 and their fans, and the email they receive.
type noticeFixture struct {
	orders    *entitymocks.MockOrderRepository
	users     *entitymocks.MockUserRepository
	tickets   *entitymocks.MockTicketRepository
	mailer    *entitymocks.MockOrderMailer
	workflows *onceWorkflows
	uc        usecase.NotificationUseCase

	mu     sync.Mutex
	emails map[entity.OrderID][]entity.OrderEmail
}

func newNoticeFixture(t *testing.T) *noticeFixture {
	t.Helper()
	f := &noticeFixture{
		orders:    entitymocks.NewMockOrderRepository(t),
		users:     entitymocks.NewMockUserRepository(t),
		tickets:   entitymocks.NewMockTicketRepository(t),
		mailer:    entitymocks.NewMockOrderMailer(t),
		workflows: &onceWorkflows{},
		emails:    map[entity.OrderID][]entity.OrderEmail{},
	}
	concert := &entity.Concert{
		ID: "event-1", LocalDate: time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC),
		Venue: &entity.Venue{Name: "Zepp Shinjuku"}, Series: &entity.Series{Title: "Liverty Live 2026", OrganizerID: new("org-1")},
	}
	f.uc = usecase.NewNotificationUseCase(entitymocks.NewMockNotificationRepository(t), entitymocks.NewMockPushSubscriptionRepository(t),
		entitymocks.NewMockPushNotificationSender(t), f.workflows, noopMetrics{}, newTestLogger(t),
		usecase.OrderConfirmationDeps{
			Now:      time.Now,
			Orders:   f.orders,
			Users:    f.users,
			Concerts: concertReader(t, concert),
			Tickets:  f.tickets,
			Mailer:   f.mailer,
			TimeZone: tsJST,
		}, usecase.NewConcertsDeps{}, usecase.SubjectDeps{Now: time.Now})
	f.mailer.EXPECT().SendCancellationEmail(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, id entity.OrderID, _ string, m entity.OrderEmail) error {
			f.record(id, m)
			return nil
		}).Maybe()
	f.mailer.EXPECT().SendRefundEmail(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, id entity.OrderID, _ string, m entity.OrderEmail) error {
			f.record(id, m)
			return nil
		}).Maybe()
	return f
}

func (f *noticeFixture) record(id entity.OrderID, m entity.OrderEmail) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.emails[id] = append(f.emails[id], m)
}

// noticeOrder is a 6000 yen Order of 2 tickets of event-1 by its own fan, paid with
// a VISA card ending in 1234.
func noticeOrder(id entity.OrderID, status entity.OrderStatus) *entity.Order {
	return &entity.Order{
		ID: id, UserID: entity.UserID("fan-" + string(id)), TicketStockID: "stock-1", TicketCount: 2,
		Status: status, Amount: 6000, Currency: "JPY",
		Payment: entity.Payment{Provider: entity.PaymentProviderStripe, CardBrand: "visa", CardLast4: "1234"},
	}
}

// noticeRefunded is noticeOrder as Refunded for the reason with the amount.
func noticeRefunded(id entity.OrderID, reason entity.RefundReason, amount int64) *entity.Order {
	o := noticeOrder(id, entity.OrderStatusRefunded)
	o.Refund = &entity.OrderRefund{Reason: reason, Amount: amount, RefundTime: time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)}
	return o
}

// readable makes the Order, its fan and its event readable, with issued and
// resold Tickets of the Order.
func (f *noticeFixture) readable(o *entity.Order, issued, resold int) {
	f.orders.EXPECT().Get(mock.Anything, o.ID).Return(o, nil).Maybe()
	f.orders.EXPECT().GetEventID(mock.Anything, o.ID).Return("event-1", nil).Maybe()
	f.users.EXPECT().Get(mock.Anything, string(o.UserID)).
		Return(&entity.User{ID: string(o.UserID), Email: string(o.UserID) + "@example.com"}, nil).Maybe()
	var tickets []*entity.Ticket
	for range issued {
		tickets = append(tickets, &entity.Ticket{OrderID: o.ID, UserID: o.UserID, Status: entity.TicketStatusIssued})
	}
	for range resold {
		tickets = append(tickets, &entity.Ticket{OrderID: o.ID, UserID: o.UserID, Status: entity.TicketStatusVoided})
	}
	// A Ticket of another Order of the fan is never counted.
	tickets = append(tickets, &entity.Ticket{OrderID: "other-order", UserID: o.UserID, Status: entity.TicketStatusIssued})
	f.tickets.EXPECT().ListByUserAndEvent(mock.Anything, o.UserID, "event-1").Return(tickets, nil).Maybe()
}

// runStarted runs each notice workflow the use case started, as the workflow
// worker does, and returns the Deliver requests they started by id.
func (f *noticeFixture) runStarted(t *testing.T, run func(context.Context, usecase.Flow, usecase.OrderNotice) (struct{}, error)) map[string]usecase.DeliverRequest {
	t.Helper()
	pushes := map[string]usecase.DeliverRequest{}
	for _, in := range f.workflows.started {
		flow := &noticeFlow{}
		_, err := run(context.Background(), flow, in)
		require.NoError(t, err)
		for id, input := range flow.inputs {
			pushes[id] = input.(usecase.DeliverRequest)
		}
	}
	return pushes
}

func TestNotificationUseCase_SendEventCancelled(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("two fans", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/notification/send-event-cancelled "Two fans"
		f := newNoticeFixture(t)
		a, b := noticeOrder("order-a", entity.OrderStatusPaid), noticeOrder("order-b", entity.OrderStatusPaid)
		f.orders.EXPECT().ListByEvent(mock.Anything, "event-1").Return([]*entity.Order{a, b}, nil)
		f.readable(a, 2, 0)
		f.readable(b, 2, 0)

		require.NoError(t, f.uc.SendEventCancelled(ctx, "event-1"))
		pushes := f.runStarted(t, f.uc.RunNotifyEventCancelled)

		assert.Len(t, f.emails["order-a"], 1)
		assert.Len(t, f.emails["order-b"], 1)
		assert.Len(t, pushes, 2)
		assert.Equal(t, entity.NotificationTypeConcertCancelled, pushes["deliver:concert_cancelled:order-a"].Type)
		assert.Equal(t, entity.NotificationTypeConcertCancelled, pushes["deliver:concert_cancelled:order-b"].Type)
	})

	t.Run("already refunded", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/notification/send-event-cancelled "Already refunded"
		f := newNoticeFixture(t)
		paid := noticeOrder("order-a", entity.OrderStatusPaid)
		f.orders.EXPECT().ListByEvent(mock.Anything, "event-1").
			Return([]*entity.Order{paid, noticeRefunded("order-b", entity.RefundReasonCancellation, 6000)}, nil)

		require.NoError(t, f.uc.SendEventCancelled(ctx, "event-1"))

		require.Len(t, f.workflows.started, 1)
		assert.Equal(t, "order-a", f.workflows.started[0].OrderID)
	})

	t.Run("refunded by a dispute before the cancellation", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/notification/send-event-cancelled "Refunded by a dispute before the cancellation"
		f := newNoticeFixture(t)
		f.orders.EXPECT().ListByEvent(mock.Anything, "event-1").
			Return([]*entity.Order{noticeRefunded("order-a", entity.RefundReasonDispute, 0)}, nil)

		require.NoError(t, f.uc.SendEventCancelled(ctx, "event-1"))

		assert.Empty(t, f.workflows.started)
	})

	t.Run("cancellation announced twice", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/notification/send-event-cancelled "Cancellation announced twice"
		f := newNoticeFixture(t)
		a, b := noticeOrder("order-a", entity.OrderStatusPaid), noticeOrder("order-b", entity.OrderStatusPaid)
		f.orders.EXPECT().ListByEvent(mock.Anything, "event-1").Return([]*entity.Order{a, b}, nil)
		f.readable(a, 2, 0)
		f.readable(b, 2, 0)

		require.NoError(t, f.uc.SendEventCancelled(ctx, "event-1"))
		require.NoError(t, f.uc.SendEventCancelled(ctx, "event-1"))
		pushes := f.runStarted(t, f.uc.RunNotifyEventCancelled)

		assert.Len(t, f.emails["order-a"], 1)
		assert.Len(t, f.emails["order-b"], 1)
		assert.Len(t, pushes, 2)
	})

	t.Run("no paid order", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/notification/send-event-cancelled "No paid order"
		f := newNoticeFixture(t)
		f.orders.EXPECT().ListByEvent(mock.Anything, "event-1").Return([]*entity.Order{}, nil)

		require.NoError(t, f.uc.SendEventCancelled(ctx, "event-1"))

		assert.Empty(t, f.workflows.started)
	})

	t.Run("listing fails", func(t *testing.T) {
		t.Parallel()
		f := newNoticeFixture(t)
		f.orders.EXPECT().ListByEvent(mock.Anything, "event-1").Return(nil, apperr.New(codes.Internal, "database down"))

		assert.ErrorIs(t, f.uc.SendEventCancelled(ctx, "event-1"), apperr.ErrInternal)
	})
}

func TestNotificationUseCase_RunNotifyEventCancelled(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	in := usecase.OrderNotice{OrderID: "order-a"}

	t.Run("cancellation email", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/notification/send-event-cancelled "Cancellation email"
		f := newNoticeFixture(t)
		f.readable(noticeOrder("order-a", entity.OrderStatusPaid), 2, 0)

		_, err := f.uc.RunNotifyEventCancelled(ctx, &noticeFlow{}, in)

		require.NoError(t, err)
		require.Len(t, f.emails["order-a"], 1)
		mail := f.emails["order-a"][0]
		assert.Contains(t, mail.Subject, "公演中止")
		for _, want := range []string{"Liverty Live 2026", "2026/11/20(金)", "Zepp Shinjuku", "中止", "枚数: 2枚", "6,000円（税込）", "VISA 末尾1234", "あらためて"} {
			assert.Contains(t, mail.TextBody, want)
		}
		assert.NotContains(t, mail.TextBody, "公式リセール")
	})

	t.Run("one ticket resold", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/notification/send-event-cancelled "One ticket resold"
		f := newNoticeFixture(t)
		f.readable(noticeOrder("order-a", entity.OrderStatusPaid), 1, 1)

		_, err := f.uc.RunNotifyEventCancelled(ctx, &noticeFlow{}, in)

		require.NoError(t, err)
		require.Len(t, f.emails["order-a"], 1)
		body := f.emails["order-a"][0].TextBody
		for _, want := range []string{"枚数: 1枚", "3,000円（税込）", "公式リセールで譲渡された1枚は、リセール成立時に返金済み"} {
			assert.Contains(t, body, want)
		}
	})

	t.Run("fan allowed notifications", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/notification/send-event-cancelled "Fan allowed notifications"
		// @spec components/usecase/notification/deliver "Cancellation sent"
		f := newNoticeFixture(t)
		f.readable(noticeOrder("order-a", entity.OrderStatusPaid), 2, 0)
		flow := &noticeFlow{}

		_, err := f.uc.RunNotifyEventCancelled(ctx, flow, in)

		require.NoError(t, err)
		push, ok := flow.inputs["deliver:concert_cancelled:order-a"].(usecase.DeliverRequest)
		require.True(t, ok, "Deliver is started named after the type and the Order")
		assert.Equal(t, "fan-order-a", push.UserID)
		assert.Equal(t, entity.NotificationTypeConcertCancelled, push.Type)
		assert.Equal(t, entity.NotificationSubject{OrderID: "order-a"}, push.Subject)

		r := newSubjectFixture(t)
		r.fanReadable(renderedFan("fan-order-a", "ja"))
		r.orderReadable(noticeOrder("order-a", entity.OrderStatusPaid), "event-1")
		r.concertsReadable(renderedConcert("event-1"))
		msg := r.push(t, push)
		assert.Equal(t, "/events/event-1", msg.Data[entity.NotificationDataKeyURL], "it opens the event's page")
		assert.Equal(t, "11/20のZepp Shinjuku公演は中止となりました。チケット代金はカードに返金されます。\nLiverty Live 2026", msg.Body)
	})

	t.Run("every ticket resold", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/notification/send-event-cancelled "Every ticket resold"
		f := newNoticeFixture(t)
		f.readable(noticeOrder("order-a", entity.OrderStatusPaid), 0, 2)
		flow := &noticeFlow{}

		_, err := f.uc.RunNotifyEventCancelled(ctx, flow, in)

		require.NoError(t, err)
		assert.Empty(t, f.emails)
		assert.Empty(t, flow.inputs)
	})

	t.Run("refunded before the notice", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/notification/send-event-cancelled "Refunded before the notice"
		f := newNoticeFixture(t)
		f.readable(noticeRefunded("order-a", entity.RefundReasonCancellation, 6000), 0, 0)
		flow := &noticeFlow{}

		_, err := f.uc.RunNotifyEventCancelled(ctx, flow, in)

		require.NoError(t, err)
		assert.Empty(t, f.emails)
		assert.Empty(t, flow.inputs)
	})

	t.Run("event deleted before the notice", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/notification/send-event-cancelled "Event deleted before the notice"
		f := newNoticeFixture(t)
		f.orders.EXPECT().Get(mock.Anything, entity.OrderID("order-a")).Return(nil, apperr.New(codes.NotFound, "order not found"))
		flow := &noticeFlow{}

		_, err := f.uc.RunNotifyEventCancelled(ctx, flow, in)

		require.NoError(t, err, "the notice is finished")
		assert.Empty(t, f.emails)
		assert.Empty(t, flow.inputs)
	})

	t.Run("mail unavailable", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/notification/send-event-cancelled "Mail unavailable"
		f := newNoticeFixture(t)
		f.mailer = entitymocks.NewMockOrderMailer(t)
		f.uc = rebuildWithMailer(t, f)
		f.readable(noticeOrder("order-a", entity.OrderStatusPaid), 2, 0)
		calls := 0
		f.mailer.EXPECT().SendCancellationEmail(mock.Anything, entity.OrderID("order-a"), "fan-order-a@example.com", mock.Anything).
			RunAndReturn(func(_ context.Context, id entity.OrderID, _ string, m entity.OrderEmail) error {
				calls++
				if calls == 1 {
					return apperr.New(codes.Unavailable, "email cannot be sent")
				}
				f.record(id, m)
				return nil
			})
		flow := newReplayFlow()

		_, err := f.uc.RunNotifyEventCancelled(ctx, flow, in)
		require.ErrorIs(t, err, apperr.ErrUnavailable)
		assert.Empty(t, flow.started, "nothing is pushed before the email")

		_, err = f.uc.RunNotifyEventCancelled(ctx, flow, in)

		require.NoError(t, err)
		assert.Len(t, f.emails["order-a"], 1)
		assert.Equal(t, []string{"deliver:concert_cancelled:order-a"}, flow.started)
	})
}

// rebuildWithMailer wires the fixture's use case again with its current
// mailer.
func rebuildWithMailer(t *testing.T, f *noticeFixture) usecase.NotificationUseCase {
	t.Helper()
	concert := &entity.Concert{
		ID: "event-1", LocalDate: time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC),
		Venue: &entity.Venue{Name: "Zepp Shinjuku"}, Series: &entity.Series{Title: "Liverty Live 2026", OrganizerID: new("org-1")},
	}
	return usecase.NewNotificationUseCase(entitymocks.NewMockNotificationRepository(t), entitymocks.NewMockPushSubscriptionRepository(t),
		entitymocks.NewMockPushNotificationSender(t), f.workflows, noopMetrics{}, newTestLogger(t),
		usecase.OrderConfirmationDeps{
			Now:    time.Now,
			Orders: f.orders, Users: f.users, Concerts: concertReader(t, concert), Tickets: f.tickets, Mailer: f.mailer, TimeZone: tsJST,
		}, usecase.NewConcertsDeps{}, usecase.SubjectDeps{Now: time.Now})
}

func TestNotificationUseCase_SendOrderRefunded(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	in := usecase.OrderNotice{OrderID: "order-a"}

	t.Run("refund email", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/notification/send-order-refunded "Refund email"
		f := newNoticeFixture(t)
		f.readable(noticeRefunded("order-a", entity.RefundReasonCancellation, 6000), 0, 0)

		_, err := f.uc.RunNotifyOrderRefunded(ctx, &noticeFlow{}, in)

		require.NoError(t, err)
		require.Len(t, f.emails["order-a"], 1)
		mail := f.emails["order-a"][0]
		assert.Contains(t, mail.Subject, "返金完了")
		for _, want := range []string{"Liverty Live 2026", "2026/11/20(金)", "Zepp Shinjuku", "中止", "枚数: 2枚", "6,000円（税込）", "VISA 末尾1234", "カード会社", "入場にご利用いただけなくなりました"} {
			assert.Contains(t, mail.TextBody, want)
		}
		assert.NotContains(t, mail.TextBody, "公式リセール")
	})

	t.Run("one ticket resold", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/notification/send-order-refunded "One ticket resold"
		f := newNoticeFixture(t)
		f.readable(noticeRefunded("order-a", entity.RefundReasonCancellation, 3000), 0, 0)

		_, err := f.uc.RunNotifyOrderRefunded(ctx, &noticeFlow{}, in)

		require.NoError(t, err)
		require.Len(t, f.emails["order-a"], 1)
		body := f.emails["order-a"][0].TextBody
		for _, want := range []string{"枚数: 1枚", "3,000円（税込）", "公式リセールで譲渡された1枚は、リセール成立時に返金済み"} {
			assert.Contains(t, body, want)
		}
	})

	t.Run("every ticket resold", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/notification/send-order-refunded "Every ticket resold"
		f := newNoticeFixture(t)
		f.readable(noticeRefunded("order-a", entity.RefundReasonCancellation, 0), 0, 0)
		flow := &noticeFlow{}

		_, err := f.uc.RunNotifyOrderRefunded(ctx, flow, in)

		require.NoError(t, err)
		assert.Empty(t, f.emails)
		assert.Empty(t, flow.inputs)
	})

	t.Run("dispute", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/notification/send-order-refunded "Dispute"
		f := newNoticeFixture(t)
		f.readable(noticeRefunded("order-a", entity.RefundReasonDispute, 0), 0, 0)
		flow := &noticeFlow{}

		_, err := f.uc.RunNotifyOrderRefunded(ctx, flow, in)

		require.NoError(t, err)
		assert.Empty(t, f.emails)
		assert.Empty(t, flow.inputs)
	})

	t.Run("fan allowed notifications", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/notification/send-order-refunded "Fan allowed notifications"
		// @spec components/usecase/notification/deliver "Refund sent"
		f := newNoticeFixture(t)
		f.readable(noticeRefunded("order-a", entity.RefundReasonCancellation, 6000), 0, 0)
		flow := &noticeFlow{}

		_, err := f.uc.RunNotifyOrderRefunded(ctx, flow, in)

		require.NoError(t, err)
		push, ok := flow.inputs["deliver:order_refunded:order-a"].(usecase.DeliverRequest)
		require.True(t, ok, "Deliver is started named after the type and the Order")
		assert.Equal(t, "fan-order-a", push.UserID)
		assert.Equal(t, entity.NotificationTypeOrderRefunded, push.Type)
		assert.Equal(t, entity.NotificationSubject{OrderID: "order-a"}, push.Subject)

		r := newSubjectFixture(t)
		r.fanReadable(renderedFan("fan-order-a", "ja"))
		r.orderReadable(noticeRefunded("order-a", entity.RefundReasonCancellation, 6000), "event-1")
		r.concertsReadable(renderedConcert("event-1"))
		msg := r.push(t, push)
		assert.Equal(t, "/orders/order-a", msg.Data[entity.NotificationDataKeyURL], "it opens the Order")
		assert.Contains(t, msg.Body, "6,000円")
	})

	t.Run("refund announced twice", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/notification/send-order-refunded "Refund announced twice"
		f := newNoticeFixture(t)
		f.readable(noticeRefunded("order-a", entity.RefundReasonCancellation, 6000), 0, 0)

		require.NoError(t, f.uc.SendOrderRefunded(ctx, "order-a"))
		require.NoError(t, f.uc.SendOrderRefunded(ctx, "order-a"))
		pushes := f.runStarted(t, f.uc.RunNotifyOrderRefunded)

		assert.Len(t, f.emails["order-a"], 1)
		assert.Len(t, pushes, 1)
	})

	t.Run("event deleted before the notice", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/notification/send-order-refunded "Event deleted before the notice"
		f := newNoticeFixture(t)
		f.orders.EXPECT().Get(mock.Anything, entity.OrderID("order-a")).Return(nil, apperr.New(codes.NotFound, "order not found"))
		flow := &noticeFlow{}

		_, err := f.uc.RunNotifyOrderRefunded(ctx, flow, in)

		require.NoError(t, err, "the notice is finished")
		assert.Empty(t, f.emails)
		assert.Empty(t, flow.inputs)
	})

	t.Run("mail unavailable", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/notification/send-order-refunded "Mail unavailable"
		f := newNoticeFixture(t)
		f.mailer = entitymocks.NewMockOrderMailer(t)
		f.uc = rebuildWithMailer(t, f)
		f.readable(noticeRefunded("order-a", entity.RefundReasonCancellation, 6000), 0, 0)
		calls := 0
		f.mailer.EXPECT().SendRefundEmail(mock.Anything, entity.OrderID("order-a"), "fan-order-a@example.com", mock.Anything).
			RunAndReturn(func(_ context.Context, id entity.OrderID, _ string, m entity.OrderEmail) error {
				calls++
				if calls == 1 {
					return apperr.New(codes.Unavailable, "email cannot be sent")
				}
				f.record(id, m)
				return nil
			})
		flow := newReplayFlow()

		_, err := f.uc.RunNotifyOrderRefunded(ctx, flow, in)
		require.ErrorIs(t, err, apperr.ErrUnavailable)
		assert.Empty(t, flow.started, "nothing is pushed before the email")

		_, err = f.uc.RunNotifyOrderRefunded(ctx, flow, in)

		require.NoError(t, err)
		assert.Len(t, f.emails["order-a"], 1)
		assert.Equal(t, []string{"deliver:order_refunded:order-a"}, flow.started)
	})
}
