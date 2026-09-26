package usecase_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/liverty-music/backend/internal/entity"
	entitymocks "github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/usecase"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
)

// reminderDeliveryTestDeps holds all dependencies for
// SalesReminderDeliveryUseCase tests. DeliverReminder stays synchronous and
// composes the same entity ports NotificationUseCase.Deliver uses, directly
// (see notification_delivery.go) rather than through that interface, so these
// tests mock the entity ports rather than a NotificationUseCase.
type reminderDeliveryTestDeps struct {
	reminderRepo *entitymocks.MockSalesPhaseReminderRepository
	notifRepo    *entitymocks.MockNotificationRepository
	pushSubRepo  *entitymocks.MockPushSubscriptionRepository
	sender       *entitymocks.MockPushNotificationSender
	publisher    *ucmocks.MockEventPublisher
	uc           usecase.SalesReminderDeliveryUseCase
}

func newReminderDeliveryTestDeps(t *testing.T) *reminderDeliveryTestDeps {
	t.Helper()
	d := &reminderDeliveryTestDeps{
		reminderRepo: entitymocks.NewMockSalesPhaseReminderRepository(t),
		notifRepo:    entitymocks.NewMockNotificationRepository(t),
		pushSubRepo:  entitymocks.NewMockPushSubscriptionRepository(t),
		sender:       entitymocks.NewMockPushNotificationSender(t),
		publisher:    ucmocks.NewMockEventPublisher(t),
	}
	d.uc = usecase.NewSalesReminderDeliveryUseCase(
		d.reminderRepo,
		d.notifRepo,
		d.pushSubRepo,
		d.sender,
		d.publisher,
		noopMetrics{},
		newTestLogger(t),
	)
	return d
}

// expectDelivered sets up the notifRepo/pushSubRepo/sender expectations for a
// successful delivery to userID: Create mints notifID, ListByUserIDs returns
// one subscription, and Send accepts it — the same "record then send" path
// deliverNotification always takes.
func expectDelivered(t *testing.T, d *reminderDeliveryTestDeps, userID, notifID string) {
	t.Helper()
	d.notifRepo.EXPECT().
		Create(anyCtx, mock.Anything).
		Run(func(_ context.Context, n *entity.Notification) { n.ID = notifID }).
		Return(nil)
	d.pushSubRepo.EXPECT().
		ListByUserIDs(anyCtx, []string{userID}).
		Return([]*entity.PushSubscription{{UserID: userID, Endpoint: "https://push/1", P256dh: "p", Auth: "a"}}, nil)
	d.sender.EXPECT().
		Send(anyCtx, mock.Anything, mock.Anything).
		Return(nil)
	d.notifRepo.EXPECT().
		UpdateDelivery(anyCtx, notifID, entity.NotificationDeliveryStatusDelivered, mock.AnythingOfType("*time.Time"), "").
		Return(nil)
	d.publisher.EXPECT().
		PublishEvent(anyCtx, entity.SubjectNotificationDelivered, mock.Anything).
		Return(nil).
		Maybe()
}

// expectNoSubscription sets up the notifRepo/pushSubRepo expectations for a
// delivery with no push subscription to send to — deliverNotification records
// it as failed with NotificationFailureReasonNoSubscription without calling Send.
func expectNoSubscription(t *testing.T, d *reminderDeliveryTestDeps, userID, notifID string) {
	t.Helper()
	d.notifRepo.EXPECT().
		Create(anyCtx, mock.Anything).
		Run(func(_ context.Context, n *entity.Notification) { n.ID = notifID }).
		Return(nil)
	d.pushSubRepo.EXPECT().
		ListByUserIDs(anyCtx, []string{userID}).
		Return([]*entity.PushSubscription{}, nil)
	d.notifRepo.EXPECT().
		UpdateDelivery(anyCtx, notifID, entity.NotificationDeliveryStatusFailed, (*time.Time)(nil), usecase.NotificationFailureReasonNoSubscription).
		Return(nil)
}

