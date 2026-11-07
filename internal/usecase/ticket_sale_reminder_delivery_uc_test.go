package usecase_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/liverty-music/backend/internal/entity"
	entitymocks "github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/usecase"
)

// reminderDeliveryTestDeps holds all dependencies for
// TicketSaleReminderDeliveryUseCase tests. DeliverReminder stays synchronous and
// composes the same entity ports NotificationUseCase.Deliver uses, directly
// (see notification_delivery.go) rather than through that interface, so these
// tests mock the entity ports rather than a NotificationUseCase, and read the
// message the real rendering produces.
type reminderDeliveryTestDeps struct {
	*subjectFixture
	reminderRepo *entitymocks.MockTicketSaleReminderRepository
	reminder     usecase.TicketSaleReminderDeliveryUseCase
}

// deliverySaleID is the TicketSale every delivery test reminds of.
const deliverySaleID entity.TicketSaleID = "sale-001"

// deliveryEventID is the fan's linked event in every delivery test.
const deliveryEventID = "event-001"

func newReminderDeliveryTestDeps(t *testing.T) *reminderDeliveryTestDeps {
	t.Helper()
	f := newSubjectFixture(t)
	d := &reminderDeliveryTestDeps{subjectFixture: f, reminderRepo: entitymocks.NewMockTicketSaleReminderRepository(t)}
	d.reminder = usecase.NewTicketSaleReminderDeliveryUseCase(
		d.reminderRepo,
		f.notifs,
		f.pushSubs,
		f.sender,
		f.subjects(),
		noopMetrics{},
		newTestLogger(t),
	)
	return d
}

// reminderReadable makes user-001 (Japanese), the sale and the linked event
// readable.
func (d *reminderDeliveryTestDeps) reminderReadable() {
	d.fanReadable(renderedFan("user-001", "ja"))
	d.ticketSales.EXPECT().Get(mock.Anything, deliverySaleID).Return(&entity.TicketSale{
		ID: deliverySaleID, Method: entity.TicketSaleMethodLottery,
		SaleStartTime: time.Date(2026, 10, 5, 18, 0, 0, 0, tsJST),
		SaleEndTime:   time.Date(2026, 10, 22, 23, 59, 0, 0, tsJST),
	}, nil).Maybe()
	d.concertsReadable(renderedConcert(deliveryEventID))
}

// notYetSent makes the sent-log hold no record of the stage.
func (d *reminderDeliveryTestDeps) notYetSent(stage entity.ReminderStage) {
	d.reminderRepo.EXPECT().AlreadySent(anyCtx, "user-001", deliverySaleID, stage).Return(false, nil)
}

// validDueData builds the due reminder of the given stage for user-001.
func validDueData(stage entity.ReminderStage) entity.TicketSaleReminderDueData {
	return entity.TicketSaleReminderDueData{
		UserID:       "user-001",
		TicketSaleID: string(deliverySaleID),
		Stage:        int16(stage),
		LinkEventID:  deliveryEventID,
	}
}

// expectDelivered expects one record and one browser that accepts the push.
func (d *reminderDeliveryTestDeps) expectDelivered(notifID string) *entity.Notification {
	stored := d.recordsAs(notifID)
	d.browsers("user-001", "https://push/1")
	d.sendAccepted("https://push/1")
	return stored
}

// ---- AlreadySent guard (no send expected) ----

// TestDeliverReminder_AlreadySentSkipsWithoutSend verifies that the AlreadySent
// dedup guard returns nil without attempting a notification send.
// @spec components/usecase/ticket-sale-reminder/deliver-reminder "Repeated request"
func TestDeliverReminder_AlreadySentSkipsWithoutSend(t *testing.T) {
	t.Parallel()

	d := newReminderDeliveryTestDeps(t)
	d.reminderRepo.EXPECT().
		AlreadySent(anyCtx, "user-001", deliverySaleID, entity.ReminderStageApplyOpen).
		Return(true, nil)

	err := d.reminder.DeliverReminder(context.Background(), validDueData(entity.ReminderStageApplyOpen))

	require.NoError(t, err)
	d.notifs.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
}

// ---- Infra-error returns ----

// TestDeliverReminder_AlreadySentErrorReturnsErr verifies that an AlreadySent
// repository error propagates and does not attempt a send.
// @spec components/usecase/ticket-sale-reminder/deliver-reminder "Check fails"
func TestDeliverReminder_AlreadySentErrorReturnsErr(t *testing.T) {
	t.Parallel()

	d := newReminderDeliveryTestDeps(t)
	d.reminderRepo.EXPECT().
		AlreadySent(anyCtx, "user-001", deliverySaleID, entity.ReminderStageApplyOpen).
		Return(false, errors.New("db error"))

	err := d.reminder.DeliverReminder(context.Background(), validDueData(entity.ReminderStageApplyOpen))

	require.Error(t, err)
	d.notifs.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
}

