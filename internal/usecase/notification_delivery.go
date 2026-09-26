package usecase

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-logging/logging"
)

// notificationDeliveryDeps bundles the entity ports and infrastructure needed
// to record and dispatch a single notification. It exists so the
// record-then-send-then-record-outcome logic is composed, not called through
// another usecase interface, by both:
//   - NotificationUseCase.Deliver, driven by the deliver-notification
//     consumer reacting to NOTIFICATION.requested; and
//   - SalesReminderDeliveryUseCase.DeliverReminder, which stays synchronous
//     (no event hop) and calls deliverNotification directly.
type notificationDeliveryDeps struct {
	notificationRepo entity.NotificationRepository
	pushSubRepo      entity.PushSubscriptionRepository
	sender           entity.PushNotificationSender
	publisher        EventPublisher
	metrics          PushMetrics
	logger           *logging.Logger
}

// NotificationFailureReasonNoSubscription is the delivery failure reason recorded
// when a notification has no web-push endpoint to deliver to. Producers can branch
// on it to distinguish "the user simply has no push device" (a terminal outcome
// not worth retrying) from a transient send error.
const NotificationFailureReasonNoSubscription = "no active push subscription"

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
)

// deliverNotification records a Notification for userID, dispatches it to the
// recipient's web-push subscriptions, and records the delivery outcome. This
// is the "Notify"/"Deliver" body shared by every caller composing the same
// entity ports (see [notificationDeliveryDeps]).
//
// The record is created first and is the source of truth. If record creation
// fails the send is NOT attempted and the error is returned, so the caller's
// own at-least-once retry path re-drives it (the "no record => no send"
// invariant: an unrecorded send is exactly the unobservable silent-delivery
// failure this service exists to eliminate). A subsequent channel send failure
// is recorded as failed (not returned) so the record stays auditable and the
// send is re-dispatchable.
//
// payload is mutated to carry the minted notification id under
// data.notification_id, establishing the end-to-end correlation key. Callers
// are responsible for rejecting a nil payload themselves — the two current
// callers disagree on what a nil payload means (an InvalidArgument error for
// NotificationUseCase.Deliver; a defensive no-op skip for
// SalesReminderDeliveryUseCase.DeliverReminder) — so this function assumes a
// non-nil payload.
func deliverNotification(
	ctx context.Context,
	d notificationDeliveryDeps,
	userID string,
	typ entity.NotificationType,
	payload *entity.NotificationPayload,
) (*entity.Notification, error) {
	// 1. Create the record first — it is the source of truth. On failure, do NOT
	//    send blind: surface the error so the caller's retry path re-drives it.
	n := &entity.Notification{
		UserID:         userID,
		Type:           typ,
		Payload:        payload,
		DeliveryStatus: entity.NotificationDeliveryStatusQueued,
	}
	if err := d.notificationRepo.Create(ctx, n); err != nil {
		return nil, fmt.Errorf("failed to create notification record: %w", err)
	}

	// 2. Carry the stable notification id into the dispatched payload so the
	//    client/service worker can correlate interactions back to this record.
	if payload.Data == nil {
		payload.Data = make(map[string]string, 1)
	}
	payload.Data[entity.NotificationDataKeyNotificationID] = n.ID

	// 3. Dispatch to the web-push channel and 4. record the outcome.
	status, deliveredAt, reason, reasonCategory := d.dispatch(ctx, n, payload)

	// Surface the outcome as an operational signal so a systemic delivery failure
	// is detectable without querying the notifications table. The metric is emitted
	// for every outcome (the alert needs a failed-vs-total ratio); a failed
	// delivery is additionally logged at WARNING, with the bounded failure_reason
	// as a label and the unbounded detail kept separate for debugging.
	d.metrics.RecordDeliveryOutcome(ctx, string(status), reasonCategory)
	if status == entity.NotificationDeliveryStatusFailed {
		d.logger.Warn(ctx, "notification delivery failed",
			slog.String("notification_id", n.ID),
			slog.String("user_id", userID),
			slog.String("notification_type", string(typ)),
			slog.String("failure_reason", reasonCategory),
			slog.String("failure_detail", reason),
		)
	}

	if err := d.notificationRepo.UpdateDelivery(ctx, n.ID, status, deliveredAt, reason); err != nil {
		// Non-fatal: the send has already happened (or failed) and the record
		// exists. The delivery-state column may lag but the notification is not
		// lost; a reconcile/re-dispatch can correct it.
		d.logger.Error(ctx, "failed to update notification delivery state", err,
			slog.String("notification_id", n.ID),
			slog.String("user_id", userID),
		)
	}
	n.DeliveryStatus = status
	n.DeliverTime = deliveredAt
	n.FailureReason = reason

	// 5. Emit the delivered analytics event exactly once per notification, only
	//    when the send actually reached the delivered state. Non-fatal by design:
	//    analytics must never affect the delivery outcome, so a publish failure is
	//    logged and swallowed (mirrors the notification.subscribed emit).
	if status == entity.NotificationDeliveryStatusDelivered {
		if err := d.publisher.PublishEvent(ctx, entity.SubjectNotificationDelivered, entity.NotificationDeliveredData{
			UserID:         userID,
			NotificationID: n.ID,
			Type:           string(typ),
		}); err != nil {
			d.logger.Error(ctx, "failed to publish NOTIFICATION.delivered event", err,
				slog.String("notification_id", n.ID),
				slog.String("user_id", userID),
			)
		}
	}

	return n, nil
}

