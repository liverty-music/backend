package usecase_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/liverty-music/backend/internal/entity"
	entitymocks "github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/usecase"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
)

// buildDeliveryUC is a test helper that wires a salesReminderDeliveryUseCase
// with caller-supplied mock dependencies.
func buildDeliveryUC(
	t *testing.T,
	reminderRepo *entitymocks.MockSalesPhaseReminderRepository,
	notificationUC *ucmocks.MockNotificationUseCase,
) usecase.SalesReminderDeliveryUseCase {
	t.Helper()
	return usecase.NewSalesReminderDeliveryUseCase(
		reminderRepo,
		notificationUC,
		newTestLogger(t),
	)
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

	reminderRepo := entitymocks.NewMockSalesPhaseReminderRepository(t)
	notificationUC := ucmocks.NewMockNotificationUseCase(t)

	reminderRepo.EXPECT().
		AlreadySent(anyCtx, "user-001", "phase-001", entity.ReminderStageApplyOpen).
		Return(true, nil)

	uc := buildDeliveryUC(t, reminderRepo, notificationUC)
	err := uc.DeliverReminder(context.Background(), validDueData(entity.ReminderStageApplyOpen))

	require.NoError(t, err)
	notificationUC.AssertNotCalled(t, "Notify")
}

// ---- Infra-error returns ----

// TestDeliverReminder_AlreadySentErrorReturnsErr verifies that an AlreadySent
// repository error propagates and does not attempt a send.
func TestDeliverReminder_AlreadySentErrorReturnsErr(t *testing.T) {
	t.Parallel()

	reminderRepo := entitymocks.NewMockSalesPhaseReminderRepository(t)
	notificationUC := ucmocks.NewMockNotificationUseCase(t)

	reminderRepo.EXPECT().
		AlreadySent(anyCtx, "user-001", "phase-001", entity.ReminderStageApplyOpen).
		Return(false, errors.New("db error"))

	uc := buildDeliveryUC(t, reminderRepo, notificationUC)
	err := uc.DeliverReminder(context.Background(), validDueData(entity.ReminderStageApplyOpen))

	require.Error(t, err)
	notificationUC.AssertNotCalled(t, "Notify")
}

// TestDeliverReminder_NotifyErrorReturnsErr verifies that a Notify error
// propagates.
func TestDeliverReminder_NotifyErrorReturnsErr(t *testing.T) {
	t.Parallel()

	reminderRepo := entitymocks.NewMockSalesPhaseReminderRepository(t)
	notificationUC := ucmocks.NewMockNotificationUseCase(t)

	reminderRepo.EXPECT().
		AlreadySent(anyCtx, "user-001", "phase-001", entity.ReminderStageApplyOpen).
		Return(false, nil)
	notificationUC.EXPECT().
		Notify(anyCtx, "user-001", entity.NotificationTypeSalesReminder, validPayload()).
		Return(nil, errors.New("record creation failed"))

	uc := buildDeliveryUC(t, reminderRepo, notificationUC)
	err := uc.DeliverReminder(context.Background(), validDueData(entity.ReminderStageApplyOpen))

	require.Error(t, err)
}

// ---- nil-payload defensive skip ----

// TestDeliverReminder_NilPayloadSkips verifies the nil-payload defensive guard
// returns nil without attempting a send — no send was attempted.
func TestDeliverReminder_NilPayloadSkips(t *testing.T) {
	t.Parallel()

	reminderRepo := entitymocks.NewMockSalesPhaseReminderRepository(t)
	notificationUC := ucmocks.NewMockNotificationUseCase(t)

	reminderRepo.EXPECT().
		AlreadySent(anyCtx, "user-001", "phase-001", entity.ReminderStageApplyOpen).
		Return(false, nil)

	data := entity.SalesPhaseReminderDueData{
		UserID:  "user-001",
		PhaseID: "phase-001",
		Stage:   int16(entity.ReminderStageApplyOpen),
		Payload: nil, // defensive skip
	}
	uc := buildDeliveryUC(t, reminderRepo, notificationUC)
	err := uc.DeliverReminder(context.Background(), data)

	require.NoError(t, err)
	notificationUC.AssertNotCalled(t, "Notify")
}

// ---- Terminal delivery outcome: no_subscription ----

