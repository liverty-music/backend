package usecase_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/liverty-music/backend/internal/entity"
	entitymocks "github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/usecase"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
)

// endedFixture holds the mocks of SendResaleEntryEnded under test.
type endedFixture struct {
	entries   *entitymocks.MockResaleEntryRepository
	users     *entitymocks.MockUserRepository
	stocks    *entitymocks.MockTicketStockRepository
	mailer    *entitymocks.MockResaleEntryEndedEmailSender
	workflows *ucmocks.MockWorkflows
	uc        usecase.NotificationUseCase
}

// newEndedFixture builds the use case for a concert starting at start.
func newEndedFixture(t *testing.T, start time.Time) *endedFixture {
	t.Helper()
	f := &endedFixture{
		entries:   entitymocks.NewMockResaleEntryRepository(t),
		users:     entitymocks.NewMockUserRepository(t),
		stocks:    entitymocks.NewMockTicketStockRepository(t),
		mailer:    entitymocks.NewMockResaleEntryEndedEmailSender(t),
		workflows: ucmocks.NewMockWorkflows(t),
	}
	concert := &entity.Concert{Series: &entity.Series{Title: "Liverty Live 2026", OrganizerID: new("org-1")}}
	concert.ID, concert.LocalDate, concert.StartTime = "event-1", start, &start
	concert.Venue = &entity.Venue{Name: "Zepp Shinjuku"}
	f.uc = usecase.NewNotificationUseCase(entitymocks.NewMockNotificationRepository(t), entitymocks.NewMockPushSubscriptionRepository(t),
		entitymocks.NewMockPushNotificationSender(t), f.workflows, noopMetrics{}, newTestLogger(t),
		usecase.OrderConfirmationDeps{
			Users:             f.users,
			Concerts:          stubConcerts{concert: concert},
			Stocks:            f.stocks,
			ResaleEntries:     f.entries,
			ResaleEntryMailer: f.mailer,
			TimeZone:          tsJST,
		}, usecase.NewConcertsDeps{})
	return f
}

// ended makes entry-1 of fan-1 for 2 S席 tickets readable in the state with
// the reason.
func (f *endedFixture) ended(state entity.ResaleEntryState, reason entity.ResaleEntryLostReason) {
	f.entries.EXPECT().Get(mock.Anything, entity.ResaleEntryID("entry-1")).Return(&entity.ResaleEntry{
		ID: "entry-1", OrderID: "order-1", State: state, LostReason: reason,
		Order: &entity.Order{ID: "order-1", UserID: "fan-1", TicketStockID: "stock-1", TicketCount: 2, Status: entity.OrderStatusVoid},
	}, nil)
	f.users.EXPECT().Get(mock.Anything, "fan-1").Return(&entity.User{ID: "fan-1", Email: "fan@example.com"}, nil).Maybe()
	f.stocks.EXPECT().Get(mock.Anything, entity.TicketStockID("stock-1"), mock.Anything).Return(&entity.TicketStock{
		ID: "stock-1", EventID: "event-1", SeatType: &entity.SeatType{Name: "S席", Price: 5000},
	}, nil).Maybe()
}

// sends records the ended email.
func (f *endedFixture) sends(mail *entity.ResaleEntryEndedEmail) {
	f.mailer.EXPECT().SendEndedEmail(mock.Anything, entity.ResaleEntryID("entry-1"), "fan@example.com", mock.Anything).
		RunAndReturn(func(_ context.Context, _ entity.ResaleEntryID, _ string, m entity.ResaleEntryEndedEmail) error {
			*mail = m
			return nil
		}).Once()
}

// expectPush expects one push of the type for fan-1, named after the type
// and the entry, and returns its payload.
func (f *endedFixture) expectPush(typ entity.NotificationType) *entity.NotificationPayload {
	var got entity.NotificationPayload
	f.workflows.EXPECT().Start(mock.Anything, usecase.WorkflowDeliverNotification, "deliver:"+string(typ)+":entry-1",
		mock.MatchedBy(func(r usecase.DeliverRequest) bool { return r.UserID == "fan-1" && r.Type == typ })).
		RunAndReturn(func(_ context.Context, _, _ string, input any, _ ...usecase.StartOption) error {
			got = *input.(usecase.DeliverRequest).Payload
			return nil
		}).Once()
	return &got
}

