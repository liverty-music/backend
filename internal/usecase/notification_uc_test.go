package usecase_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase"
)

// deliveryOutcomeCall captures one RecordDeliveryOutcome invocation.
type deliveryOutcomeCall struct{ outcome, reason string }

// captureMetrics is a PushMetrics spy that records delivery-outcome calls so a
// test can assert the operational signal was emitted with the right labels.
type captureMetrics struct {
	outcomes []deliveryOutcomeCall
}

func (m *captureMetrics) RecordPushSend(_ context.Context, _ string) {}

func (m *captureMetrics) RecordDeliveryOutcome(_ context.Context, outcome, reason string) {
	m.outcomes = append(m.outcomes, deliveryOutcomeCall{outcome: outcome, reason: reason})
}

// newCaptureLogger returns a JSON logger writing into the returned buffer so a
// test can assert on emitted log lines.
func newCaptureLogger(t *testing.T) (*logging.Logger, *bytes.Buffer) {
	t.Helper()
	buf := &bytes.Buffer{}
	logger, err := logging.New(
		logging.WithFormat(logging.FormatJSON),
		logging.WithLevel(slog.LevelInfo),
		logging.WithWriter(buf),
	)
	require.NoError(t, err)
	return logger, buf
}

func sub(userID, endpoint string) *entity.PushSubscription {
	return &entity.PushSubscription{UserID: userID, Endpoint: endpoint, P256dh: "p", Auth: "a"}
}

// newConcertsFixture is a subjectFixture in which user-1 (Japanese) can be sent
// a new_concerts notification of artist-1 and event-1.
func newConcertsFixture(t *testing.T) (*subjectFixture, usecase.DeliverRequest) {
	t.Helper()
	f := newSubjectFixture(t)
	f.fanReadable(renderedFan("user-1", "ja"))
	f.artists.EXPECT().Get(mock.Anything, "artist-1").Return(&entity.Artist{ID: "artist-1", Name: "Vaundy"}, nil).Maybe()
	f.concertsReadable(renderedConcert("event-1"))
	return f, usecase.DeliverRequest{
		UserID:  "user-1",
		Type:    entity.NotificationTypeNewConcerts,
		Subject: entity.NotificationSubject{ArtistID: "artist-1", EventIDs: []string{"event-1"}},
	}
}

// capturePushes accepts each send and appends the pushed message to the result.
func (f *subjectFixture) capturePushes(times int) *[]*capturedPush {
	pushed := &[]*capturedPush{}
	f.sender.EXPECT().Send(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, msg []byte, s *entity.PushSubscription) error {
			*pushed = append(*pushed, &capturedPush{raw: msg, endpoint: s.Endpoint})
			return nil
		}).Times(times)
	return pushed
}

// capturedPush is one send to one browser.
type capturedPush struct {
	raw      []byte
	endpoint string
}

// sendFails makes the send to the endpoint fail with err.
func (f *subjectFixture) sendFails(endpoint string, err error) {
	f.sender.EXPECT().
		Send(mock.Anything, mock.Anything, mock.MatchedBy(func(s *entity.PushSubscription) bool { return s.Endpoint == endpoint })).
		Return(err).Once()
}

// sendAccepted makes the send to the endpoint succeed.
func (f *subjectFixture) sendAccepted(endpoint string) {
	f.sender.EXPECT().
		Send(mock.Anything, mock.Anything, mock.MatchedBy(func(s *entity.PushSubscription) bool { return s.Endpoint == endpoint })).
		Return(nil).Once()
}

