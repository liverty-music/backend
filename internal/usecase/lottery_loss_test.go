package usecase_test

import (
	"context"
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

// lossFixture holds the mocks of SendLotteryLoss under test.
type lossFixture struct {
	entries   *entitymocks.MockLotteryEntryRepository
	orders    *entitymocks.MockOrderRepository
	users     *entitymocks.MockUserRepository
	stocks    *entitymocks.MockTicketStockRepository
	mailer    *entitymocks.MockLotteryLossEmailSender
	workflows *ucmocks.MockWorkflows
	uc        usecase.NotificationUseCase
}

func newLossFixture(t *testing.T) *lossFixture {
	t.Helper()
	f := &lossFixture{
		entries:   entitymocks.NewMockLotteryEntryRepository(t),
		orders:    entitymocks.NewMockOrderRepository(t),
		users:     entitymocks.NewMockUserRepository(t),
		stocks:    entitymocks.NewMockTicketStockRepository(t),
		mailer:    entitymocks.NewMockLotteryLossEmailSender(t),
		workflows: ucmocks.NewMockWorkflows(t),
	}
	concert := &entity.Concert{
		ID: "event-1", LocalDate: time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC),
		Venue: &entity.Venue{Name: "Zepp Shinjuku"}, Series: &entity.Series{Title: "Liverty Live 2026", OrganizerID: new("org-1")},
	}
	f.uc = usecase.NewNotificationUseCase(entitymocks.NewMockNotificationRepository(t), entitymocks.NewMockPushSubscriptionRepository(t),
		entitymocks.NewMockPushNotificationSender(t), f.workflows, noopMetrics{}, newTestLogger(t),
		usecase.OrderConfirmationDeps{
			Orders:     f.orders,
			Users:      f.users,
			Concerts:   stubConcerts{concert: concert},
			Entries:    f.entries,
			Stocks:     f.stocks,
			LossMailer: f.mailer,
			TimeZone:   tsJST,
		}, usecase.NewConcertsDeps{})
	return f
}

// lost makes entry-1 Lost with the reason, its Void Order order-1 of fan-1
// for 2 S席 tickets of stock-1 of event-1, readable.
func (f *lossFixture) lost(reason entity.LotteryEntryLostReason, language string) {
	f.entries.EXPECT().Get(mock.Anything, entity.LotteryEntryID("entry-1")).Return(&entity.LotteryEntry{
		ID: "entry-1", OrderID: "order-1", TicketSaleID: "sale-l", State: entity.LotteryEntryStateLost, LostReason: reason,
	}, nil).Maybe()
	f.orders.EXPECT().Get(mock.Anything, entity.OrderID("order-1")).Return(&entity.Order{
		ID: "order-1", UserID: "fan-1", TicketStockID: "stock-1", TicketCount: 2, Status: entity.OrderStatusVoid,
	}, nil).Maybe()
	f.users.EXPECT().Get(mock.Anything, "fan-1").Return(&entity.User{ID: "fan-1", Email: "fan@example.com", PreferredLanguage: language}, nil).Maybe()
	f.stocks.EXPECT().Get(mock.Anything, entity.TicketStockID("stock-1"), mock.Anything).Return(&entity.TicketStock{
		ID: "stock-1", EventID: "event-1", SeatType: &entity.SeatType{Name: "S席", Price: 8000},
	}, nil).Maybe()
}

// sends records the loss email.
func (f *lossFixture) sends(mail *entity.LotteryLossEmail) {
	f.mailer.EXPECT().SendLossEmail(mock.Anything, entity.LotteryEntryID("entry-1"), "fan@example.com", mock.Anything).
		RunAndReturn(func(_ context.Context, _ entity.LotteryEntryID, _ string, m entity.LotteryLossEmail) error {
			*mail = m
			return nil
		})
}

// expectPush expects the deliver-notification workflow of one lottery_loss
// notification for fan-1, named after the entry, and returns its payload.
func (f *lossFixture) expectPush() *entity.NotificationPayload {
	var got entity.NotificationPayload
	f.workflows.EXPECT().Start(mock.Anything, usecase.WorkflowDeliverNotification, "deliver:lottery_loss:entry-1",
		mock.MatchedBy(func(r usecase.DeliverRequest) bool {
			return r.UserID == "fan-1" && r.Type == entity.NotificationTypeLotteryLoss
		})).
		RunAndReturn(func(_ context.Context, _, _ string, input any, _ ...usecase.StartOption) error {
			got = *input.(usecase.DeliverRequest).Payload
			return nil
		})
	return &got
}

