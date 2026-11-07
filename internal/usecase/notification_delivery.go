package usecase

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/pkg/analyticslog"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-logging/logging"
)

// notificationDeliveryDeps bundles the entity ports and infrastructure needed
// to render, record and dispatch a single notification. It exists so the
// logic is composed, not called through another usecase interface, by both:
//   - NotificationUseCase.Deliver, run by the deliver-notification workflow;
//     and
//   - TicketSaleReminderDeliveryUseCase.DeliverReminder, run by the
//     deliver-reminder workflow, which calls deliverNotification directly.
type notificationDeliveryDeps struct {
	notificationRepo entity.NotificationRepository
	pushSubRepo      entity.PushSubscriptionRepository
	sender           entity.PushNotificationSender
	subjects         SubjectDeps
	metrics          PushMetrics
	logger           *logging.Logger
}

// DeliveryStatus is whether the push channel accepted a notification's message.
type DeliveryStatus string

const (
	// DeliveryStatusDelivered means at least one browser's send was accepted by
	// the push service, not that a device displayed it.
	DeliveryStatusDelivered DeliveryStatus = "delivered"
	// DeliveryStatusFailed means no send was accepted.
	DeliveryStatusFailed DeliveryStatus = "failed"
)

// DeliveryOutcome is what Deliver returns to its caller. It is not stored.
type DeliveryOutcome struct {
	// NotificationID is the recorded Notification; empty when nothing was
	// recorded because the subject no longer exists.
	NotificationID string
	// Status is whether the push was accepted.
	Status DeliveryStatus
	// FailureReason says why, when Status is Failed.
	FailureReason string
}

// NotificationFailureReasonNoSubscription is the delivery failure reason recorded
// when a notification has no web-push endpoint to deliver to. Producers can branch
// on it to distinguish "the user simply has no push device" (a terminal outcome
// not worth retrying) from a transient send error.
const NotificationFailureReasonNoSubscription = "no active push subscription"

// NotificationFailureReasonSubjectGone is the failure reason when a record the
// subject names no longer exists, so nothing was recorded or sent.
const NotificationFailureReasonSubjectGone = "subject no longer exists"

// Bounded delivery-failure categories. They are the *label* attached to the
// delivery-outcome metric and the WARNING failure log, kept deliberately small so
// the signal stays low-cardinality and safe for a log-based-metric alert (the raw
// reason string carries the unbounded diagnostic detail separately).
const (
	deliveryFailureReasonNone           = "none"
	deliveryFailureReasonNoSubscription = "no_subscription"
	deliveryFailureReasonGone           = "gone"
	deliveryFailureReasonSendError      = "send_error"
	deliveryFailureReasonListFailed     = "list_failed"
	deliveryFailureReasonMarshalFailed  = "marshal_failed"
	deliveryFailureReasonCancelled      = "cancelled"
	deliveryFailureReasonSubjectGone    = "subject_gone"
)

// deliverNotification renders the requested notification from its subject,
// records it, dispatches it to the recipient's web-push subscriptions, and
// returns and logs the outcome. This is the body shared by every caller
// composing the same entity ports (see [notificationDeliveryDeps]).
//
// Nothing is sent before the record exists: a failure to read or record returns
// the error, so the caller's own retry path runs it again. A subject whose
// record no longer exists records and sends nothing and succeeds with a Failed
// outcome. A channel send failure is a Failed outcome, not an error.
func deliverNotification(ctx context.Context, d notificationDeliveryDeps, req DeliverRequest) (DeliveryOutcome, error) {
	if err := req.Subject.Validate(req.Type); err != nil {
		return DeliveryOutcome{}, err
	}
	user, err := d.subjects.Users.Get(ctx, req.UserID)
	if err != nil {
		return DeliveryOutcome{}, fmt.Errorf("failed to read the notification's user: %w", err)
	}
	follows, err := d.subjects.Follows.ListByUser(ctx, req.UserID)
	if err != nil {
		return DeliveryOutcome{}, fmt.Errorf("failed to read the follows of the notification's user: %w", err)
	}

	n := &entity.Notification{UserID: req.UserID, Type: req.Type, Subject: req.Subject}
	subject, err := d.subjects.load(ctx, n, user, follows)
	if errors.Is(err, errSubjectGone) {
		d.logOutcome(ctx, n, DeliveryOutcome{Status: DeliveryStatusFailed, FailureReason: NotificationFailureReasonSubjectGone}, deliveryFailureReasonSubjectGone)
		return DeliveryOutcome{Status: DeliveryStatusFailed, FailureReason: NotificationFailureReasonSubjectGone}, nil
	}
	if err != nil {
		return DeliveryOutcome{}, fmt.Errorf("failed to read the notification's subject: %w", err)
	}
	payload := n.Message(user.PreferredLanguage, subject)

	if err := d.notificationRepo.Create(ctx, n); err != nil {
		return DeliveryOutcome{}, fmt.Errorf("failed to create notification record: %w", err)
	}

	// Carry the stable notification id and its type into the dispatched payload
	// so the client/service worker can correlate interactions back to this
	// record and report them by type, and name a reminder's sale and milestone
	// so a fan's reaction can be told apart by milestone.
	payload.Data[entity.NotificationDataKeyNotificationID] = n.ID
	payload.Data[entity.NotificationDataKeyType] = string(req.Type)
	if req.Type == entity.NotificationTypeSalesReminder {
		payload.Data[entity.NotificationDataKeyTicketSaleID] = req.Subject.TicketSaleID
		payload.Data[entity.NotificationDataKeyMilestone] = strings.ToLower(req.Subject.Stage.String())
	}

	status, reason, reasonCategory := d.dispatch(ctx, n, payload)
	outcome := DeliveryOutcome{NotificationID: n.ID, Status: status, FailureReason: reason}
	d.logOutcome(ctx, n, outcome, reasonCategory)
	return outcome, nil
}