// @spec components/usecase/notification/deliver "Message identifies its notification"
// @spec components/usecase/notification/deliver "Delivered"
func TestDeliver_Delivered(t *testing.T) {
	t.Parallel()

	f, req := newConcertsFixture(t)
	stored := f.recordsAs("01890000-0000-7000-8000-000000000abc")
	f.browsers("user-1", "https://push/1", "https://push/2")
	pushed := f.capturePushes(2)

	outcome, err := f.uc.Deliver(context.Background(), req)

	require.NoError(t, err)
	assert.Equal(t, usecase.DeliveryOutcome{NotificationID: "01890000-0000-7000-8000-000000000abc", Status: usecase.DeliveryStatusDelivered}, outcome)
	assert.Equal(t, "user-1", stored.UserID)
	assert.Equal(t, entity.NotificationTypeNewConcerts, stored.Type)
	assert.Equal(t, req.Subject, stored.Subject)
	// The same message, carrying the id, reaches every browser.
	require.Len(t, *pushed, 2)
	assert.ElementsMatch(t, []string{"https://push/1", "https://push/2"}, []string{(*pushed)[0].endpoint, (*pushed)[1].endpoint})
	assert.Equal(t, (*pushed)[0].raw, (*pushed)[1].raw)
	for _, p := range *pushed {
		msg := decodeMessage(t, p.raw)
		assert.Equal(t, "01890000-0000-7000-8000-000000000abc", msg.Data[entity.NotificationDataKeyNotificationID])
		assert.Equal(t, "[Vaundy] 新着ライブ発表!!", msg.Title)
	}
	// Logged for usage analytics with the fan, the type and the id.
	logs := f.logs.String()
	assert.Contains(t, logs, "notification delivered")
	assert.Contains(t, logs, `"topic":"notification_delivery"`)
	assert.Contains(t, logs, `"user_id":"user-1"`)
	assert.Contains(t, logs, `"notification_type":"new_concerts"`)
	assert.Contains(t, logs, `"notification_id":"01890000-0000-7000-8000-000000000abc"`)
}

// @spec components/usecase/notification/deliver "Message names its type"
func TestDeliver_MessageNamesType(t *testing.T) {
	t.Parallel()

	f := newSubjectFixture(t)
	f.fanReadable(renderedFan("user-1", "ja"))
	f.ticketSales.EXPECT().Get(mock.Anything, entity.TicketSaleID("sale-1")).Return(&entity.TicketSale{
		ID: "sale-1", Method: entity.TicketSaleMethodLottery,
		SaleStartTime: time.Date(2026, 10, 5, 18, 0, 0, 0, tsJST),
		SaleEndTime:   time.Date(2026, 10, 22, 23, 59, 0, 0, tsJST),
	}, nil).Once()
	f.concertsReadable(renderedConcert("event-1"))
	f.recordsAs("notif-type-1")
	f.browsers("user-1", "https://push/1", "https://push/2")
	pushed := f.capturePushes(2)

	_, err := f.uc.Deliver(context.Background(), usecase.DeliverRequest{
		UserID: "user-1",
		Type:   entity.NotificationTypeSalesReminder,
		Subject: entity.NotificationSubject{
			TicketSaleID: "sale-1", Stage: entity.ReminderStageApplyClose24H, EventID: "event-1",
		},
	})

	require.NoError(t, err)
	require.Len(t, *pushed, 2)
	for _, p := range *pushed {
		assert.Equal(t, map[string]string{
			"url":             "/concerts/event-1",
			"notification_id": "notif-type-1",
			"type":            "sales_reminder",
			"ticket_sale_id":  "sale-1",
			"milestone":       "apply_close_24h",
		}, decodeMessage(t, p.raw).Data)
	}
}

// A type other than sales_reminder carries no sale or stage.
func TestDeliver_OnlyAReminderNamesASaleAndStage(t *testing.T) {
	t.Parallel()

	f, req := newConcertsFixture(t)
	msg := f.push(t, req)

	assert.Equal(t, "new_concerts", msg.Data[entity.NotificationDataKeyType])
	assert.NotContains(t, msg.Data, entity.NotificationDataKeyTicketSaleID)
	assert.NotContains(t, msg.Data, entity.NotificationDataKeyMilestone)
}