// TestDeliverReminder_DeliveryErrorReturnsErr verifies that a failure to record
// the Notification fails DeliverReminder so the request runs again, with nothing
// recorded as sent.
func TestDeliverReminder_DeliveryErrorReturnsErr(t *testing.T) {
	t.Parallel()

	d := newReminderDeliveryTestDeps(t)
	d.notYetSent(entity.ReminderStageApplyOpen)
	d.reminderReadable()
	d.notifs.EXPECT().Create(anyCtx, mock.Anything).Return(errors.New("record creation failed"))

	err := d.reminder.DeliverReminder(context.Background(), validDueData(entity.ReminderStageApplyOpen))

	require.Error(t, err)
	d.reminderRepo.AssertNotCalled(t, "RecordSent", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

// ---- No linked event defensive skip ----

// @spec components/usecase/ticket-sale-reminder/deliver-reminder "Empty request"
func TestDeliverReminder_WithoutALinkedEventSkips(t *testing.T) {
	t.Parallel()

	d := newReminderDeliveryTestDeps(t)
	d.notYetSent(entity.ReminderStageApplyOpen)

	data := validDueData(entity.ReminderStageApplyOpen)
	data.LinkEventID = ""
	err := d.reminder.DeliverReminder(context.Background(), data)

	require.NoError(t, err)
	d.notifs.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
}

// ---- Terminal delivery outcome: no_subscription ----

// TestDeliverReminder_NoSubscriptionRecordsSent verifies that a no-subscription
// outcome records sent (suppressing future re-delivery) and returns nil.
// @spec components/usecase/ticket-sale-reminder/deliver-reminder "No device"
func TestDeliverReminder_NoSubscriptionRecordsSent(t *testing.T) {
	t.Parallel()

	d := newReminderDeliveryTestDeps(t)
	d.notYetSent(entity.ReminderStageApplyClose24H)
	d.reminderReadable()
	d.recordsAs("id-nosub")
	d.browsers("user-001")
	d.reminderRepo.EXPECT().
		RecordSent(anyCtx, "user-001", deliverySaleID, entity.ReminderStageApplyClose24H).
		Return(nil).
		Once()

	err := d.reminder.DeliverReminder(context.Background(), validDueData(entity.ReminderStageApplyClose24H))

	require.NoError(t, err)
}

// ---- Terminal delivery outcome: delivered ----

// TestDeliverReminder_SuccessfulSendRecordsSent verifies that a successful
// notification names the sale, the stage and the linked event, records sent and
// returns nil.
// @spec components/usecase/ticket-sale-reminder/deliver-reminder "Delivered to a device"
// @spec components/usecase/ticket-sale-reminder/deliver-reminder "Accepted by a device"
func TestDeliverReminder_SuccessfulSendRecordsSent(t *testing.T) {
	t.Parallel()

	d := newReminderDeliveryTestDeps(t)
	d.notYetSent(entity.ReminderStageApplyOpen)
	d.reminderReadable()
	stored := d.expectDelivered("id-ok")
	d.reminderRepo.EXPECT().
		RecordSent(anyCtx, "user-001", deliverySaleID, entity.ReminderStageApplyOpen).
		Return(nil).
		Once()

	err := d.reminder.DeliverReminder(context.Background(), validDueData(entity.ReminderStageApplyOpen))

	require.NoError(t, err)
	assert.Equal(t, "user-001", stored.UserID)
	assert.Equal(t, entity.NotificationTypeSalesReminder, stored.Type)
	assert.Equal(t, entity.NotificationSubject{
		TicketSaleID: string(deliverySaleID), Stage: entity.ReminderStageApplyOpen, EventID: deliveryEventID,
	}, stored.Subject)
}

// TestRunDeliverReminder_DeliversTheRequestedReminder verifies that the
// deliver-reminder workflow the scan starts delivers that fan's reminder for
// the sale and stage.
//
// @spec components/usecase/ticket-sale-reminder/deliver-reminder "Reminder requested"
func TestRunDeliverReminder_DeliversTheRequestedReminder(t *testing.T) {
	t.Parallel()

	d := newReminderDeliveryTestDeps(t)
	d.notYetSent(entity.ReminderStageApplyClose24H)
	d.reminderReadable()
	stored := d.expectDelivered("id-ok")
	d.reminderRepo.EXPECT().
		RecordSent(anyCtx, "user-001", deliverySaleID, entity.ReminderStageApplyClose24H).
		Return(nil).
		Once()

	_, err := d.reminder.RunDeliverReminder(context.Background(), &inlineFlow{}, validDueData(entity.ReminderStageApplyClose24H))

	require.NoError(t, err)
	assert.Equal(t, entity.ReminderStageApplyClose24H, stored.Subject.Stage)
	assert.Equal(t, string(deliverySaleID), stored.Subject.TicketSaleID)
}

// ---- Terminal delivery outcome: failed ----

// TestDeliverReminder_TransientFailureDoesNotRecordSent verifies that on a
// transient failure (not the no-subscription sentinel) RecordSent is NOT called
// so the next scan can retry, and no error is returned.
// @spec components/usecase/ticket-sale-reminder/deliver-reminder "Push fails"
func TestDeliverReminder_TransientFailureDoesNotRecordSent(t *testing.T) {
	t.Parallel()

	d := newReminderDeliveryTestDeps(t)
	d.notYetSent(entity.ReminderStageApplyClose24H)
	d.reminderReadable()
	d.recordsAs("id-transient")
	d.browsers("user-001", "https://push/1")
	d.sendFails("https://push/1", errors.New("push service unavailable"))
	// RecordSent must NOT be called — leave the sent-log empty so the next scan retries.

	err := d.reminder.DeliverReminder(context.Background(), validDueData(entity.ReminderStageApplyClose24H))

	// Total failure is not returned as an error — the next scan will retry.
	require.NoError(t, err)
	d.reminderRepo.AssertNotCalled(t, "RecordSent", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

// A sale deleted before delivery records and sends nothing and stays out of the
// sent-log, without failing the request.
func TestDeliverReminder_SubjectDeleted(t *testing.T) {
	t.Parallel()

	d := newReminderDeliveryTestDeps(t)
	d.notYetSent(entity.ReminderStageApplyOpen)
	d.fanReadable(renderedFan("user-001", "ja"))
	d.ticketSales.EXPECT().Get(mock.Anything, deliverySaleID).Return(nil, apperr.New(codes.NotFound, "sale not found")).Once()

	err := d.reminder.DeliverReminder(context.Background(), validDueData(entity.ReminderStageApplyOpen))

	require.NoError(t, err)
	d.notifs.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
	d.reminderRepo.AssertNotCalled(t, "RecordSent", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

// ---- phase_stage log field coverage across stages ----

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
	}

	for _, tt := range tests {
		t.Run(tt.wantStage, func(t *testing.T) {
			t.Parallel()

			d := newReminderDeliveryTestDeps(t)
			d.notYetSent(tt.stage)
			d.reminderReadable()
			d.expectDelivered("id-" + tt.wantStage)
			d.reminderRepo.EXPECT().
				RecordSent(anyCtx, "user-001", deliverySaleID, tt.stage).
				Return(nil)

			err := d.reminder.DeliverReminder(context.Background(), validDueData(tt.stage))
			require.NoError(t, err)
		})
	}
}

// TestDeliverReminder_RecordingFailsAfterDelivery verifies that a failure to
// record a delivered reminder does not fail DeliverReminder.
//
// @spec components/usecase/ticket-sale-reminder/deliver-reminder "Recording fails after delivery"
func TestDeliverReminder_RecordingFailsAfterDelivery(t *testing.T) {
	t.Parallel()

	d := newReminderDeliveryTestDeps(t)
	d.notYetSent(entity.ReminderStageApplyClose24H)
	d.reminderReadable()
	d.expectDelivered("id-rec-fail")
	d.reminderRepo.EXPECT().
		RecordSent(anyCtx, "user-001", deliverySaleID, entity.ReminderStageApplyClose24H).
		Return(errors.New("db down"))

	err := d.reminder.DeliverReminder(context.Background(), validDueData(entity.ReminderStageApplyClose24H))

	require.NoError(t, err)
}

// TestDeliverReminder_MessageNamesSaleAndMilestone verifies that the issued
// sales_reminder message data names the TicketSale and the milestone, next to
// the notification id and type Deliver adds.
//
// @spec components/usecase/ticket-sale-reminder/deliver-reminder "Apply-close reminder"
func TestDeliverReminder_MessageNamesSaleAndMilestone(t *testing.T) {
	t.Parallel()

	d := newReminderDeliveryTestDeps(t)
	d.notYetSent(entity.ReminderStageApplyClose24H)
	d.reminderReadable()
	d.recordsAs("id-milestone")
	d.browsers("user-001", "https://push/1")
	pushed := d.capturePushes(1)
	d.reminderRepo.EXPECT().
		RecordSent(anyCtx, "user-001", deliverySaleID, entity.ReminderStageApplyClose24H).
		Return(nil)

	err := d.reminder.DeliverReminder(context.Background(), validDueData(entity.ReminderStageApplyClose24H))
	require.NoError(t, err)

	require.Len(t, *pushed, 1)
	assert.Equal(t, map[string]string{
		"url":             "/concerts/" + deliveryEventID,
		"notification_id": "id-milestone",
		"type":            "sales_reminder",
		"ticket_sale_id":  string(deliverySaleID),
		"milestone":       "apply_close_24h",
	}, decodeMessage(t, (*pushed)[0].raw).Data)
}