// TestDeliverReminder_NoSubscriptionRecordsSent verifies that a no-subscription
// outcome records sent (suppressing future re-delivery) and returns nil.
func TestDeliverReminder_NoSubscriptionRecordsSent(t *testing.T) {
	t.Parallel()

	reminderRepo := entitymocks.NewMockSalesPhaseReminderRepository(t)
	notificationUC := ucmocks.NewMockNotificationUseCase(t)

	reminderRepo.EXPECT().
		AlreadySent(anyCtx, "user-001", "phase-001", entity.ReminderStageApplyClose24H).
		Return(false, nil)
	// Notify returns a failed notification with the no-subscription reason.
	notificationUC.EXPECT().
		Notify(anyCtx, "user-001", entity.NotificationTypeSalesReminder, mock.Anything).
		Return(&entity.Notification{
			DeliveryStatus: entity.NotificationDeliveryStatusFailed,
			FailureReason:  usecase.NotificationFailureReasonNoSubscription,
		}, nil)
	reminderRepo.EXPECT().
		RecordSent(anyCtx, "user-001", "phase-001", entity.ReminderStageApplyClose24H).
		Return(nil).
		Once()

	uc := buildDeliveryUC(t, reminderRepo, notificationUC)
	err := uc.DeliverReminder(context.Background(), validDueData(entity.ReminderStageApplyClose24H))

	require.NoError(t, err)
}

// ---- Terminal delivery outcome: delivered ----

// TestDeliverReminder_SuccessfulSendRecordsSent verifies that a successful
// notification records sent and returns nil.
func TestDeliverReminder_SuccessfulSendRecordsSent(t *testing.T) {
	t.Parallel()

	reminderRepo := entitymocks.NewMockSalesPhaseReminderRepository(t)
	notificationUC := ucmocks.NewMockNotificationUseCase(t)

	reminderRepo.EXPECT().
		AlreadySent(anyCtx, "user-001", "phase-001", entity.ReminderStageApplyOpen).
		Return(false, nil)
	notificationUC.EXPECT().
		Notify(anyCtx, "user-001", entity.NotificationTypeSalesReminder, mock.Anything).
		Return(&entity.Notification{
			DeliveryStatus: entity.NotificationDeliveryStatusDelivered,
		}, nil)
	reminderRepo.EXPECT().
		RecordSent(anyCtx, "user-001", "phase-001", entity.ReminderStageApplyOpen).
		Return(nil)

	uc := buildDeliveryUC(t, reminderRepo, notificationUC)
	err := uc.DeliverReminder(context.Background(), validDueData(entity.ReminderStageApplyOpen))

	require.NoError(t, err)
}

// ---- Terminal delivery outcome: failed ----

// TestDeliverReminder_TransientFailureDoesNotRecordSent verifies that on a
// transient failure (not the no-subscription sentinel) RecordSent is NOT called
// so the next scan can retry, and no error is returned.
func TestDeliverReminder_TransientFailureDoesNotRecordSent(t *testing.T) {
	t.Parallel()

	reminderRepo := entitymocks.NewMockSalesPhaseReminderRepository(t)
	notificationUC := ucmocks.NewMockNotificationUseCase(t)

	reminderRepo.EXPECT().
		AlreadySent(anyCtx, "user-001", "phase-001", entity.ReminderStageResultDay).
		Return(false, nil)
	// A failed notification with a non-no-subscription reason (transient).
	notificationUC.EXPECT().
		Notify(anyCtx, "user-001", entity.NotificationTypeSalesReminder, mock.Anything).
		Return(&entity.Notification{
			DeliveryStatus: entity.NotificationDeliveryStatusFailed,
			FailureReason:  "push service unavailable",
		}, nil)
	// RecordSent must NOT be called — leave the sent-log empty so the next scan retries.

	uc := buildDeliveryUC(t, reminderRepo, notificationUC)
	err := uc.DeliverReminder(context.Background(), validDueData(entity.ReminderStageResultDay))

	// Total failure is not returned as an error — the next scan will retry.
	require.NoError(t, err)
	reminderRepo.AssertNotCalled(t, "RecordSent")
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

			reminderRepo := entitymocks.NewMockSalesPhaseReminderRepository(t)
			notificationUC := ucmocks.NewMockNotificationUseCase(t)

			reminderRepo.EXPECT().
				AlreadySent(anyCtx, "user-001", "phase-001", tt.stage).
				Return(false, nil)
			notificationUC.EXPECT().
				Notify(anyCtx, "user-001", entity.NotificationTypeSalesReminder, mock.Anything).
				Return(&entity.Notification{
					DeliveryStatus: entity.NotificationDeliveryStatusDelivered,
				}, nil)
			reminderRepo.EXPECT().
				RecordSent(anyCtx, "user-001", "phase-001", tt.stage).
				Return(nil)

			uc := buildDeliveryUC(t, reminderRepo, notificationUC)
			err := uc.DeliverReminder(context.Background(), validDueData(tt.stage))
			require.NoError(t, err)
		})
	}
}