// @spec components/usecase/notification/deliver "Every send fails"
// @spec components/usecase/notification/deliver "Delivery failed"
func TestDeliver_EverySendFails(t *testing.T) {
	t.Parallel()

	f, req := newConcertsFixture(t)
	f.recordsAs("id-fail")
	f.browsers("user-1", "https://push/1", "https://push/2")
	f.sendFails("https://push/1", errors.New("first rejected"))
	f.sendFails("https://push/2", errors.New("last rejected"))

	outcome, err := f.uc.Deliver(context.Background(), req)

	require.NoError(t, err, "a Failed outcome does not fail Deliver")
	assert.Equal(t, usecase.DeliveryOutcome{NotificationID: "id-fail", Status: usecase.DeliveryStatusFailed, FailureReason: "last rejected"}, outcome)
	// The stored Notification is unchanged: Create is the only repository call.
	assert.Len(t, f.notifs.Calls, 1)
	f.notifs.AssertCalled(t, "Create", mock.Anything, mock.Anything)
}

// Only the dead subscription is removed; the fan's other browser keeps its
// subscription and, with no accepted send, the outcome is Failed.
//
// @spec components/usecase/notification/deliver "Browser gone"
func TestDeliver_BrowserGone(t *testing.T) {
	t.Parallel()

	f, req := newConcertsFixture(t)
	f.recordsAs("id-gone")
	f.browsers("user-1", "https://push/gone", "https://push/other")
	f.sendFails("https://push/gone", apperr.New(codes.NotFound, "410 gone"))
	f.sendFails("https://push/other", errors.New("push service rejected"))
	// Only the dead (user, endpoint) pair is removed (any other call would fail
	// the mock), and the removal is not the fan's own, so it goes through
	// DeleteGone, which announces no unsubscription.
	f.pushSubs.EXPECT().DeleteGone(mock.Anything, "user-1", "https://push/gone").Return(nil).Once()

	outcome, err := f.uc.Deliver(context.Background(), req)

	require.NoError(t, err)
	assert.Equal(t, usecase.DeliveryStatusFailed, outcome.Status)
}

// @spec components/usecase/notification/deliver "No browser registered"
func TestDeliver_NoBrowserRegistered(t *testing.T) {
	t.Parallel()

	f, req := newConcertsFixture(t)
	f.recordsAs("id-nosub")
	f.browsers("user-1")

	outcome, err := f.uc.Deliver(context.Background(), req)

	require.NoError(t, err)
	assert.Equal(t, usecase.DeliveryOutcome{NotificationID: "id-nosub", Status: usecase.DeliveryStatusFailed, FailureReason: "no active push subscription"}, outcome)
	f.sender.AssertNotCalled(t, "Send", mock.Anything, mock.Anything, mock.Anything)
}

// The subscriptions cannot be read: the outcome is Failed with the listing
// error, not an error, because the Notification is already recorded.
func TestDeliver_ListingFails(t *testing.T) {
	t.Parallel()

	f, req := newConcertsFixture(t)
	f.recordsAs("id-list")
	f.pushSubs.EXPECT().ListByUserIDs(mock.Anything, []string{"user-1"}).Return(nil, errors.New("db down")).Once()

	outcome, err := f.uc.Deliver(context.Background(), req)

	require.NoError(t, err)
	assert.Equal(t, usecase.DeliveryStatusFailed, outcome.Status)
	assert.Contains(t, outcome.FailureReason, "db down")
}

// A request cancelled before the first send sends nothing and is Failed; the
// record still exists.
func TestDeliver_ContextCancelledStopsDispatch(t *testing.T) {
	t.Parallel()

	f, req := newConcertsFixture(t)
	f.recordsAs("id-cancel")
	f.browsers("user-1", "https://push/1")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	outcome, err := f.uc.Deliver(ctx, req)

	require.NoError(t, err)
	assert.Equal(t, usecase.DeliveryStatusFailed, outcome.Status)
	assert.NotEmpty(t, outcome.FailureReason)
	f.sender.AssertNotCalled(t, "Send", mock.Anything, mock.Anything, mock.Anything)
}