// dispatch sends the rendered payload to every web-push subscription the user
// has, cleaning up gone (410) subscriptions, and returns the terminal delivery
// outcome: delivered when at least one send was accepted, otherwise failed (with
// a human-readable reason and a bounded reason category for metric/log labels).
// It never returns an error — a failed dispatch is a recorded outcome, not a lost
// notification.
func (d notificationDeliveryDeps) dispatch(ctx context.Context, n *entity.Notification, payload *entity.NotificationPayload) (status entity.NotificationDeliveryStatus, deliveredAt *time.Time, reason, reasonCategory string) {
	subs, err := d.pushSubRepo.ListByUserIDs(ctx, []string{n.UserID})
	if err != nil {
		d.logger.Error(ctx, "failed to list push subscriptions for notification", err,
			slog.String("notification_id", n.ID),
			slog.String("user_id", n.UserID),
		)
		return entity.NotificationDeliveryStatusFailed, nil, "failed to list push subscriptions: " + err.Error(), deliveryFailureReasonListFailed
	}
	if len(subs) == 0 {
		// The record still exists for the in-app inbox; the push channel simply
		// had no endpoint to deliver to. Recorded as failed for delivery audit.
		return entity.NotificationDeliveryStatusFailed, nil, NotificationFailureReasonNoSubscription, deliveryFailureReasonNoSubscription
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return entity.NotificationDeliveryStatusFailed, nil, "failed to marshal payload: " + err.Error(), deliveryFailureReasonMarshalFailed
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
				if delErr := d.pushSubRepo.Delete(ctx, sub.UserID, sub.Endpoint); delErr != nil {
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
		now := time.Now().UTC()
		return entity.NotificationDeliveryStatusDelivered, &now, "", deliveryFailureReasonNone
	}
	return entity.NotificationDeliveryStatusFailed, nil, lastErr, lastCategory
}

// notificationRequestMsgID derives a deterministic id for a NOTIFICATION.requested
// publish from stable business keys (never from randomness or wall-clock time),
// so an at-least-once retry of the same triggering event re-publishes the same
// id for the same recipient. Passed as the EventPublisher.PublishEventWithID id,
// it becomes the NATS Msg-Id the JetStream stream's Duplicates window (2 minutes,
// see infrastructure/messaging/streams.go) deduplicates broker-side — a retried
// trigger re-requests delivery for the same recipients without queuing a second,
// redundant request. It is unrelated to and never reused as the eventual
// entity.Notification.ID (which NotificationRepository.Create still mints fresh).
func notificationRequestMsgID(typ entity.NotificationType, userID, correlationKey string) string {
	sum := sha256.Sum256([]byte(string(typ) + "\x00" + userID + "\x00" + correlationKey))
	return hex.EncodeToString(sum[:])
}
