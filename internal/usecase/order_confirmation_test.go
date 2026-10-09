package usecase_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	entitymocks "github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/usecase"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// stubConcerts is a ConcertsByIDsReader returning a fixed concert.
type stubConcerts struct{ concert *entity.Concert }

func (s stubConcerts) ListByIDs(context.Context, []string) ([]*entity.Concert, error) {
	if s.concert == nil {
		return nil, nil
	}
	return []*entity.Concert{s.concert}, nil
}

// confirmationFixture holds the mocks of SendOrderConfirmation under test.
type confirmationFixture struct {
	orders     *entitymocks.MockOrderRepository
	users      *entitymocks.MockUserRepository
	organizers *entitymocks.MockOrganizerRepository
	mailer     *entitymocks.MockOrderConfirmationSender
	notifs     *entitymocks.MockNotificationRepository
	pushSubs   *entitymocks.MockPushSubscriptionRepository
	sender     *entitymocks.MockPushNotificationSender
	publisher  *ucmocks.MockEventPublisher
	uc         usecase.NotificationUseCase
}

func newConfirmationFixture(t *testing.T) *confirmationFixture {
	t.Helper()
	f := &confirmationFixture{
		orders:     entitymocks.NewMockOrderRepository(t),
		users:      entitymocks.NewMockUserRepository(t),
		organizers: entitymocks.NewMockOrganizerRepository(t),
		mailer:     entitymocks.NewMockOrderConfirmationSender(t),
		notifs:     entitymocks.NewMockNotificationRepository(t),
		pushSubs:   entitymocks.NewMockPushSubscriptionRepository(t),
		sender:     entitymocks.NewMockPushNotificationSender(t),
		publisher:  ucmocks.NewMockEventPublisher(t),
	}
	start := time.Date(2026, 11, 20, 19, 0, 0, 0, tsJST)
	open := time.Date(2026, 11, 20, 18, 0, 0, 0, tsJST)
	concert := &entity.Concert{
		ID: "event-1", LocalDate: time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC),
		StartTime: &start, OpenTime: &open, Venue: &entity.Venue{Name: "Zepp Shinjuku"},
		Series: &entity.Series{Title: "Liverty Live 2026"},
	}
	f.uc = usecase.NewNotificationUseCase(f.notifs, f.pushSubs, f.sender, f.publisher, noopMetrics{}, newTestLogger(t),
		usecase.OrderConfirmationDeps{
			Orders:         f.orders,
			Users:          f.users,
			Concerts:       stubConcerts{concert: concert},
			EventOrganizer: &stubEventOrganizerRepo{getOrganizerIDFn: func(context.Context, string) (string, error) { return "org-1", nil }},
			Organizers:     f.organizers,
			Mailer:         f.mailer,
			TimeZone:       tsJST,
		})
	f.organizers.EXPECT().Get(mock.Anything, "org-1").Return(&entity.Organizer{ID: "org-1", SellerDetails: &entity.SellerDetails{
		LegalName: "株式会社リバティ", RepresentativeName: "代表 太郎", Address: "東京都渋谷区1-2-3",
		PhoneNumber: "+81312345678", ContactEmail: "contact@example.com",
	}}, nil).Maybe()
	return f
}

// paidOrder is order-1: 2 tickets for 6000 yen on a Visa ending 4242.
func confirmedOrder(source string) (*entity.Order, entity.OrderPaidData) {
	order := &entity.Order{
		ID: "order-1", BuyerID: "fan-1", Amount: 6000, Currency: "JPY", Status: entity.OrderStatusPaid,
		PaidTime: time.Date(2026, 11, 5, 9, 20, 0, 0, time.UTC),
		Payment:  entity.Payment{Provider: entity.PaymentProviderStripe, PaymentIntentRef: "pi_1", CardBrand: "visa", CardLast4: "4242"},
	}
	data := entity.OrderPaidData{OrderID: "order-1", BuyerID: "fan-1", EventID: "event-1", TicketCount: 2, Amount: 6000, Currency: "JPY"}
	if source == "lottery" {
		order.ApplicationID, data.ApplicationID = "app-1", "app-1"
	} else {
		order.ReservationID, data.ReservationID = "res-1", "res-1"
	}
	return order, data
}