// @spec components/usecase/notification/deliver "Recording fails"
func TestDeliver_RecordingFails(t *testing.T) {
	t.Parallel()

	f, req := newConcertsFixture(t)
	f.notifs.EXPECT().Create(mock.Anything, mock.Anything).Return(apperr.New(codes.Unavailable, "db down")).Once()

	_, err := f.uc.Deliver(context.Background(), req)

	require.ErrorIs(t, err, apperr.ErrUnavailable, "the request is processed again")
	f.pushSubs.AssertNotCalled(t, "ListByUserIDs", mock.Anything, mock.Anything)
	f.sender.AssertNotCalled(t, "Send", mock.Anything, mock.Anything, mock.Anything)
}

// @spec components/usecase/notification/deliver "Subject missing a record"
func TestDeliver_SubjectMissingARecord(t *testing.T) {
	t.Parallel()

	f := newSubjectFixture(t)

	_, err := f.uc.Deliver(context.Background(), usecase.DeliverRequest{
		UserID: "user-1", Type: entity.NotificationTypeLotteryLoss, Subject: entity.NotificationSubject{},
	})

	require.ErrorIs(t, err, apperr.ErrInvalidArgument)
	f.notifs.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
	f.sender.AssertNotCalled(t, "Send", mock.Anything, mock.Anything, mock.Anything)
}

// @spec components/usecase/notification/deliver "Subject deleted before delivery"
func TestDeliver_SubjectDeletedBeforeDelivery(t *testing.T) {
	t.Parallel()

	f := newSubjectFixture(t)
	f.fanReadable(renderedFan("user-1", "ja"))
	f.orders.EXPECT().Get(mock.Anything, entity.OrderID("order-1")).Return(nil, apperr.New(codes.NotFound, "order not found")).Once()

	outcome, err := f.uc.Deliver(context.Background(), usecase.DeliverRequest{
		UserID: "user-1", Type: entity.NotificationTypeConcertCancelled, Subject: entity.NotificationSubject{OrderID: "order-1"},
	})

	require.NoError(t, err)
	assert.Equal(t, usecase.DeliveryOutcome{Status: usecase.DeliveryStatusFailed, FailureReason: "subject no longer exists"}, outcome)
	f.notifs.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
	f.sender.AssertNotCalled(t, "Send", mock.Anything, mock.Anything, mock.Anything)
	assert.Contains(t, f.metrics.outcomes, deliveryOutcomeCall{outcome: "failed", reason: "subject_gone"})
}

// A concert the subject names that no longer exists makes the subject gone too.
func TestDeliver_ConcertDeletedBeforeDelivery(t *testing.T) {
	t.Parallel()

	f := newSubjectFixture(t)
	f.fanReadable(renderedFan("user-1", "ja"))
	f.artists.EXPECT().Get(mock.Anything, "artist-1").Return(&entity.Artist{ID: "artist-1", Name: "Vaundy"}, nil).Once()
	f.concertsReadable(renderedConcert("event-1"))

	outcome, err := f.uc.Deliver(context.Background(), usecase.DeliverRequest{
		UserID: "user-1", Type: entity.NotificationTypeNewConcerts,
		Subject: entity.NotificationSubject{ArtistID: "artist-1", EventIDs: []string{"event-1", "event-deleted"}},
	})

	require.NoError(t, err)
	assert.Equal(t, usecase.NotificationFailureReasonSubjectGone, outcome.FailureReason)
	f.notifs.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
}

