package usecase_test

import (
	"context"
	"errors"
	"testing"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/liverty-music/backend/internal/entity"
	entitymocks "github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/usecase"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
)

// noopDeliveryMetrics is a no-op PushMetrics for delivery use case tests that do
// not assert on metric labels.
type noopDeliveryMetrics struct{}

func (noopDeliveryMetrics) RecordPushSend(_ context.Context, _ string) {}

// buildDeliveryUC is a test helper that wires a salesReminderDeliveryUseCase
// with caller-supplied mock dependencies.
func buildDeliveryUC(
	t *testing.T,
	reminderRepo *entitymocks.MockSalesPhaseReminderRepository,
	pushSubRepo *entitymocks.MockPushSubscriptionRepository,
	sender *entitymocks.MockPushNotificationSender,
	publisher *ucmocks.MockEventPublisher,
) usecase.SalesReminderDeliveryUseCase {
	t.Helper()
	return usecase.NewSalesReminderDeliveryUseCase(
		reminderRepo,
		pushSubRepo,
		sender,
		publisher,
		noopDeliveryMetrics{},
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

// ---- AlreadySent guard (no analytics emit expected) ----

// TestDeliverReminder_AlreadySentSkipsWithoutEmit verifies that the AlreadySent
// dedup guard returns nil without emitting an analytics event.
func TestDeliverReminder_AlreadySentSkipsWithoutEmit(t *testing.T) {
	t.Parallel()

	reminderRepo := entitymocks.NewMockSalesPhaseReminderRepository(t)
	pushSubRepo := entitymocks.NewMockPushSubscriptionRepository(t)
	sender := entitymocks.NewMockPushNotificationSender(t)
	publisher := ucmocks.NewMockEventPublisher(t)

	reminderRepo.EXPECT().
		AlreadySent(anyCtx, "user-001", "phase-001", entity.ReminderStageApplyOpen).
		Return(true, nil)

	uc := buildDeliveryUC(t, reminderRepo, pushSubRepo, sender, publisher)
	err := uc.DeliverReminder(context.Background(), validDueData(entity.ReminderStageApplyOpen))

	require.NoError(t, err)
	publisher.AssertNotCalled(t, "PublishEvent")
}

// ---- Infra-error returns (no analytics emit expected) ----

// TestDeliverReminder_AlreadySentErrorReturnsErrWithoutEmit verifies that an
// AlreadySent repository error propagates and does not emit analytics.
func TestDeliverReminder_AlreadySentErrorReturnsErrWithoutEmit(t *testing.T) {
	t.Parallel()

	reminderRepo := entitymocks.NewMockSalesPhaseReminderRepository(t)
	pushSubRepo := entitymocks.NewMockPushSubscriptionRepository(t)
	sender := entitymocks.NewMockPushNotificationSender(t)
	publisher := ucmocks.NewMockEventPublisher(t)

	reminderRepo.EXPECT().
		AlreadySent(anyCtx, "user-001", "phase-001", entity.ReminderStageApplyOpen).
		Return(false, errors.New("db error"))

	uc := buildDeliveryUC(t, reminderRepo, pushSubRepo, sender, publisher)
	err := uc.DeliverReminder(context.Background(), validDueData(entity.ReminderStageApplyOpen))

	require.Error(t, err)
	publisher.AssertNotCalled(t, "PublishEvent")
}

// TestDeliverReminder_ListByUserIDsErrorReturnsErrWithoutEmit verifies that a
// push subscription list error propagates and does not emit analytics.
func TestDeliverReminder_ListByUserIDsErrorReturnsErrWithoutEmit(t *testing.T) {
	t.Parallel()

	reminderRepo := entitymocks.NewMockSalesPhaseReminderRepository(t)
	pushSubRepo := entitymocks.NewMockPushSubscriptionRepository(t)
	sender := entitymocks.NewMockPushNotificationSender(t)
	publisher := ucmocks.NewMockEventPublisher(t)

	reminderRepo.EXPECT().
		AlreadySent(anyCtx, "user-001", "phase-001", entity.ReminderStageApplyOpen).
		Return(false, nil)
	pushSubRepo.EXPECT().
		ListByUserIDs(anyCtx, []string{"user-001"}).
		Return(nil, errors.New("db error"))

	uc := buildDeliveryUC(t, reminderRepo, pushSubRepo, sender, publisher)
	err := uc.DeliverReminder(context.Background(), validDueData(entity.ReminderStageApplyOpen))

	require.Error(t, err)
	publisher.AssertNotCalled(t, "PublishEvent")
}

// ---- nil-payload defensive skip (no analytics emit expected) ----

// TestDeliverReminder_NilPayloadSkipsWithoutEmit verifies the nil-payload
// defensive guard returns nil without emitting analytics — no send was attempted.
func TestDeliverReminder_NilPayloadSkipsWithoutEmit(t *testing.T) {
	t.Parallel()

	reminderRepo := entitymocks.NewMockSalesPhaseReminderRepository(t)
	pushSubRepo := entitymocks.NewMockPushSubscriptionRepository(t)
	sender := entitymocks.NewMockPushNotificationSender(t)
	publisher := ucmocks.NewMockEventPublisher(t)

	sub := &entity.PushSubscription{UserID: "user-001", Endpoint: "https://push.example.com/1"}
	reminderRepo.EXPECT().
		AlreadySent(anyCtx, "user-001", "phase-001", entity.ReminderStageApplyOpen).
		Return(false, nil)
	pushSubRepo.EXPECT().
		ListByUserIDs(anyCtx, []string{"user-001"}).
		Return([]*entity.PushSubscription{sub}, nil)

	data := entity.SalesPhaseReminderDueData{
		UserID:  "user-001",
		PhaseID: "phase-001",
		Stage:   int16(entity.ReminderStageApplyOpen),
		Payload: nil, // defensive skip
	}
	uc := buildDeliveryUC(t, reminderRepo, pushSubRepo, sender, publisher)
	err := uc.DeliverReminder(context.Background(), data)

	require.NoError(t, err)
	publisher.AssertNotCalled(t, "PublishEvent")
}

// ---- Terminal delivery outcome: no_subscription ----

// TestDeliverReminder_NoSubscriptionEmitsNoSubscription verifies that
// delivery_status="no_subscription" is emitted when the user has no push
// subscriptions.
func TestDeliverReminder_NoSubscriptionEmitsNoSubscription(t *testing.T) {
	t.Parallel()

	reminderRepo := entitymocks.NewMockSalesPhaseReminderRepository(t)
	pushSubRepo := entitymocks.NewMockPushSubscriptionRepository(t)
	sender := entitymocks.NewMockPushNotificationSender(t)
	publisher := ucmocks.NewMockEventPublisher(t)

	reminderRepo.EXPECT().
		AlreadySent(anyCtx, "user-001", "phase-001", entity.ReminderStageApplyClose24H).
		Return(false, nil)
	pushSubRepo.EXPECT().
		ListByUserIDs(anyCtx, []string{"user-001"}).
		Return([]*entity.PushSubscription{}, nil)
	reminderRepo.EXPECT().
		RecordSent(anyCtx, "user-001", "phase-001", entity.ReminderStageApplyClose24H).
		Return(nil).
		Maybe()

	publisher.EXPECT().
		PublishEvent(anyCtx, entity.SubjectSalesReminderDelivered,
			mock.MatchedBy(func(d entity.SalesReminderDeliveredData) bool {
				return d.UserID == "user-001" &&
					d.PhaseStage == "APPLY_CLOSE_24H" &&
					d.DeliveryStatus == "no_subscription"
			}),
		).
		Return(nil).
		Once()

	uc := buildDeliveryUC(t, reminderRepo, pushSubRepo, sender, publisher)
	err := uc.DeliverReminder(context.Background(), validDueData(entity.ReminderStageApplyClose24H))

	require.NoError(t, err)
}

// ---- Terminal delivery outcome: delivered ----

// TestDeliverReminder_SuccessfulSendEmitsDelivered verifies that
// delivery_status="delivered" is emitted after at least one successful push send.
func TestDeliverReminder_SuccessfulSendEmitsDelivered(t *testing.T) {
	t.Parallel()

	reminderRepo := entitymocks.NewMockSalesPhaseReminderRepository(t)
	pushSubRepo := entitymocks.NewMockPushSubscriptionRepository(t)
	sender := entitymocks.NewMockPushNotificationSender(t)
	publisher := ucmocks.NewMockEventPublisher(t)

	sub := &entity.PushSubscription{UserID: "user-001", Endpoint: "https://push.example.com/1"}

	reminderRepo.EXPECT().
		AlreadySent(anyCtx, "user-001", "phase-001", entity.ReminderStageApplyOpen).
		Return(false, nil)
	pushSubRepo.EXPECT().
		ListByUserIDs(anyCtx, []string{"user-001"}).
		Return([]*entity.PushSubscription{sub}, nil)
	sender.EXPECT().
		Send(anyCtx, mock.Anything, sub).
		Return(nil)
	reminderRepo.EXPECT().
		RecordSent(anyCtx, "user-001", "phase-001", entity.ReminderStageApplyOpen).
		Return(nil)
	publisher.EXPECT().
		PublishEvent(anyCtx, entity.SubjectSalesReminderDelivered,
			mock.MatchedBy(func(d entity.SalesReminderDeliveredData) bool {
				return d.UserID == "user-001" &&
					d.PhaseStage == "APPLY_OPEN" &&
					d.DeliveryStatus == "delivered"
			}),
		).
		Return(nil).
		Once()

	uc := buildDeliveryUC(t, reminderRepo, pushSubRepo, sender, publisher)
	err := uc.DeliverReminder(context.Background(), validDueData(entity.ReminderStageApplyOpen))

	require.NoError(t, err)
}

// ---- Terminal delivery outcome: failed ----

// TestDeliverReminder_AllSendFailEmitsFailed verifies that
// delivery_status="failed" is emitted when all send attempts are rejected (non-410
// errors, so no RecordSent).
func TestDeliverReminder_AllSendFailEmitsFailed(t *testing.T) {
	t.Parallel()

	reminderRepo := entitymocks.NewMockSalesPhaseReminderRepository(t)
	pushSubRepo := entitymocks.NewMockPushSubscriptionRepository(t)
	sender := entitymocks.NewMockPushNotificationSender(t)
	publisher := ucmocks.NewMockEventPublisher(t)

	sub := &entity.PushSubscription{UserID: "user-001", Endpoint: "https://push.example.com/1"}

	reminderRepo.EXPECT().
		AlreadySent(anyCtx, "user-001", "phase-001", entity.ReminderStageResultDay).
		Return(false, nil)
	pushSubRepo.EXPECT().
		ListByUserIDs(anyCtx, []string{"user-001"}).
		Return([]*entity.PushSubscription{sub}, nil)
	sender.EXPECT().
		Send(anyCtx, mock.Anything, sub).
		Return(errors.New("send error: 500 internal server error"))
	publisher.EXPECT().
		PublishEvent(anyCtx, entity.SubjectSalesReminderDelivered,
			mock.MatchedBy(func(d entity.SalesReminderDeliveredData) bool {
				return d.UserID == "user-001" &&
					d.PhaseStage == "RESULT_DAY" &&
					d.DeliveryStatus == "failed"
			}),
		).
		Return(nil).
		Once()

	uc := buildDeliveryUC(t, reminderRepo, pushSubRepo, sender, publisher)
	err := uc.DeliverReminder(context.Background(), validDueData(entity.ReminderStageResultDay))

	// Total failure is not returned as an error — the next scan will retry.
	require.NoError(t, err)
}

// TestDeliverReminder_AllGoneEmitsFailed verifies that delivery_status="failed"
// is emitted when all subscriptions return 410 Gone (all gone, none succeeded).
func TestDeliverReminder_AllGoneEmitsFailed(t *testing.T) {
	t.Parallel()

	reminderRepo := entitymocks.NewMockSalesPhaseReminderRepository(t)
	pushSubRepo := entitymocks.NewMockPushSubscriptionRepository(t)
	sender := entitymocks.NewMockPushNotificationSender(t)
	publisher := ucmocks.NewMockEventPublisher(t)

	sub := &entity.PushSubscription{UserID: "user-001", Endpoint: "https://push.example.com/gone"}

	reminderRepo.EXPECT().
		AlreadySent(anyCtx, "user-001", "phase-001", entity.ReminderStageApplyClose1H).
		Return(false, nil)
	pushSubRepo.EXPECT().
		ListByUserIDs(anyCtx, []string{"user-001"}).
		Return([]*entity.PushSubscription{sub}, nil)
	sender.EXPECT().
		Send(anyCtx, mock.Anything, sub).
		Return(apperr.ErrNotFound)
	pushSubRepo.EXPECT().
		Delete(anyCtx, "user-001", "https://push.example.com/gone").
		Return(nil)
	publisher.EXPECT().
		PublishEvent(anyCtx, entity.SubjectSalesReminderDelivered,
			mock.MatchedBy(func(d entity.SalesReminderDeliveredData) bool {
				return d.UserID == "user-001" &&
					d.PhaseStage == "APPLY_CLOSE_1H" &&
					d.DeliveryStatus == "failed"
			}),
		).
		Return(nil).
		Once()

	uc := buildDeliveryUC(t, reminderRepo, pushSubRepo, sender, publisher)
	err := uc.DeliverReminder(context.Background(), validDueData(entity.ReminderStageApplyClose1H))

	require.NoError(t, err)
}

// ---- Publish non-fatal ----

// TestDeliverReminder_PublishErrorIsNonFatal verifies that a PublishEvent
// failure does not change DeliverReminder's return value or the once-only
// RecordSent semantics.
func TestDeliverReminder_PublishErrorIsNonFatal(t *testing.T) {
	t.Parallel()

	reminderRepo := entitymocks.NewMockSalesPhaseReminderRepository(t)
	pushSubRepo := entitymocks.NewMockPushSubscriptionRepository(t)
	sender := entitymocks.NewMockPushNotificationSender(t)
	publisher := ucmocks.NewMockEventPublisher(t)

	sub := &entity.PushSubscription{UserID: "user-001", Endpoint: "https://push.example.com/1"}

	reminderRepo.EXPECT().
		AlreadySent(anyCtx, "user-001", "phase-001", entity.ReminderStageApplyOpen).
		Return(false, nil)
	pushSubRepo.EXPECT().
		ListByUserIDs(anyCtx, []string{"user-001"}).
		Return([]*entity.PushSubscription{sub}, nil)
	sender.EXPECT().
		Send(anyCtx, mock.Anything, sub).
		Return(nil)
	reminderRepo.EXPECT().
		RecordSent(anyCtx, "user-001", "phase-001", entity.ReminderStageApplyOpen).
		Return(nil)
	publisher.EXPECT().
		PublishEvent(anyCtx, entity.SubjectSalesReminderDelivered, mock.Anything).
		Return(errors.New("nats unavailable")).
		Once()

	uc := buildDeliveryUC(t, reminderRepo, pushSubRepo, sender, publisher)
	err := uc.DeliverReminder(context.Background(), validDueData(entity.ReminderStageApplyOpen))

	// Publish failure must NOT be returned — push delivered, RecordSent was called.
	require.NoError(t, err)
}

// ---- phase_stage string form ----

// TestDeliverReminder_PhaseStageStrings verifies that each ReminderStage value
// serialises to the expected string in the published payload.
func TestDeliverReminder_PhaseStageStrings(t *testing.T) {
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
			pushSubRepo := entitymocks.NewMockPushSubscriptionRepository(t)
			sender := entitymocks.NewMockPushNotificationSender(t)
			publisher := ucmocks.NewMockEventPublisher(t)

			sub := &entity.PushSubscription{UserID: "user-001", Endpoint: "https://push.example.com/1"}

			reminderRepo.EXPECT().
				AlreadySent(anyCtx, "user-001", "phase-001", tt.stage).
				Return(false, nil)
			pushSubRepo.EXPECT().
				ListByUserIDs(anyCtx, []string{"user-001"}).
				Return([]*entity.PushSubscription{sub}, nil)
			sender.EXPECT().
				Send(anyCtx, mock.Anything, sub).
				Return(nil)
			reminderRepo.EXPECT().
				RecordSent(anyCtx, "user-001", "phase-001", tt.stage).
				Return(nil)
			publisher.EXPECT().
				PublishEvent(anyCtx, entity.SubjectSalesReminderDelivered,
					mock.MatchedBy(func(d entity.SalesReminderDeliveredData) bool {
						assert.Equal(t, tt.wantStage, d.PhaseStage, "unexpected phase_stage")
						return d.PhaseStage == tt.wantStage
					}),
				).
				Return(nil).
				Once()

			uc := buildDeliveryUC(t, reminderRepo, pushSubRepo, sender, publisher)
			err := uc.DeliverReminder(context.Background(), validDueData(tt.stage))
			require.NoError(t, err)
		})
	}
}