// logOutcome surfaces the outcome as an operational signal: a metric for every
// outcome (the alert needs a failed-vs-total ratio) and a log line marked for
// analytics, so delivery rates can be read over months. A delivery logs at
// INFO, a failure at WARNING with the bounded failure_reason as a label and the
// unbounded detail kept separate for debugging.
func (d notificationDeliveryDeps) logOutcome(ctx context.Context, n *entity.Notification, outcome DeliveryOutcome, reasonCategory string) {
	d.metrics.RecordDeliveryOutcome(ctx, string(outcome.Status), reasonCategory)
	attrs := []slog.Attr{
		slog.String("user_id", n.UserID),
		slog.String("notification_type", string(n.Type)),
	}
	if n.ID != "" {
		attrs = append(attrs, slog.String("notification_id", n.ID))
	}
	if outcome.Status == DeliveryStatusDelivered {
		d.logger.Info(ctx, "notification delivered", analyticslog.Attrs(analyticslog.TopicNotificationDelivery, attrs...)...)
		return
	}
	attrs = append(attrs,
		slog.String("failure_reason", reasonCategory),
		slog.String("failure_detail", outcome.FailureReason),
	)
	d.logger.Warn(ctx, "notification delivery failed", analyticslog.Attrs(analyticslog.TopicNotificationDelivery, attrs...)...)
}

// dispatch sends the rendered payload to every web-push subscription the user
// has, cleaning up gone (410) subscriptions, and returns the terminal delivery
// outcome: delivered when at least one send was accepted, otherwise failed (with
// a human-readable reason and a bounded reason category for metric/log labels).
// It never returns an error — a failed dispatch is an outcome, not a lost
// notification.
func (d notificationDeliveryDeps) dispatch(ctx context.Context, n *entity.Notification, payload *entity.NotificationPayload) (status DeliveryStatus, reason, reasonCategory string) {
	subs, err := d.pushSubRepo.ListByUserIDs(ctx, []string{n.UserID})
	if err != nil {
		d.logger.Error(ctx, "failed to list push subscriptions for notification", err,
			slog.String("notification_id", n.ID),
			slog.String("user_id", n.UserID),
		)
		return DeliveryStatusFailed, "failed to list push subscriptions: " + err.Error(), deliveryFailureReasonListFailed
	}
	if len(subs) == 0 {
		// The record still exists for the notification center; the push channel
		// simply had no endpoint to deliver to.
		return DeliveryStatusFailed, NotificationFailureReasonNoSubscription, deliveryFailureReasonNoSubscription
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return DeliveryStatusFailed, "failed to marshal payload: " + err.Error(), deliveryFailureReasonMarshalFailed
	}

	var (
		atLeastOneSuccess bool
		lastErr           string
		lastCategory      string
	)
	for _, sub := range subs {
		// Stop dispatching the moment the context is cancelled: record the cause
		// and break so no further sends are issued. Any sends already accepted
		// keep the notification's delivered outcome; otherwise it is failed.
		if err := ctx.Err(); err != nil {
			lastErr = err.Error()
			lastCategory = deliveryFailureReasonCancelled
			break
		}

		if err := d.sender.Send(ctx, payloadBytes, sub); err != nil {
			if errors.Is(err, apperr.ErrNotFound) {
				d.metrics.RecordPushSend(ctx, "gone")
				// Scoped cleanup: delete only the dead (userID, endpoint) pair.
				if delErr := d.pushSubRepo.DeleteGone(ctx, sub.UserID, sub.Endpoint); delErr != nil {
					d.logger.Error(ctx, "failed to delete stale push subscription", delErr,
						slog.String("user_id", sub.UserID),
						slog.String("endpoint", sub.Endpoint),
					)
				}
				lastErr = "push subscription gone (410)"
				lastCategory = deliveryFailureReasonGone
			} else {
				d.metrics.RecordPushSend(ctx, "error")
				d.logger.Error(ctx, "failed to send push notification", err,
					slog.String("notification_id", n.ID),
					slog.String("user_id", sub.UserID),
				)
				lastErr = err.Error()
				lastCategory = deliveryFailureReasonSendError
			}
		} else {
			d.metrics.RecordPushSend(ctx, "success")
			atLeastOneSuccess = true
		}
	}

	if atLeastOneSuccess {
		return DeliveryStatusDelivered, "", deliveryFailureReasonNone
	}
	return DeliveryStatusFailed, lastErr, lastCategory
}