func TestNotificationUseCase_SendResaleEntryEnded(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	open := time.Now().Add(72 * time.Hour)
	closed := time.Now().Add(-time.Hour)

	t.Run("lapsed entry", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/notification/send-resale-entry-ended "Lapsed entry"
		f := newEndedFixture(t, open)
		f.ended(entity.ResaleEntryStateLapsed, entity.ResaleEntryLostReasonUnspecified)
		var mail entity.ResaleEntryEndedEmail
		f.sends(&mail)
		push := f.expectPush(entity.NotificationTypeResaleEntryLapsed)

		require.NoError(t, f.uc.SendResaleEntryEnded(ctx, "entry-1"))

		assert.Contains(t, mail.TextBody, "カードへの請求はありません")
		assert.Contains(t, mail.TextBody, "再度お申し込みいただけます")
		assert.Contains(t, mail.TextBody, "/resale-entries")
		assert.Contains(t, mail.TextBody, "S席")
		assert.Contains(t, push.Body, "請求はありません")
		assert.Equal(t, "/resale-entries", push.Data["url"])
	})

	t.Run("lost at the last draw", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/notification/send-resale-entry-ended "Lost at the last draw"
		f := newEndedFixture(t, closed)
		f.ended(entity.ResaleEntryStateLost, entity.ResaleEntryLostReasonNoTicketLeft)
		var mail entity.ResaleEntryEndedEmail
		f.sends(&mail)
		push := f.expectPush(entity.NotificationTypeResaleEntryLost)

		require.NoError(t, f.uc.SendResaleEntryEnded(ctx, "entry-1"))

		assert.Contains(t, mail.TextBody, "割り当てられるリセールチケットがありませんでした")
		assert.Contains(t, mail.TextBody, "カードへの請求はありません")
		assert.Contains(t, push.Body, "請求はありません")
	})

	t.Run("run again after the email was sent", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/notification/send-resale-entry-ended "Run again after the email was sent"
		// ResaleEntry.SendEndedEmail sends nothing for an entry with an ended
		// email send time, and the push is the same named Deliver.
		f := newEndedFixture(t, open)
		f.ended(entity.ResaleEntryStateLapsed, entity.ResaleEntryLostReasonUnspecified)
		f.mailer.EXPECT().SendEndedEmail(mock.Anything, entity.ResaleEntryID("entry-1"), "fan@example.com", mock.Anything).Return(nil).Once()
		f.workflows.EXPECT().Start(mock.Anything, usecase.WorkflowDeliverNotification, "deliver:resale_entry_lapsed:entry-1", mock.Anything).Return(nil).Once()

		require.NoError(t, f.uc.SendResaleEntryEnded(ctx, "entry-1"))
	})

	t.Run("won but not charged while resale is open", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/notification/send-resale-entry-ended "Won but not charged while resale is open"
		f := newEndedFixture(t, open)
		f.ended(entity.ResaleEntryStateLost, entity.ResaleEntryLostReasonCardNotCharged)
		var mail entity.ResaleEntryEndedEmail
		f.sends(&mail)
		push := f.expectPush(entity.NotificationTypeResaleEntryLost)

		require.NoError(t, f.uc.SendResaleEntryEnded(ctx, "entry-1"))

		assert.Contains(t, mail.TextBody, "カードで決済できなかったため、当選を取り消しました")
		assert.Contains(t, mail.TextBody, "別のカードで再度お申し込みいただけます")
		assert.Contains(t, mail.TextBody, "/resale-entries")
		assert.Contains(t, push.Body, "再度お申し込みいただけます")
	})

	t.Run("won but not charged after resale closed", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/notification/send-resale-entry-ended "Won but not charged after resale closed"
		f := newEndedFixture(t, closed)
		f.ended(entity.ResaleEntryStateLost, entity.ResaleEntryLostReasonCardNotCharged)
		var mail entity.ResaleEntryEndedEmail
		f.sends(&mail)
		f.expectPush(entity.NotificationTypeResaleEntryLost)

		require.NoError(t, f.uc.SendResaleEntryEnded(ctx, "entry-1"))

		assert.Contains(t, mail.TextBody, "当選を取り消しました")
		assert.Contains(t, mail.TextBody, "受付は終了しました")
		assert.NotContains(t, mail.TextBody, "再度お申し込み")
	})

	t.Run("concert cancelled", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/notification/send-resale-entry-ended "Concert cancelled"
		f := newEndedFixture(t, open)
		f.ended(entity.ResaleEntryStateLost, entity.ResaleEntryLostReasonConcertCancelled)
		var mail entity.ResaleEntryEndedEmail
		f.sends(&mail)
		push := f.expectPush(entity.NotificationTypeResaleEntryLost)

		require.NoError(t, f.uc.SendResaleEntryEnded(ctx, "entry-1"))

		assert.Contains(t, mail.TextBody, "公演が中止")
		assert.Contains(t, mail.TextBody, "カードへの請求はありません")
		assert.Contains(t, push.Body, "公演中止")
	})

	t.Run("payment not completed before the show", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/notification/send-resale-entry-ended "Payment not completed before the show"
		f := newEndedFixture(t, closed)
		f.ended(entity.ResaleEntryStateLost, entity.ResaleEntryLostReasonPaymentNotCompletedBeforeShow)
		var mail entity.ResaleEntryEndedEmail
		f.sends(&mail)
		f.expectPush(entity.NotificationTypeResaleEntryLost)

		require.NoError(t, f.uc.SendResaleEntryEnded(ctx, "entry-1"))

		assert.Contains(t, mail.TextBody, "決済事業者の障害により開演までに決済を完了できなかった")
		assert.Contains(t, mail.TextBody, "カードへの請求はありません")
		assert.Contains(t, mail.TextBody, "受付は終了しました")
	})

	t.Run("an entry still waiting sends nothing", func(t *testing.T) {
		t.Parallel()
		f := newEndedFixture(t, open)
		f.ended(entity.ResaleEntryStateEntered, entity.ResaleEntryLostReasonUnspecified)

		require.NoError(t, f.uc.SendResaleEntryEnded(ctx, "entry-1"))
	})
}

