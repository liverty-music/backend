package usecase

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-logging/logging"
)

// SalesReminderDeliveryUseCase delivers a single reminder event to the target
// user's push subscriptions, enforcing once-only delivery semantics.
type SalesReminderDeliveryUseCase interface {
	// DeliverReminder delivers the reminder described by data by dispatching it
	// through the notification service (which records the notification, sends to
	// the user's push subscriptions, and cleans up gone endpoints). It enforces
	// the once-only contract via AlreadySent / RecordSent on the sent-log.
	//
	// Semantics preserved from the previous consumer implementation:
	//   - AlreadySent guard fires first (at-least-once broker replay protection).
	//   - nil Payload → warn and return nil (defensive; publisher should never emit this).
	//   - No push subscription → record sent (suppress future re-delivery), return nil.
	//   - RecordSent is called ONLY when the notification was delivered (or there
	//     was no device to deliver to). A transient send failure leaves the
	//     sent-log empty so the next scan can retry.
	DeliverReminder(ctx context.Context, data entity.SalesPhaseReminderDueData) error
}

type salesReminderDeliveryUseCase struct {
	reminderRepo   entity.SalesPhaseReminderRepository
	notificationUC NotificationUseCase
	publisher      EventPublisher
	logger         *logging.Logger
}

// Compile-time interface compliance check.
var _ SalesReminderDeliveryUseCase = (*salesReminderDeliveryUseCase)(nil)

// NewSalesReminderDeliveryUseCase wires the reminder delivery use case.
func NewSalesReminderDeliveryUseCase(
	reminderRepo entity.SalesPhaseReminderRepository,
	notificationUC NotificationUseCase,
	publisher EventPublisher,
	logger *logging.Logger,
) *salesReminderDeliveryUseCase {
	return &salesReminderDeliveryUseCase{
		reminderRepo:   reminderRepo,
		notificationUC: notificationUC,
		publisher:      publisher,
		logger:         logger,
	}
}

// publishDeliveryOutcome emits a sales_reminder.delivered analytics event for
// the given terminal outcome. Failures are logged and never propagated — the
// analytics publish must not affect DeliverReminder's return value or the
// once-only RecordSent semantics.
func (uc *salesReminderDeliveryUseCase) publishDeliveryOutcome(
	ctx context.Context,
	data entity.SalesPhaseReminderDueData,
	stage entity.ReminderStage,
	deliveryStatus string,
) {
	if err := uc.publisher.PublishEvent(ctx, entity.SubjectSalesReminderDelivered, entity.SalesReminderDeliveredData{
		UserID:         data.UserID,
		PhaseStage:     stage.String(),
		DeliveryStatus: deliveryStatus,
	}); err != nil {
		uc.logger.Error(ctx, "failed to publish SALES_REMINDER.delivered event", err,
			slog.String("user_id", data.UserID),
			slog.String("phase_id", data.PhaseID),
			slog.String("stage", stage.String()),
			slog.String("delivery_status", deliveryStatus),
		)
		// Non-fatal: DeliverReminder's return value and RecordSent semantics are unchanged.
	}
}

// DeliverReminder implements [SalesReminderDeliveryUseCase].
func (uc *salesReminderDeliveryUseCase) DeliverReminder(ctx context.Context, data entity.SalesPhaseReminderDueData) error {
	stage := entity.ReminderStage(data.Stage)
	attrs := []slog.Attr{
		slog.String("user_id", data.UserID),
		slog.String("phase_id", data.PhaseID),
		slog.Int("stage", int(stage)),
	}

	// Once-only guard: re-check AlreadySent to prevent double-send on
	// at-least-once broker replay. Not a terminal delivery outcome — no analytics emit.
	already, err := uc.reminderRepo.AlreadySent(ctx, data.UserID, data.PhaseID, stage)
	if err != nil {
		return fmt.Errorf("sales_reminder_delivery: AlreadySent check: %w", err)
	}
	if already {
		uc.logger.Info(ctx, "sales_reminder_delivery: already sent, skipping", attrs...)
		return nil
	}

	// nil Payload is a defensive skip — publisher should never emit this. Not
	// a terminal delivery outcome because no send was attempted, and not an error
	// (returning one would poison-loop the message on at-least-once redelivery).
	if data.Payload == nil {
		uc.logger.Warn(ctx, "sales_reminder_delivery: nil payload, skipping", attrs...)
		return nil
	}

	// Record and dispatch through the notification service: it creates the
	// durable record, resolves the user's push subscriptions, sends, cleans up
	// gone endpoints, and records the delivery outcome. A record-create failure
	// (no record => no send) surfaces here so the at-least-once retry re-drives it.
	n, err := uc.notificationUC.Notify(ctx, data.UserID, entity.NotificationTypeSalesReminder, data.Payload)
	if err != nil {
		return fmt.Errorf("sales_reminder_delivery: notify: %w", err)
	}

	switch {
	case n.DeliveryStatus == entity.NotificationDeliveryStatusDelivered:
		// At least one subscription accepted the push. Record sent so the next
		// scan does not re-deliver, then emit the terminal delivered outcome.
		if err := uc.reminderRepo.RecordSent(ctx, data.UserID, data.PhaseID, stage); err != nil {
			uc.logger.Error(ctx, "sales_reminder_delivery: RecordSent failed", err, attrs...)
			// Non-fatal: next scan will re-check via ListSentStages / AlreadySent.
		}
		uc.publishDeliveryOutcome(ctx, data, stage, "delivered")
	case n.FailureReason == NotificationFailureReasonNoSubscription:
		// The user has no push device to deliver to — nothing to retry. Record
		// sent to suppress future re-delivery and emit the no_subscription outcome.
		if err := uc.reminderRepo.RecordSent(ctx, data.UserID, data.PhaseID, stage); err != nil {
			uc.logger.Error(ctx, "sales_reminder_delivery: RecordSent failed", err, attrs...)
		}
		uc.publishDeliveryOutcome(ctx, data, stage, "no_subscription")
	default:
		// Transient send failure — leave the sent-log empty so the next scan
		// retries, and emit the terminal failed outcome.
		uc.publishDeliveryOutcome(ctx, data, stage, "failed")
	}
	return nil
}
