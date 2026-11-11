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
	// language is the fan's preferred language; empty means none.
	language string
	uc       usecase.NotificationUseCase
}

// endedNow is the time the ended-message tests' clock reads.
var endedNow = time.Date(2026, 11, 1, 3, 0, 0, 0, time.UTC)

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
			Now:               func() time.Time { return endedNow },
			Users:             f.users,
			Concerts:          stubConcerts{concert: concert},
			Stocks:            f.stocks,
			ResaleEntries:     f.entries,
			ResaleEntryMailer: f.mailer,
			TimeZone:          tsJST,
		}, usecase.NewConcertsDeps{}, usecase.SubjectDeps{Now: func() time.Time { return endedNow }})
	return f
}

// ended makes entry-1 of fan-1 for 2 S席 tickets readable in the state with
// the reason.
func (f *endedFixture) ended(state entity.ResaleEntryState, reason entity.ResaleEntryLostReason) {
	f.entries.EXPECT().Get(mock.Anything, entity.ResaleEntryID("entry-1")).Return(&entity.ResaleEntry{
		ID: "entry-1", OrderID: "order-1", State: state, LostReason: reason,
		Order: &entity.Order{ID: "order-1", UserID: "fan-1", TicketStockID: "stock-1", TicketCount: 2, Status: entity.OrderStatusVoid},
	}, nil)
	f.users.EXPECT().Get(mock.Anything, "fan-1").Return(&entity.User{ID: "fan-1", Email: "fan@example.com", PreferredLanguage: f.language}, nil).Maybe()
	f.stocks.EXPECT().Get(mock.Anything, entity.TicketStockID("stock-1"), endedNow).Return(&entity.TicketStock{
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

// expectPush expects one push of the type for fan-1, named after the type and
// the entry, whose subject is the entry, and returns the started request.
func (f *endedFixture) expectPush(typ entity.NotificationType) *usecase.DeliverRequest {
	var got usecase.DeliverRequest
	want := usecase.DeliverRequest{UserID: "fan-1", Type: typ, Subject: entity.NotificationSubject{ResaleEntryID: "entry-1"}}
	f.workflows.EXPECT().Start(mock.Anything, usecase.WorkflowDeliverNotification, "deliver:"+string(typ)+":entry-1", want).
		RunAndReturn(func(_ context.Context, _, _ string, input any, _ ...usecase.StartOption) error {
			got = input.(usecase.DeliverRequest)
			return nil
		}).Once()
	return &got
}

// pushedFor runs the real Deliver of the started request with the concert open
// for resale or not, in the language, and returns the message the browser
// receives.
func (f *endedFixture) pushedFor(t *testing.T, req usecase.DeliverRequest, start time.Time, reason entity.ResaleEntryLostReason, language string) *entity.NotificationPayload {
	t.Helper()
	r := newSubjectFixture(t)
	r.now = func() time.Time { return endedNow }
	r.fanReadable(renderedFan("fan-1", language))
	r.resaleEntryReadable(&entity.ResaleEntry{ID: "entry-1", OrderID: "order-1", LostReason: reason}, "event-1")
	concert := renderedConcert("event-1")
	concert.StartTime = &start
	r.concertsReadable(concert)
	return r.push(t, req)
}

func TestNotificationUseCase_SendResaleEntryEnded(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	open := endedNow.Add(72 * time.Hour)
	closed := endedNow.Add(-time.Hour)

	t.Run("lapsed entry", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/notification/send-resale-entry-ended "Lapsed entry"
		// @spec components/usecase/notification/deliver "Resale entry ended"
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
		msg := f.pushedFor(t, *push, open, entity.ResaleEntryLostReasonUnspecified, "ja")
		assert.Contains(t, msg.Body, "再度、リセール申し込みをお願いします。", "the message invites them to apply again")
		assert.Equal(t, "/resale-entries", msg.Data[entity.NotificationDataKeyURL])
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
		assert.Contains(t, f.pushedFor(t, *push, closed, entity.ResaleEntryLostReasonNoTicketLeft, "ja").Body, "チケットの割り当てはありませんでした。")
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
		msg := f.pushedFor(t, *push, open, entity.ResaleEntryLostReasonCardNotCharged, "ja")
		assert.Contains(t, msg.Body, "別のカードで再度お申し込みいただけます。")
		assert.Contains(t, msg.Body, "請求はありません")
		assert.Equal(t, "/resale-entries", msg.Data[entity.NotificationDataKeyURL])
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
		assert.Contains(t, f.pushedFor(t, *push, open, entity.ResaleEntryLostReasonConcertCancelled, "ja").Body, "公演中止")
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

	t.Run("listing cancelled", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/notification/send-resale-entry-ended "Listing cancelled"
		f := newEndedFixture(t, open)
		f.ended(entity.ResaleEntryStateLost, entity.ResaleEntryLostReasonListingCancelled)
		var mail entity.ResaleEntryEndedEmail
		f.sends(&mail)
		push := f.expectPush(entity.NotificationTypeResaleEntryLost)

		require.NoError(t, f.uc.SendResaleEntryEnded(ctx, "entry-1"))

		assert.Contains(t, mail.TextBody, "チケットを販売できなくなったため、当選を取り消しました")
		assert.Contains(t, mail.TextBody, "全額をカードに返金します")
		assert.NotContains(t, mail.TextBody, "請求はありません", "the card was charged")
		assert.NotContains(t, mail.TextBody, "受付は終了しました", "resale may still be open")
		msg := f.pushedFor(t, *push, open, entity.ResaleEntryLostReasonListingCancelled, "ja")
		assert.Contains(t, msg.Body, "当選を取り消しました")
		assert.Contains(t, msg.Body, "カードに返金します")
		assert.NotContains(t, msg.Body, "請求はありません")
	})

	t.Run("listing cancelled in English", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/notification/send-resale-entry-ended "Listing cancelled"
		f := newEndedFixture(t, open)
		f.language = "en"
		f.ended(entity.ResaleEntryStateLost, entity.ResaleEntryLostReasonListingCancelled)
		var mail entity.ResaleEntryEndedEmail
		f.sends(&mail)
		push := f.expectPush(entity.NotificationTypeResaleEntryLost)

		require.NoError(t, f.uc.SendResaleEntryEnded(ctx, "entry-1"))

		assert.Contains(t, mail.TextBody, "could no longer be sold, so the win was cancelled")
		assert.Contains(t, mail.TextBody, "refunded to your card in full")
		assert.NotContains(t, mail.TextBody, "was not charged")
		msg := f.pushedFor(t, *push, open, entity.ResaleEntryLostReasonListingCancelled, "en")
		assert.Contains(t, msg.Body, "refunded to your card")
		assert.NotContains(t, msg.Body, "not charged")
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

	t.Run("win reverted for a cancelled concert is released", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/order/release-card-authorization "Win reverted for a cancelled concert"
		workflows := ucmocks.NewMockWorkflows(t)
		uc := usecase.NewOrderUseCase(usecase.OrderDeps{Workflows: workflows, Clock: time.Now, Logger: newTestLogger(t)})
		workflows.EXPECT().Start(mock.Anything, usecase.WorkflowReleaseCardAuthorization, "release:order-1", entity.OrderID("order-1"), mock.Anything, mock.Anything).
			Return(nil).Once()

		require.NoError(t, uc.StartResaleEntryWorkflow(ctx, entry(entity.ResaleEntryStateLost, entity.ResaleEntryLostReasonConcertCancelled)))
	})

	t.Run("win reverted after a refund is not released", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/order/release-card-authorization "Win reverted after a refund"
		workflows := ucmocks.NewMockWorkflows(t)
		uc := usecase.NewOrderUseCase(usecase.OrderDeps{Workflows: workflows, Clock: time.Now, Logger: newTestLogger(t)})

		require.NoError(t, uc.StartResaleEntryWorkflow(ctx, entry(entity.ResaleEntryStateLost, entity.ResaleEntryLostReasonListingCancelled)))
		workflows.AssertNotCalled(t, "Start", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	})
}
