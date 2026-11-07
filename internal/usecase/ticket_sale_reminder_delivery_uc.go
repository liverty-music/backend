package usecase

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/pkg/analyticslog"
	"github.com/pannpers/go-logging/logging"
)

// TicketSaleReminderDeliveryUseCase delivers a single reminder event to the target
// user's push subscriptions, enforcing once-only delivery semantics.
type TicketSaleReminderDeliveryUseCase interface {
	// DeliverReminder delivers the reminder described by data synchronously: it
	// renders and records the notification and sends it to the user's push
	// subscriptions (the same delivery logic NotificationUseCase.Deliver uses,
	// composed directly rather than through that interface — see
	// notification_delivery.go). It enforces the once-only contract via
	// AlreadySent / RecordSent on the sent-log.
	//
	//   - AlreadySent guard fires first (at-least-once broker replay protection).
	//   - No linked event → warn and return nil (defensive; the scan always names one).
	//   - No push subscription → record sent (suppress future re-delivery), return nil.
	//   - RecordSent is called ONLY when the notification was delivered (or there
	//     was no device to deliver to). A transient send failure leaves the
	//     sent-log empty so the next scan can retry.
	DeliverReminder(ctx context.Context, data entity.TicketSaleReminderDueData) error

	// RunDeliverReminder is the deliver-reminder workflow, started by
	// ScanDueReminders by name ([WorkflowDeliverReminder]).
	RunDeliverReminder(ctx context.Context, f Flow, in entity.TicketSaleReminderDueData) (struct{}, error)
}

type ticketSaleReminderDeliveryUseCase struct {
	reminderRepo entity.TicketSaleReminderRepository
	// delivery composes the entity ports directly (see notification_delivery.go)
	// instead of depending on NotificationUseCase's interface: DeliverReminder
	// stays synchronous (no event hop) and shares the same delivery logic that
	// NotificationUseCase.Deliver uses.
	delivery notificationDeliveryDeps
	logger   *logging.Logger
}

// Compile-time interface compliance check.
var _ TicketSaleReminderDeliveryUseCase = (*ticketSaleReminderDeliveryUseCase)(nil)

// NewTicketSaleReminderDeliveryUseCase wires the reminder delivery use case.
func NewTicketSaleReminderDeliveryUseCase(
	reminderRepo entity.TicketSaleReminderRepository,
	notificationRepo entity.NotificationRepository,
	pushSubRepo entity.PushSubscriptionRepository,
	sender entity.PushNotificationSender,
	subjects SubjectDeps,
	metrics PushMetrics,
	logger *logging.Logger,
) *ticketSaleReminderDeliveryUseCase {
	return &ticketSaleReminderDeliveryUseCase{
		reminderRepo: reminderRepo,
		delivery: notificationDeliveryDeps{
			notificationRepo: notificationRepo,
			pushSubRepo:      pushSubRepo,
			sender:           sender,
			subjects:         subjects,
			metrics:          metrics,
			logger:           logger,
		},
		logger: logger,
	}
}

// logDeliveryOutcome records a structured log line for a terminal reminder
// delivery outcome. This log gives the delivery-reliability visibility
// (no_subscription / failed per stage) that operational telemetry needs. A log-based metric keys on the
// stable message "sales_reminder delivery outcome" and the `outcome` /
// `phase_stage` fields. Non-success outcomes are logged at Warn so they
// surface without a metric; the success case stays at Info.
func (uc *ticketSaleReminderDeliveryUseCase) logDeliveryOutcome(
	ctx context.Context,
	data entity.TicketSaleReminderDueData,
	stage entity.ReminderStage,
	outcome string,
) {
	attrs := analyticslog.Attrs(analyticslog.TopicNotificationDelivery,
		slog.String("outcome", outcome),
		slog.String("phase_stage", stage.String()),
		slog.String("user_id", data.UserID),
		slog.String("ticket_sale_id", data.TicketSaleID),
	)
	if outcome == "delivered" {
		uc.logger.Info(ctx, "sales_reminder delivery outcome", attrs...)
		return
	}
	uc.logger.Warn(ctx, "sales_reminder delivery outcome", attrs...)
}

// DeliverReminder implements [TicketSaleReminderDeliveryUseCase].
func (uc *ticketSaleReminderDeliveryUseCase) DeliverReminder(ctx context.Context, data entity.TicketSaleReminderDueData) error {
	stage := entity.ReminderStage(data.Stage)
	attrs := []slog.Attr{
		slog.String("user_id", data.UserID),
		slog.String("ticket_sale_id", data.TicketSaleID),
		slog.Int("stage", int(stage)),
	}

	// Once-only guard: re-check AlreadySent to prevent double-send on
	// at-least-once broker replay. Not a terminal delivery outcome — no analytics emit.
	already, err := uc.reminderRepo.AlreadySent(ctx, data.UserID, entity.TicketSaleID(data.TicketSaleID), stage)
	if err != nil {
		return fmt.Errorf("ticket_sale_reminder_delivery: AlreadySent check: %w", err)
	}
	if already {
		uc.logger.Info(ctx, "ticket_sale_reminder_delivery: already sent, skipping", attrs...)
		return nil
	}

	// A request without a linked event is a defensive skip — the scan always
	// names one. Not a terminal delivery outcome because no send was attempted,
	// and not an error (returning one would poison-loop the request).
	if data.LinkEventID == "" {
		uc.logger.Warn(ctx, "ticket_sale_reminder_delivery: no linked event, skipping", attrs...)
		return nil
	}

	// Render, record and dispatch directly (see notification_delivery.go). A
	// failure to read or record surfaces here so the request runs again.
	outcome, err := deliverNotification(ctx, uc.delivery, DeliverRequest{
		UserID: data.UserID,
		Type:   entity.NotificationTypeSalesReminder,
		Subject: entity.NotificationSubject{
			TicketSaleID: data.TicketSaleID,
			Stage:        stage,
			EventID:      data.LinkEventID,
		},
	})
	if err != nil {
		return fmt.Errorf("ticket_sale_reminder_delivery: deliver: %w", err)
	}

	switch {
	case outcome.Status == DeliveryStatusDelivered:
		// At least one subscription accepted the push. Record sent so the next
		// scan does not re-deliver, then log the terminal delivered outcome.
		if err := uc.reminderRepo.RecordSent(ctx, data.UserID, entity.TicketSaleID(data.TicketSaleID), stage); err != nil {
			uc.logger.Error(ctx, "ticket_sale_reminder_delivery: RecordSent failed", err, attrs...)
			// Non-fatal: next scan will re-check via ListSentStages / AlreadySent.
		}
		uc.logDeliveryOutcome(ctx, data, stage, "delivered")
	case outcome.FailureReason == NotificationFailureReasonNoSubscription:
		// The user has no push device to deliver to — nothing to retry. Record
		// sent to suppress future re-delivery and log the no_subscription outcome.
		if err := uc.reminderRepo.RecordSent(ctx, data.UserID, entity.TicketSaleID(data.TicketSaleID), stage); err != nil {
			uc.logger.Error(ctx, "ticket_sale_reminder_delivery: RecordSent failed", err, attrs...)
		}
		uc.logDeliveryOutcome(ctx, data, stage, "no_subscription")
	default:
		// Transient send failure — leave the sent-log empty so the next scan
		// retries, and log the terminal failed outcome.
		uc.logDeliveryOutcome(ctx, data, stage, "failed")
	}
	return nil
}