// expectPush expects one order_confirmation notification for fan-1, pushed to
// one registered browser, and returns the captured payload.
func (f *confirmationFixture) expectPush(t *testing.T) *entity.NotificationPayload {
	t.Helper()
	var got entity.NotificationPayload
	f.notifs.EXPECT().Create(mock.Anything, mock.MatchedBy(func(n *entity.Notification) bool {
		return n.UserID == "fan-1" && n.Type == entity.NotificationTypeOrderConfirmation
	})).RunAndReturn(func(_ context.Context, n *entity.Notification) error { n.ID = "notif-1"; return nil })
	f.pushSubs.EXPECT().ListByUserIDs(mock.Anything, []string{"fan-1"}).Return([]*entity.PushSubscription{{ID: "sub-1", UserID: "fan-1"}}, nil)
	f.sender.EXPECT().Send(mock.Anything, mock.Anything, mock.Anything).RunAndReturn(func(_ context.Context, raw []byte, _ *entity.PushSubscription) error {
		return json.Unmarshal(raw, &got)
	})
	f.notifs.EXPECT().UpdateDelivery(mock.Anything, "notif-1", entity.NotificationDeliveryStatusDelivered, mock.Anything, "").Return(nil)
	f.publisher.EXPECT().PublishEvent(mock.Anything, entity.SubjectNotificationDelivered, mock.Anything).Return(nil)
	return &got
}

func TestNotificationUseCase_SendOrderConfirmation(t *testing.T) {
	t.Parallel()

	t.Run("checkout paid", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/notification/send-order-confirmation "Checkout paid"
		// @spec components/usecase/notification/send-order-confirmation "Fan allowed notifications"
		// @spec components/usecase/notification/deliver "Purchase notification requested"
		f := newConfirmationFixture(t)
		order, data := confirmedOrder("checkout")
		f.orders.EXPECT().Get(mock.Anything, entity.OrderID("order-1")).Return(order, nil)
		f.users.EXPECT().Get(mock.Anything, "fan-1").Return(&entity.User{ID: "fan-1", Email: "fan@example.com", PreferredLanguage: "ja"}, nil)
		var mail entity.OrderConfirmationEmail
		f.mailer.EXPECT().SendConfirmationEmail(mock.Anything, entity.OrderID("order-1"), "fan@example.com", mock.Anything).
			RunAndReturn(func(_ context.Context, _ entity.OrderID, _ string, m entity.OrderConfirmationEmail) error {
				mail = m
				return nil
			})
		push := f.expectPush(t)

		require.NoError(t, f.uc.SendOrderConfirmation(context.Background(), data))

		for _, want := range []string{
			"Liverty Live 2026", "2026/11/20(金)", "開場 18:00", "開演 19:00", "Zepp Shinjuku",
			"枚数: 2枚", "6,000円（税込）", "VISA 末尾4242", "2026/11/05 18:20",
			"株式会社リバティ", "代表 太郎", "東京都渋谷区1-2-3", "+81312345678", "contact@example.com",
			"転売", "キャンセル・返金はできません", "「チケット」画面",
		} {
			assert.Contains(t, mail.TextBody, want)
		}
		assert.Contains(t, mail.Subject, "ご購入が完了しました")
		assert.Equal(t, "/tickets", push.Data[entity.NotificationDataKeyURL])
		assert.Contains(t, push.Title, "ご購入が完了しました")
	})

	t.Run("lottery win", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/notification/send-order-confirmation "Lottery win"
		f := newConfirmationFixture(t)
		order, data := confirmedOrder("lottery")
		f.orders.EXPECT().Get(mock.Anything, entity.OrderID("order-1")).Return(order, nil)
		f.users.EXPECT().Get(mock.Anything, "fan-1").Return(&entity.User{ID: "fan-1", Email: "fan@example.com", PreferredLanguage: "en"}, nil)
		var mail entity.OrderConfirmationEmail
		f.mailer.EXPECT().SendConfirmationEmail(mock.Anything, entity.OrderID("order-1"), "fan@example.com", mock.Anything).
			RunAndReturn(func(_ context.Context, _ entity.OrderID, _ string, m entity.OrderConfirmationEmail) error {
				mail = m
				return nil
			})
		push := f.expectPush(t)

		require.NoError(t, f.uc.SendOrderConfirmation(context.Background(), data))

		assert.Contains(t, mail.TextBody, "JPY 6,000 (tax included)")
		assert.Contains(t, mail.TextBody, "Resale of these tickets without the organizer's consent is prohibited")
		assert.Equal(t, "Ticket purchase complete", push.Title)
	})

	t.Run("order announced twice", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/notification/send-order-confirmation "Order announced twice"
		// The sender keeps the email to one per Order (it sends nothing for an
		// Order with a confirmation-sent time); the redelivery still pushes.
		f := newConfirmationFixture(t)
		order, data := confirmedOrder("checkout")
		f.orders.EXPECT().Get(mock.Anything, entity.OrderID("order-1")).Return(order, nil)
		f.users.EXPECT().Get(mock.Anything, "fan-1").Return(&entity.User{ID: "fan-1", Email: "fan@example.com"}, nil)
		f.mailer.EXPECT().SendConfirmationEmail(mock.Anything, entity.OrderID("order-1"), "fan@example.com", mock.Anything).Return(nil).Once()
		f.expectPush(t)

		require.NoError(t, f.uc.SendOrderConfirmation(context.Background(), data))
	})

	t.Run("mail unavailable", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/notification/send-order-confirmation "Mail unavailable"
		f := newConfirmationFixture(t)
		order, data := confirmedOrder("checkout")
		f.orders.EXPECT().Get(mock.Anything, entity.OrderID("order-1")).Return(order, nil)
		f.users.EXPECT().Get(mock.Anything, "fan-1").Return(&entity.User{ID: "fan-1", Email: "fan@example.com"}, nil)
		f.mailer.EXPECT().SendConfirmationEmail(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(apperr.New(apperr.ErrUnavailable.Code, "postmark down"))

		err := f.uc.SendOrderConfirmation(context.Background(), data)

		assert.ErrorIs(t, err, apperr.ErrUnavailable, "fails so the announcement is redelivered; no push yet")
	})
}