// A read that fails for another reason fails Deliver, so the request is
// processed again; nothing is recorded or sent.
func TestDeliver_ReadFails(t *testing.T) {
	t.Parallel()

	req := usecase.DeliverRequest{
		UserID: "user-1", Type: entity.NotificationTypeConcertCancelled, Subject: entity.NotificationSubject{OrderID: "order-1"},
	}

	t.Run("subject record", func(t *testing.T) {
		t.Parallel()
		f := newSubjectFixture(t)
		f.fanReadable(renderedFan("user-1", "ja"))
		f.orders.EXPECT().Get(mock.Anything, entity.OrderID("order-1")).Return(nil, apperr.New(codes.Unavailable, "db down")).Once()

		_, err := f.uc.Deliver(context.Background(), req)

		require.ErrorIs(t, err, apperr.ErrUnavailable)
		f.notifs.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
	})

	t.Run("the fan", func(t *testing.T) {
		t.Parallel()
		f := newSubjectFixture(t)
		f.users.EXPECT().Get(mock.Anything, "user-1").Return(nil, apperr.New(codes.Unavailable, "db down")).Once()

		_, err := f.uc.Deliver(context.Background(), req)

		require.ErrorIs(t, err, apperr.ErrUnavailable)
		f.notifs.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
	})

	t.Run("the fan's follows", func(t *testing.T) {
		t.Parallel()
		f := newSubjectFixture(t)
		f.users.EXPECT().Get(mock.Anything, "user-1").Return(renderedFan("user-1", "ja"), nil).Once()
		f.follows.EXPECT().ListByUser(mock.Anything, "user-1").Return(nil, apperr.New(codes.Unavailable, "db down")).Once()

		_, err := f.uc.Deliver(context.Background(), req)

		require.ErrorIs(t, err, apperr.ErrUnavailable)
		f.notifs.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
	})
}

// @spec components/usecase/notification/deliver "One of two browsers accepts"
func TestDeliver_OneOfTwoBrowsersAccepts(t *testing.T) {
	t.Parallel()

	f, req := newConcertsFixture(t)
	f.recordsAs("id-partial")
	f.browsers("user-1", "https://push/bad", "https://push/good")
	f.sendFails("https://push/bad", errors.New("push service rejected"))
	f.sendAccepted("https://push/good")

	outcome, err := f.uc.Deliver(context.Background(), req)

	require.NoError(t, err)
	assert.Equal(t, usecase.DeliveryOutcome{NotificationID: "id-partial", Status: usecase.DeliveryStatusDelivered}, outcome)
}

// @spec components/usecase/notification/deliver "Cancelled after one accepted send"
func TestDeliver_CancelledAfterOneAcceptedSend(t *testing.T) {
	t.Parallel()

	f, req := newConcertsFixture(t)
	f.recordsAs("id-cancel-after")
	f.browsers("user-1", "https://push/1", "https://push/2", "https://push/3")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	// The first send is accepted and cancels the request; a second send would
	// fail the mock.
	f.sender.EXPECT().Send(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(context.Context, []byte, *entity.PushSubscription) error {
			cancel()
			return nil
		}).Once()

	outcome, err := f.uc.Deliver(ctx, req)

	require.NoError(t, err)
	assert.Equal(t, usecase.DeliveryStatusDelivered, outcome.Status)
	f.sender.AssertNumberOfCalls(t, "Send", 1)
}

// Observability: a failed delivery emits the WARNING log (with the bounded
// failure_reason label) and the delivery-outcome metric, so the failure is
// detectable without storing the outcome.
func TestDeliver_FailedDeliveryEmitsWarningLogAndMetric(t *testing.T) {
	t.Parallel()

	f, req := newConcertsFixture(t)
	f.recordsAs("id-obs")
	f.browsers("user-1")

	_, err := f.uc.Deliver(context.Background(), req)
	require.NoError(t, err)

	require.Contains(t, f.metrics.outcomes, deliveryOutcomeCall{outcome: "failed", reason: "no_subscription"})
	logs := f.logs.String()
	assert.Contains(t, logs, `"level":"WARN"`)
	assert.Contains(t, logs, "notification delivery failed")
	assert.Contains(t, logs, `"failure_reason":"no_subscription"`)
	assert.Contains(t, logs, `"notification_id":"id-obs"`)
}

// Observability: the success path emits the delivered metric and no WARNING.
func TestDeliver_SuccessEmitsNoWarningLog(t *testing.T) {
	t.Parallel()

	f, req := newConcertsFixture(t)
	f.push(t, req)

	require.Contains(t, f.metrics.outcomes, deliveryOutcomeCall{outcome: "delivered", reason: "none"})
	assert.NotContains(t, f.logs.String(), "notification delivery failed")
	assert.NotContains(t, f.logs.String(), `"level":"WARN"`)
}