func TestOrderUseCase_StartResaleEntryWorkflow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	entry := func(state entity.ResaleEntryState, reason entity.ResaleEntryLostReason) *entity.ResaleEntry {
		return &entity.ResaleEntry{ID: "entry-1", OrderID: "order-1", State: state, LostReason: reason}
	}

	t.Run("won entry is issued once however often it is delivered", func(t *testing.T) {
		t.Parallel()
		workflows := ucmocks.NewMockWorkflows(t)
		uc := usecase.NewOrderUseCase(usecase.OrderDeps{Workflows: workflows, Clock: time.Now, Logger: newTestLogger(t)})
		workflows.EXPECT().Start(mock.Anything, usecase.WorkflowIssue, "issue:order-1", usecase.IssueRequest{ResaleEntryID: "entry-1"}, mock.Anything, mock.Anything).
			Return(nil).Twice()

		require.NoError(t, uc.StartResaleEntryWorkflow(ctx, entry(entity.ResaleEntryStateWon, 0)))
		require.NoError(t, uc.StartResaleEntryWorkflow(ctx, entry(entity.ResaleEntryStateWon, 0)), "the same name starts nothing new")
	})

	for _, tt := range []struct {
		name   string
		state  entity.ResaleEntryState
		reason entity.ResaleEntryLostReason
	}{
		{"lapsed", entity.ResaleEntryStateLapsed, 0},
		{"withdrawn", entity.ResaleEntryStateWithdrawn, 0},
		{"lost at the last draw", entity.ResaleEntryStateLost, entity.ResaleEntryLostReasonNoTicketLeft},
		{"win reverted", entity.ResaleEntryStateLost, entity.ResaleEntryLostReasonCardNotCharged},
	} {
		t.Run(tt.name+" is released", func(t *testing.T) {
			t.Parallel()
			workflows := ucmocks.NewMockWorkflows(t)
			uc := usecase.NewOrderUseCase(usecase.OrderDeps{Workflows: workflows, Clock: time.Now, Logger: newTestLogger(t)})
			workflows.EXPECT().Start(mock.Anything, usecase.WorkflowReleaseCardAuthorization, "release:order-1", entity.OrderID("order-1"), mock.Anything, mock.Anything).
				Return(nil).Once()

			require.NoError(t, uc.StartResaleEntryWorkflow(ctx, entry(tt.state, tt.reason)))
		})
	}

	t.Run("payment not completed before the show is not released again", func(t *testing.T) {
		t.Parallel()
		workflows := ucmocks.NewMockWorkflows(t)
		uc := usecase.NewOrderUseCase(usecase.OrderDeps{Workflows: workflows, Clock: time.Now, Logger: newTestLogger(t)})

		require.NoError(t, uc.StartResaleEntryWorkflow(ctx, entry(entity.ResaleEntryStateLost, entity.ResaleEntryLostReasonPaymentNotCompletedBeforeShow)))
	})
}
