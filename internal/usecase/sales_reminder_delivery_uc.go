package usecase

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-logging/logging"
)

// SalesReminderDeliveryUseCase delivers a single reminder event to the target
// user's push subscriptions, enforcing once-only delivery semantics.
type SalesReminderDeliveryUseCase interface {
	// DeliverReminder delivers the reminder described by data to the user's push
	// subscriptions. It enforces the once-only contract via AlreadySent / RecordSent
	// on the sent-log, and cleans up gone subscriptions.
	//
	// Semantics preserved from the previous consumer implementation:
	//   - AlreadySent guard fires first (at-least-once broker replay protection).
	//   - No subscriptions → record sent (suppress future re-delivery), return nil.
	//   - nil Payload → warn and return nil (defensive; publisher should never emit this).
	//   - RecordSent is called ONLY when at least one push succeeded. Total failure
	//     leaves the sent-log empty so the next scan can retry.
	//   - 410 Gone subscriptions are deleted inline.
	DeliverReminder(ctx context.Context, data entity.SalesPhaseReminderDueData) error
}

type salesReminderDeliveryUseCase struct {
	reminderRepo entity.SalesPhaseReminderRepository
	pushSubRepo  entity.PushSubscriptionRepository
	sender       entity.PushNotificationSender
	metrics      PushMetrics
	logger       *logging.Logger
}

// Compile-time interface compliance check.
var _ SalesReminderDeliveryUseCase = (*salesReminderDeliveryUseCase)(nil)

// NewSalesReminderDeliveryUseCase wires the reminder delivery use case.
func NewSalesReminderDeliveryUseCase(
	reminderRepo entity.SalesPhaseReminderRepository,
	pushSubRepo entity.PushSubscriptionRepository,
	sender entity.PushNotificationSender,
	metrics PushMetrics,
	logger *logging.Logger,
) *salesReminderDeliveryUseCase {
	return &salesReminderDeliveryUseCase{
		reminderRepo: reminderRepo,
		pushSubRepo:  pushSubRepo,
		sender:       sender,
		metrics:      metrics,
		logger:       logger,
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
	// at-least-once broker replay.
	already, err := uc.reminderRepo.AlreadySent(ctx, data.UserID, data.PhaseID, stage)
	if err != nil {
		return fmt.Errorf("sales_reminder_delivery: AlreadySent check: %w", err)
	}
	if already {
		uc.logger.Info(ctx, "sales_reminder_delivery: already sent, skipping", attrs...)
		return nil
	}

	subs, err := uc.pushSubRepo.ListByUserIDs(ctx, []string{data.UserID})
	if err != nil {
		return fmt.Errorf("sales_reminder_delivery: list subscriptions: %w", err)
	}
	if len(subs) == 0 {
		// No subscriptions — record sent so the next scan does not retry.
		_ = uc.reminderRepo.RecordSent(ctx, data.UserID, data.PhaseID, stage)
		return nil
	}

	if data.Payload == nil {
		uc.logger.Warn(ctx, "sales_reminder_delivery: nil payload, skipping", attrs...)
		return nil
	}
	payloadBytes, err := json.Marshal(data.Payload)
	if err != nil {
		return fmt.Errorf("sales_reminder_delivery: marshal payload: %w", err)
	}

	var atLeastOneSuccess bool
	for _, sub := range subs {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if err := uc.sender.Send(ctx, payloadBytes, sub); err != nil {
			if errors.Is(err, apperr.ErrNotFound) {
				uc.metrics.RecordPushSend(ctx, "gone")
				if delErr := uc.pushSubRepo.Delete(ctx, sub.UserID, sub.Endpoint); delErr != nil {
					uc.logger.Error(ctx, "sales_reminder_delivery: delete stale sub failed", delErr,
						append(attrs, slog.String("endpoint", sub.Endpoint))...)
				}
			} else {
				uc.metrics.RecordPushSend(ctx, "error")
				uc.logger.Error(ctx, "sales_reminder_delivery: send failed", err, attrs...)
			}
		} else {
			uc.metrics.RecordPushSend(ctx, "success")
			atLeastOneSuccess = true
		}
	}

	// Record sent only when at least one subscription accepted the delivery.
	// Total failure leaves the sent-log empty so the next scan can retry.
	if atLeastOneSuccess {
		if err := uc.reminderRepo.RecordSent(ctx, data.UserID, data.PhaseID, stage); err != nil {
			uc.logger.Error(ctx, "sales_reminder_delivery: RecordSent failed", err, attrs...)
			// Non-fatal: next scan will re-check via ListSentStages / AlreadySent.
		}
	}
	return nil
}