// lossNotification is a stored lottery_loss Notification of user-1 for entry-1.
func lossNotification(id string) *entity.Notification {
	return &entity.Notification{
		ID: id, UserID: "user-1", Type: entity.NotificationTypeLotteryLoss,
		Subject:    entity.NotificationSubject{LotteryEntryID: "entry-1"},
		CreateTime: time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC),
	}
}

// lossFixtureForList makes entry-1 of event-1 readable with no follows.
func lossFixtureForList(t *testing.T) *subjectFixture {
	t.Helper()
	f := newSubjectFixture(t)
	f.lotteryEntryReadable(&entity.LotteryEntry{
		ID: "entry-1", OrderID: "order-1", State: entity.LotteryEntryStateLost, LostReason: entity.LotteryEntryLostReasonNotDrawn,
	}, "event-1")
	f.concertsReadable(renderedConcert("event-1"))
	return f
}

func TestNotificationUseCase_List(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("failed push still listed", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/notification/list "Failed push still listed"
		f := lossFixtureForList(t)
		fan := renderedFan("user-1", "ja")
		f.fanReadable(fan)
		stored := f.recordsAs("notif-1")
		f.browsers("user-1") // No browser registered: the push fails.

		outcome, err := f.uc.Deliver(ctx, usecase.DeliverRequest{
			UserID: "user-1", Type: entity.NotificationTypeLotteryLoss, Subject: entity.NotificationSubject{LotteryEntryID: "entry-1"},
		})
		require.NoError(t, err)
		require.Equal(t, usecase.DeliveryStatusFailed, outcome.Status)
		f.notifs.EXPECT().ListByUser(mock.Anything, "user-1", 50).Return([]*entity.Notification{stored}, nil).Once()

		listed, err := f.uc.List(ctx, fan)

		require.NoError(t, err)
		require.Len(t, listed, 1)
		assert.Equal(t, "notif-1", listed[0].Notification.ID)
		assert.Equal(t, "[Vaundy] 抽選結果のお知らせ", listed[0].Message.Title)
		assert.Equal(t, "11/20のZepp Shinjuku公演のチケット抽選が落選となりました。\nLiverty Live 2026", listed[0].Message.Body)
	})

	t.Run("language changed since the push", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/notification/list "Language changed since the push"
		f := lossFixtureForList(t)
		f.follows.EXPECT().ListByUser(mock.Anything, "user-1").Return(nil, nil).Once()
		f.notifs.EXPECT().ListByUser(mock.Anything, "user-1", 50).Return([]*entity.Notification{lossNotification("notif-1")}, nil).Once()

		// List is given the fan as they are now; the language at the push is not
		// stored on the Notification.
		listed, err := f.uc.List(ctx, renderedFan("user-1", "en"))

		require.NoError(t, err)
		require.Len(t, listed, 1)
		assert.Equal(t, "[Vaundy] Ticket Lottery Result", listed[0].Message.Title)
		assert.Equal(t, "Your lottery entry for Zepp Shinjuku on Nov 20 was not drawn.\nLiverty Live 2026", listed[0].Message.Body)
	})

	t.Run("read and unread", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/notification/list "Read and unread"
		f := lossFixtureForList(t)
		f.follows.EXPECT().ListByUser(mock.Anything, "user-1").Return(nil, nil).Once()
		readAt := time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)
		read := lossNotification("notif-read")
		read.ReadTime = &readAt
		f.notifs.EXPECT().ListByUser(mock.Anything, "user-1", 50).
			Return([]*entity.Notification{lossNotification("notif-unread"), read}, nil).Once()

		listed, err := f.uc.List(ctx, renderedFan("user-1", "ja"))

		require.NoError(t, err)
		require.Len(t, listed, 2)
		assert.Equal(t, "notif-unread", listed[0].Notification.ID, "newest first, as read")
		assert.Nil(t, listed[0].Notification.ReadTime)
		assert.Equal(t, "notif-read", listed[1].Notification.ID)
		require.NotNil(t, listed[1].Notification.ReadTime)
		assert.True(t, readAt.Equal(*listed[1].Notification.ReadTime))
		assert.False(t, listed[0].Notification.CreateTime.IsZero(), "with its create time")
	})

	t.Run("subject deleted", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/notification/list "Subject deleted"
		f := lossFixtureForList(t)
		f.follows.EXPECT().ListByUser(mock.Anything, "user-1").Return(nil, nil).Once()
		f.orders.EXPECT().Get(mock.Anything, entity.OrderID("order-gone")).Return(nil, apperr.New(codes.NotFound, "order not found")).Once()
		f.notifs.EXPECT().ListByUser(mock.Anything, "user-1", 50).Return([]*entity.Notification{
			{ID: "notif-gone", UserID: "user-1", Type: entity.NotificationTypeConcertCancelled, Subject: entity.NotificationSubject{OrderID: "order-gone"}},
			lossNotification("notif-kept"),
		}, nil).Once()

		listed, err := f.uc.List(ctx, renderedFan("user-1", "ja"))

		require.NoError(t, err)
		require.Len(t, listed, 1)
		assert.Equal(t, "notif-kept", listed[0].Notification.ID)
	})

	t.Run("no notification", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/notification/list "No notification"
		f := newSubjectFixture(t)
		f.notifs.EXPECT().ListByUser(mock.Anything, "user-1", 50).Return(nil, nil).Once()

		listed, err := f.uc.List(ctx, renderedFan("user-1", "ja"))

		require.NoError(t, err)
		assert.NotNil(t, listed)
		assert.Empty(t, listed)
	})

	t.Run("listing fails", func(t *testing.T) {
		t.Parallel()
		f := newSubjectFixture(t)
		f.notifs.EXPECT().ListByUser(mock.Anything, "user-1", 50).Return(nil, apperr.New(codes.Unavailable, "db down")).Once()

		_, err := f.uc.List(ctx, renderedFan("user-1", "ja"))

		assert.ErrorIs(t, err, apperr.ErrUnavailable)
	})

	t.Run("a subject read fails", func(t *testing.T) {
		t.Parallel()
		f := newSubjectFixture(t)
		f.follows.EXPECT().ListByUser(mock.Anything, "user-1").Return(nil, nil).Once()
		f.entries.EXPECT().Get(mock.Anything, entity.LotteryEntryID("entry-1")).Return(nil, apperr.New(codes.Unavailable, "db down")).Once()
		f.notifs.EXPECT().ListByUser(mock.Anything, "user-1", 50).Return([]*entity.Notification{lossNotification("notif-1")}, nil).Once()

		_, err := f.uc.List(ctx, renderedFan("user-1", "ja"))

		assert.ErrorIs(t, err, apperr.ErrUnavailable)
	})
}

// Read idempotency: MarkRead loads the record, confirms ownership, and delegates
// the idempotent set to the repository.
func TestMarkRead_OwnedDelegatesToRepo(t *testing.T) {
	t.Parallel()

	f := newSubjectFixture(t)
	f.notifs.EXPECT().Get(anyCtx, "n-1").Return(&entity.Notification{ID: "n-1", UserID: "user-1"}, nil)
	f.notifs.EXPECT().MarkRead(anyCtx, "user-1", "n-1").Return(nil)

	require.NoError(t, f.uc.MarkRead(context.Background(), "user-1", "n-1"))
}

// Cross-user rejection: marking another user's notification is PermissionDenied
// and never reaches the repository mutation.
func TestMarkRead_CrossUserRejected(t *testing.T) {
	t.Parallel()

	f := newSubjectFixture(t)
	f.notifs.EXPECT().Get(anyCtx, "n-1").Return(&entity.Notification{ID: "n-1", UserID: "owner"}, nil)

	err := f.uc.MarkRead(context.Background(), "attacker", "n-1")

	require.ErrorIs(t, err, apperr.ErrPermissionDenied)
	f.notifs.AssertNotCalled(t, "MarkRead", mock.Anything, mock.Anything, mock.Anything)
}
