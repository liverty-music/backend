package event

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/messaging"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-logging/logging"
)

// SalesReminderConsumer handles SALES_PHASE.reminder.due events by sending the
// pre-built notification payload to the user's push subscriptions and recording
// the delivery in the sent-log.
type SalesReminderConsumer struct {
	pushSubRepo  entity.PushSubscriptionRepository
	reminderRepo entity.SalesPhaseReminderRepository
	sender       entity.PushNotificationSender
	metrics      usecase.PushMetrics
	logger       *logging.Logger
}

// NewSalesReminderConsumer creates a new SalesReminderConsumer.
func NewSalesReminderConsumer(
	pushSubRepo entity.PushSubscriptionRepository,
	reminderRepo entity.SalesPhaseReminderRepository,
	sender entity.PushNotificationSender,
	metrics usecase.PushMetrics,
	logger *logging.Logger,
) *SalesReminderConsumer {
	return &SalesReminderConsumer{
		pushSubRepo:  pushSubRepo,
		reminderRepo: reminderRepo,
		sender:       sender,
		metrics:      metrics,
		logger:       logger,
	}
}

// Handle processes a SALES_PHASE.reminder.due event by sending the payload to
// all subscriptions belonging to the target user.
func (h *SalesReminderConsumer) Handle(msg *message.Message) error {
	ctx := msg.Context()

	var data entity.SalesPhaseReminderDueData
	if err := messaging.ParseCloudEventData(msg, &data); err != nil {
		h.logger.Error(ctx, "sales_reminder_consumer: failed to parse event", err)
		return fmt.Errorf("parse SALES_PHASE.reminder.due: %w", err)
	}

	stage := entity.ReminderStage(data.Stage)
	attrs := []slog.Attr{
		slog.String("user_id", data.UserID),
		slog.String("phase_id", data.PhaseID),
		slog.Int("stage", int(stage)),
	}
	h.logger.Info(ctx, "sales_reminder_consumer: processing", attrs...)

	// Once-only guard: the scan already checked AlreadySent before publishing,
	// but a duplicate message delivery (at-least-once broker) could replay the
	// event. Re-check here to prevent double-send.
	already, err := h.reminderRepo.AlreadySent(ctx, data.UserID, data.PhaseID, stage)
	if err != nil {
		return fmt.Errorf("sales_reminder_consumer: AlreadySent check: %w", err)
	}
	if already {
		h.logger.Info(ctx, "sales_reminder_consumer: already sent, skipping", attrs...)
		return nil
	}

	subs, err := h.pushSubRepo.ListByUserIDs(ctx, []string{data.UserID})
	if err != nil {
		return fmt.Errorf("sales_reminder_consumer: list subscriptions: %w", err)
	}
	if len(subs) == 0 {
		// No subscriptions — record sent so we don't retry on next scan.
		_ = h.reminderRepo.RecordSent(ctx, data.UserID, data.PhaseID, stage)
		return nil
	}

	if data.Payload == nil {
		h.logger.Warn(ctx, "sales_reminder_consumer: nil payload, skipping", attrs...)
		return nil
	}
	payloadBytes, err := json.Marshal(data.Payload)
	if err != nil {
		return fmt.Errorf("sales_reminder_consumer: marshal payload: %w", err)
	}

	var atLeastOneSuccess bool
	for _, sub := range subs {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if err := h.sender.Send(ctx, payloadBytes, sub); err != nil {
			if errors.Is(err, apperr.ErrNotFound) {
				h.metrics.RecordPushSend(ctx, "gone")
				if delErr := h.pushSubRepo.Delete(ctx, sub.UserID, sub.Endpoint); delErr != nil {
					h.logger.Error(ctx, "sales_reminder_consumer: delete stale sub failed", delErr,
						append(attrs, slog.String("endpoint", sub.Endpoint))...)
				}
			} else {
				h.metrics.RecordPushSend(ctx, "error")
				h.logger.Error(ctx, "sales_reminder_consumer: send failed", err, attrs...)
			}
		} else {
			h.metrics.RecordPushSend(ctx, "success")
			atLeastOneSuccess = true
		}
	}

	// Record the reminder as sent only when at least one subscription
	// accepted the delivery. A total-delivery failure leaves the sent-log
	// empty so the next scan (and consumer retry) can re-attempt.
	if atLeastOneSuccess {
		if err := h.reminderRepo.RecordSent(ctx, data.UserID, data.PhaseID, stage); err != nil {
			h.logger.Error(ctx, "sales_reminder_consumer: RecordSent failed", err, attrs...)
			// Non-fatal: next scan will re-check via ListSentStages / AlreadySent.
		}
	}
	return nil
}