func TestNotificationUseCase_SendLotteryLoss(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("entry not drawn", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/notification/send-lottery-loss "Entry not drawn"
		f := newLossFixture(t)
		f.lost(entity.LotteryEntryLostReasonNotDrawn, "ja")
		var mail entity.LotteryLossEmail
		f.sends(&mail)
		f.expectPush()

		require.NoError(t, f.uc.SendLotteryLoss(ctx, "entry-1"))

		for _, want := range []string{"Liverty Live 2026", "2026/11/20(金)", "Zepp Shinjuku", "席種: S席", "枚数: 2枚", "落選", "請求はありません", "数日間"} {
			assert.Contains(t, mail.TextBody, want)
		}
		assert.Contains(t, mail.Subject, "抽選の結果")
	})

	t.Run("card could not be charged", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/notification/send-lottery-loss "Card could not be charged"
		f := newLossFixture(t)
		f.lost(entity.LotteryEntryLostReasonCardNotCharged, "en")
		var mail entity.LotteryLossEmail
		f.sends(&mail)
		f.expectPush()

		require.NoError(t, f.uc.SendLotteryLoss(ctx, "entry-1"))

		assert.Contains(t, mail.TextBody, "Your lottery entry won, but your card could not be charged, so the win was cancelled")
		assert.Contains(t, mail.TextBody, "Your card was not charged")
		assert.Contains(t, mail.Subject, "win was cancelled")
	})

	t.Run("loss announced twice", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/notification/send-lottery-loss "Loss announced twice"
		// The sender emails an entry once (it sends nothing once the entry has
		// a loss email send time) and the runtime runs one workflow per id.
		f := newLossFixture(t)
		f.lost(entity.LotteryEntryLostReasonNotDrawn, "ja")
		emails := 0
		f.mailer.EXPECT().SendLossEmail(mock.Anything, entity.LotteryEntryID("entry-1"), "fan@example.com", mock.Anything).
			RunAndReturn(func(context.Context, entity.LotteryEntryID, string, entity.LotteryLossEmail) error {
				emails++
				return nil
			}).Twice()
		pushed := map[string]bool{}
		f.workflows.EXPECT().Start(mock.Anything, usecase.WorkflowDeliverNotification, mock.Anything, mock.Anything).
			RunAndReturn(func(_ context.Context, _, id string, _ any, _ ...usecase.StartOption) error {
				pushed[id] = true
				return nil
			}).Twice()

		require.NoError(t, f.uc.SendLotteryLoss(ctx, "entry-1"))
		require.NoError(t, f.uc.SendLotteryLoss(ctx, "entry-1"))

		assert.Equal(t, 2, emails, "both runs ask the sender, which keeps it to one email")
		assert.Len(t, pushed, 1, "one push workflow for the entry")
	})

	t.Run("mail unavailable", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/notification/send-lottery-loss "Mail unavailable"
		f := newLossFixture(t)
		f.lost(entity.LotteryEntryLostReasonNotDrawn, "ja")
		f.mailer.EXPECT().SendLossEmail(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(apperr.New(apperr.ErrUnavailable.Code, "postmark down"))

		err := f.uc.SendLotteryLoss(ctx, "entry-1")

		assert.ErrorIs(t, err, apperr.ErrUnavailable, "fails so the announcement is redelivered; no push yet")
	})

	t.Run("fan allowed notifications", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/notification/send-lottery-loss "Fan allowed notifications"
		// @spec components/usecase/notification/deliver "Lottery loss sent"
		f := newLossFixture(t)
		f.lost(entity.LotteryEntryLostReasonNotDrawn, "ja")
		var mail entity.LotteryLossEmail
		f.sends(&mail)
		push := f.expectPush()

		require.NoError(t, f.uc.SendLotteryLoss(ctx, "entry-1"))

		assert.Equal(t, "抽選結果のお知らせ", push.Title)
		assert.Equal(t, "/events/event-1", push.Data[entity.NotificationDataKeyURL], "it opens the event's page")
	})

	t.Run("entry not lost", func(t *testing.T) {
		t.Parallel()
		f := newLossFixture(t)
		f.entries.EXPECT().Get(mock.Anything, entity.LotteryEntryID("entry-1")).Return(&entity.LotteryEntry{
			ID: "entry-1", OrderID: "order-1", State: entity.LotteryEntryStateWon,
		}, nil)

		require.NoError(t, f.uc.SendLotteryLoss(ctx, "entry-1"))
	})
}