// expectTransientFailure sets up the notifRepo/pushSubRepo/sender expectations
// for a delivery whose only subscription's send fails transiently (not a 410).
func expectTransientFailure(t *testing.T, d *reminderDeliveryTestDeps, userID, notifID string) {
	t.Helper()
	d.notifRepo.EXPECT().
		Create(anyCtx, mock.Anything).
		Run(func(_ context.Context, n *entity.Notification) { n.ID = notifID }).
		Return(nil)
	d.pushSubRepo.EXPECT().
		ListByUserIDs(anyCtx, []string{userID}).
		Return([]*entity.PushSubscription{{UserID: userID, Endpoint: "https://push/1", P256dh: "p", Auth: "a"}}, nil)
	d.sender.EXPECT().
		Send(anyCtx, mock.Anything, mock.Anything).
		Return(errors.New("push service unavailable"))
	d.notifRepo.EXPECT().
		UpdateDelivery(anyCtx, notifID, entity.NotificationDeliveryStatusFailed, (*time.Time)(nil), mock.MatchedBy(func(s string) bool { return s != "" })).
		Return(nil)
}

// validPayload returns a non-nil NotificationPayload for happy-path tests.
func validPayload() *entity.NotificationPayload {
	return &entity.NotificationPayload{Title: "Ticket Sales Open", Body: "Apply now."}
}

// validDueData builds a SalesPhaseReminderDueData for the given stage with a
// non-nil payload.
func validDueData(stage entity.ReminderStage) entity.SalesPhaseReminderDueData {
	return entity.SalesPhaseReminderDueData{
		UserID:  "user-001",
		PhaseID: "phase-001",
		Stage:   int16(stage),
		Payload: validPayload(),
	}
}

// ---- AlreadySent guard (no send expected) ----

// TestDeliverReminder_AlreadySentSkipsWithoutSend verifies that the AlreadySent
// dedup guard returns nil without attempting a notification send.
func TestDeliverReminder_AlreadySentSkipsWithoutSend(t *testing.T) {
	t.Parallel()

	d := newReminderDeliveryTestDeps(t)
	d.reminderRepo.EXPECT().
		AlreadySent(anyCtx, "user-001", "phase-001", entity.ReminderStageApplyOpen).
		Return(true, nil)

	err := d.uc.DeliverReminder(context.Background(), validDueData(entity.ReminderStageApplyOpen))

	require.NoError(t, err)
	d.notifRepo.AssertNotCalled(t, "Create")
}

// ---- Infra-error returns ----

// TestDeliverReminder_AlreadySentErrorReturnsErr verifies that an AlreadySent
// repository error propagates and does not attempt a send.
func TestDeliverReminder_AlreadySentErrorReturnsErr(t *testing.T) {
	t.Parallel()

	d := newReminderDeliveryTestDeps(t)
	d.reminderRepo.EXPECT().
		AlreadySent(anyCtx, "user-001", "phase-001", entity.ReminderStageApplyOpen).
		Return(false, errors.New("db error"))

	err := d.uc.DeliverReminder(context.Background(), validDueData(entity.ReminderStageApplyOpen))

	require.Error(t, err)
	d.notifRepo.AssertNotCalled(t, "Create")
}

// TestDeliverReminder_DeliveryErrorReturnsErr verifies that a Notification
// record-creation failure (deliverNotification's "no record => no send"
// invariant) propagates.
func TestDeliverReminder_DeliveryErrorReturnsErr(t *testing.T) {
	t.Parallel()

	d := newReminderDeliveryTestDeps(t)
	d.reminderRepo.EXPECT().
		AlreadySent(anyCtx, "user-001", "phase-001", entity.ReminderStageApplyOpen).
		Return(false, nil)
	d.notifRepo.EXPECT().
		Create(anyCtx, mock.Anything).
		Return(errors.New("record creation failed"))

	err := d.uc.DeliverReminder(context.Background(), validDueData(entity.ReminderStageApplyOpen))

	require.Error(t, err)
}

// ---- nil-payload defensive skip ----

// TestDeliverReminder_NilPayloadSkips verifies the nil-payload defensive guard
// returns nil without attempting a send — no send was attempted.
func TestDeliverReminder_NilPayloadSkips(t *testing.T) {
	t.Parallel()

	d := newReminderDeliveryTestDeps(t)
	d.reminderRepo.EXPECT().
		AlreadySent(anyCtx, "user-001", "phase-001", entity.ReminderStageApplyOpen).
		Return(false, nil)

	data := entity.SalesPhaseReminderDueData{
		UserID:  "user-001",
		PhaseID: "phase-001",
		Stage:   int16(entity.ReminderStageApplyOpen),
		Payload: nil, // defensive skip
	}
	err := d.uc.DeliverReminder(context.Background(), data)

	require.NoError(t, err)
	d.notifRepo.AssertNotCalled(t, "Create")
}