// TestEmailLanguageDefault checks that a buyer without a language preference
// gets the Japanese email.
func TestEmailLanguageDefault(t *testing.T) {
	t.Parallel()
	f := newConfirmationFixture(t)
	order, data := confirmedOrder("checkout")
	f.orders.EXPECT().Get(mock.Anything, entity.OrderID("order-1")).Return(order, nil)
	f.users.EXPECT().Get(mock.Anything, "fan-1").Return(&entity.User{ID: "fan-1", Email: "fan@example.com"}, nil)
	var mail entity.OrderConfirmationEmail
	f.mailer.EXPECT().SendConfirmationEmail(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, _ entity.OrderID, _ string, m entity.OrderConfirmationEmail) error {
			mail = m
			return nil
		})
	push := f.expectPush(t)

	require.NoError(t, f.uc.SendOrderConfirmation(context.Background(), data))

	assert.Contains(t, mail.Subject, "【Liverty Music】")
	assert.Contains(t, push.Title, "ご購入が完了しました")
}

func TestTicketJourneyUseCase_MarkPaid(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ name, source string }{
		// @spec components/usecase/ticket-journey/mark-paid "Checkout paid"
		{"checkout paid", "checkout"},
		// @spec components/usecase/ticket-journey/mark-paid "Lottery win"
		{"lottery win", "lottery"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			journeys := entitymocks.NewMockTicketJourneyRepository(t)
			uc := usecase.NewTicketJourneyUseCase(journeys, ucmocks.NewMockEventPublisher(t), newTestLogger(t))
			_, data := confirmedOrder(tc.source)
			journeys.EXPECT().Upsert(mock.Anything, &entity.TicketJourney{UserID: "fan-1", EventID: "event-1", Status: entity.TicketJourneyStatusPaid}).Return(nil).Twice()

			// No status-change announcement (the publisher mock expects none),
			// and running again leaves the journey Paid.
			require.NoError(t, uc.MarkPaid(context.Background(), data))
			require.NoError(t, uc.MarkPaid(context.Background(), data))
		})
	}

	t.Run("update fails", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket-journey/mark-paid "Update fails"
		journeys := entitymocks.NewMockTicketJourneyRepository(t)
		uc := usecase.NewTicketJourneyUseCase(journeys, ucmocks.NewMockEventPublisher(t), newTestLogger(t))
		_, data := confirmedOrder("checkout")
		journeys.EXPECT().Upsert(mock.Anything, mock.Anything).Return(apperr.ErrInternal)

		assert.ErrorIs(t, uc.MarkPaid(context.Background(), data), apperr.ErrInternal)
	})
}