// ---- Terminal delivery outcome: no_subscription ----

// TestDeliverReminder_NoSubscriptionRecordsSent verifies that a no-subscription
// outcome records sent (suppressing future re-delivery) and returns nil.
func TestDeliverReminder_NoSubscriptionRecordsSent(t *testing.T) {
	t.Parallel()

	d := newReminderDeliveryTestDeps(t)
	d.reminderRepo.EXPECT().
		AlreadySent(anyCtx, "user-001", "phase-001", entity.ReminderStageApplyClose24H).
		Return(false, nil)
	expectNoSubscription(t, d, "user-001", "id-nosub")
	d.reminderRepo.EXPECT().
		RecordSent(anyCtx, "user-001", "phase-001", entity.ReminderStageApplyClose24H).
		Return(nil).
		Once()

	err := d.uc.DeliverReminder(context.Background(), validDueData(entity.ReminderStageApplyClose24H))

	require.NoError(t, err)
}

// ---- Terminal delivery outcome: delivered ----

// TestDeliverReminder_SuccessfulSendRecordsSent verifies that a successful
// notification records sent and returns nil.
func TestDeliverReminder_SuccessfulSendRecordsSent(t *testing.T) {
	t.Parallel()

	d := newReminderDeliveryTestDeps(t)
	d.reminderRepo.EXPECT().
		AlreadySent(anyCtx, "user-001", "phase-001", entity.ReminderStageApplyOpen).
		Return(false, nil)
	expectDelivered(t, d, "user-001", "id-ok")
	d.reminderRepo.EXPECT().
		RecordSent(anyCtx, "user-001", "phase-001", entity.ReminderStageApplyOpen).
		Return(nil)

	err := d.uc.DeliverReminder(context.Background(), validDueData(entity.ReminderStageApplyOpen))

	require.NoError(t, err)
}

// ---- Terminal delivery outcome: failed ----

// TestDeliverReminder_TransientFailureDoesNotRecordSent verifies that on a
// transient failure (not the no-subscription sentinel) RecordSent is NOT called
// so the next scan can retry, and no error is returned.
func TestDeliverReminder_TransientFailureDoesNotRecordSent(t *testing.T) {
	t.Parallel()

	d := newReminderDeliveryTestDeps(t)
	d.reminderRepo.EXPECT().
		AlreadySent(anyCtx, "user-001", "phase-001", entity.ReminderStageResultDay).
		Return(false, nil)
	expectTransientFailure(t, d, "user-001", "id-transient")
	// RecordSent must NOT be called — leave the sent-log empty so the next scan retries.

	err := d.uc.DeliverReminder(context.Background(), validDueData(entity.ReminderStageResultDay))

	// Total failure is not returned as an error — the next scan will retry.
	require.NoError(t, err)
	d.reminderRepo.AssertNotCalled(t, "RecordSent")
}

// ---- phase_stage coverage across stages ----

// TestDeliverReminder_AllStagesDeliver verifies that DeliverReminder handles
// each ReminderStage value on the delivered path without error.
func TestDeliverReminder_AllStagesDeliver(t *testing.T) {
	t.Parallel()

	tests := []struct {
		stage     entity.ReminderStage
		wantStage string
	}{
		{entity.ReminderStageApplyOpen, "APPLY_OPEN"},
		{entity.ReminderStageApplyClose24H, "APPLY_CLOSE_24H"},
		{entity.ReminderStageApplyClose1H, "APPLY_CLOSE_1H"},
		{entity.ReminderStageResultDay, "RESULT_DAY"},
	}

	for _, tt := range tests {
		t.Run(tt.wantStage, func(t *testing.T) {
			t.Parallel()

			d := newReminderDeliveryTestDeps(t)
			d.reminderRepo.EXPECT().
				AlreadySent(anyCtx, "user-001", "phase-001", tt.stage).
				Return(false, nil)
			expectDelivered(t, d, "user-001", "id-"+tt.wantStage)
			d.reminderRepo.EXPECT().
				RecordSent(anyCtx, "user-001", "phase-001", tt.stage).
				Return(nil)

			err := d.uc.DeliverReminder(context.Background(), validDueData(tt.stage))
			require.NoError(t, err)
		})
	}
}
