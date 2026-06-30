package usecase

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
)

// NotificationUseCase is the single entry point through which every user-facing
// notification is produced. It persists a durable record (the source of truth)
// before dispatching to the web-push channel, then records the delivery outcome,
// so that "did this notification reach the user?" is answerable from stored state
// and the in-app inbox has durable, per-user read/dismiss state to build on.
type NotificationUseCase interface {
	// Notify records a notification for userID and dispatches it to the user's
	// web-push subscriptions.
	//
	// The record is created first and is the source of truth. If record creation
	// fails the send is NOT attempted and the error is returned, so the caller's
	// existing at-least-once retry path re-drives it (the "no record => no send"
	// invariant: an unrecorded send is exactly the unobservable silent-delivery
	// failure this service exists to eliminate). A subsequent channel send failure
	// is recorded as failed (not returned) so the record stays auditable and the
	// send is re-dispatchable.
	//
	// payload is mutated to carry the minted notification id under
	// data.notification_id, establishing the end-to-end correlation key.
	//
	// # Possible errors
	//
	//   - InvalidArgument: payload is nil.
	//   - Internal: notification record creation failed (no send was attempted).
	Notify(ctx context.Context, userID string, typ entity.NotificationType, payload *entity.NotificationPayload) (*entity.Notification, error)

	// MarkRead marks the (userID, notificationID) notification as read. It is
	// idempotent and user-scoped: marking another user's notification is rejected.
	//
	// # Possible errors
	//
	//   - NotFound: no notification with that id exists.
	//   - PermissionDenied: the notification belongs to a different user.
	MarkRead(ctx context.Context, userID, notificationID string) error

	// MarkDismissed marks the (userID, notificationID) notification as dismissed.
	// It is idempotent and user-scoped.
	//
	// # Possible errors
	//
	//   - NotFound: no notification with that id exists.
	//   - PermissionDenied: the notification belongs to a different user.
	MarkDismissed(ctx context.Context, userID, notificationID string) error
}

// NotificationFailureReasonNoSubscription is the delivery failure reason recorded
// when a notification has no web-push endpoint to deliver to. Producers can branch
// on it to distinguish "the user simply has no push device" (a terminal outcome
// not worth retrying) from a transient send error.
const NotificationFailureReasonNoSubscription = "no active push subscription"

// notificationUseCase implements NotificationUseCase.
type notificationUseCase struct {
	notificationRepo entity.NotificationRepository
	pushSubRepo      entity.PushSubscriptionRepository
	sender           entity.PushNotificationSender
	metrics          PushMetrics
	logger           *logging.Logger
}

// Compile-time interface compliance check.
var _ NotificationUseCase = (*notificationUseCase)(nil)

// NewNotificationUseCase wires the notification use case.
func NewNotificationUseCase(
	notificationRepo entity.NotificationRepository,
	pushSubRepo entity.PushSubscriptionRepository,
	sender entity.PushNotificationSender,
	metrics PushMetrics,
	logger *logging.Logger,
) NotificationUseCase {
	return &notificationUseCase{
		notificationRepo: notificationRepo,
		pushSubRepo:      pushSubRepo,
		sender:           sender,
		metrics:          metrics,
		logger:           logger,
	}
}

// Notify implements [NotificationUseCase].
func (uc *notificationUseCase) Notify(ctx context.Context, userID string, typ entity.NotificationType, payload *entity.NotificationPayload) (*entity.Notification, error) {
	if payload == nil {
		return nil, apperr.New(codes.InvalidArgument, "notification payload must not be nil")
	}

	// 1. Create the record first — it is the source of truth. On failure, do NOT
	//    send blind: surface the error so the caller's retry path re-drives it.
	n := &entity.Notification{
		UserID:         userID,
		Type:           typ,
		Payload:        payload,
		DeliveryStatus: entity.NotificationDeliveryStatusQueued,
	}
	if err := uc.notificationRepo.Create(ctx, n); err != nil {
		return nil, fmt.Errorf("failed to create notification record: %w", err)
	}

	// 2. Carry the stable notification id into the dispatched payload so the
	//    client/service worker can correlate interactions back to this record.
	if payload.Data == nil {
		payload.Data = make(map[string]string, 1)
	}
	payload.Data[entity.NotificationDataKeyNotificationID] = n.ID

	// 3. Dispatch to the web-push channel and 4. record the outcome.
	status, deliveredAt, reason := uc.dispatch(ctx, n, payload)
	if err := uc.notificationRepo.UpdateDelivery(ctx, n.ID, status, deliveredAt, reason); err != nil {
		// Non-fatal: the send has already happened (or failed) and the record
		// exists. The delivery-state column may lag but the notification is not
		// lost; a reconcile/re-dispatch can correct it.
		uc.logger.Error(ctx, "failed to update notification delivery state", err,
			slog.String("notification_id", n.ID),
			slog.String("user_id", userID),
		)
	}
	n.DeliveryStatus = status
	n.DeliverTime = deliveredAt
	n.FailureReason = reason
	return n, nil
}

// dispatch sends the rendered payload to every web-push subscription the user
// has, cleaning up gone (410) subscriptions, and returns the terminal delivery
// outcome: delivered when at least one send was accepted, otherwise failed (with
// a reason). It never returns an error — a failed dispatch is a recorded outcome,
// not a lost notification.
func (uc *notificationUseCase) dispatch(ctx context.Context, n *entity.Notification, payload *entity.NotificationPayload) (entity.NotificationDeliveryStatus, *time.Time, string) {
	subs, err := uc.pushSubRepo.ListByUserIDs(ctx, []string{n.UserID})
	if err != nil {
		uc.logger.Error(ctx, "failed to list push subscriptions for notification", err,
			slog.String("notification_id", n.ID),
			slog.String("user_id", n.UserID),
		)
		return entity.NotificationDeliveryStatusFailed, nil, "failed to list push subscriptions: " + err.Error()
	}
	if len(subs) == 0 {
		// The record still exists for the in-app inbox; the push channel simply
		// had no endpoint to deliver to. Recorded as failed for delivery audit.
		return entity.NotificationDeliveryStatusFailed, nil, NotificationFailureReasonNoSubscription
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return entity.NotificationDeliveryStatusFailed, nil, "failed to marshal payload: " + err.Error()
	}

	var (
		atLeastOneSuccess bool
		lastErr           string
	)
	for _, sub := range subs {
		// Stop dispatching the moment the context is cancelled: record the cause
		// and break so no further sends are issued. Any sends already accepted
		// keep the notification's delivered outcome; otherwise it is failed.
		if err := ctx.Err(); err != nil {
			lastErr = err.Error()
			break
		}

		if err := uc.sender.Send(ctx, payloadBytes, sub); err != nil {
			if errors.Is(err, apperr.ErrNotFound) {
				uc.metrics.RecordPushSend(ctx, "gone")
				// Scoped cleanup: delete only the dead (userID, endpoint) pair.
				if delErr := uc.pushSubRepo.Delete(ctx, sub.UserID, sub.Endpoint); delErr != nil {
					uc.logger.Error(ctx, "failed to delete stale push subscription", delErr,
						slog.String("user_id", sub.UserID),
						slog.String("endpoint", sub.Endpoint),
					)
				}
				lastErr = "push subscription gone (410)"
			} else {
				uc.metrics.RecordPushSend(ctx, "error")
				uc.logger.Error(ctx, "failed to send push notification", err,
					slog.String("notification_id", n.ID),
					slog.String("user_id", sub.UserID),
				)
				lastErr = err.Error()
			}
		} else {
			uc.metrics.RecordPushSend(ctx, "success")
			atLeastOneSuccess = true
		}
	}

	if atLeastOneSuccess {
		now := time.Now().UTC()
		return entity.NotificationDeliveryStatusDelivered, &now, ""
	}
	return entity.NotificationDeliveryStatusFailed, nil, lastErr
}

// MarkRead implements [NotificationUseCase].
func (uc *notificationUseCase) MarkRead(ctx context.Context, userID, notificationID string) error {
	if err := uc.assertOwnership(ctx, userID, notificationID); err != nil {
		return err
	}
	if err := uc.notificationRepo.MarkRead(ctx, userID, notificationID); err != nil {
		return fmt.Errorf("failed to mark notification read: %w", err)
	}
	return nil
}

// MarkDismissed implements [NotificationUseCase].
func (uc *notificationUseCase) MarkDismissed(ctx context.Context, userID, notificationID string) error {
	if err := uc.assertOwnership(ctx, userID, notificationID); err != nil {
		return err
	}
	if err := uc.notificationRepo.MarkDismissed(ctx, userID, notificationID); err != nil {
		return fmt.Errorf("failed to mark notification dismissed: %w", err)
	}
	return nil
}

// assertOwnership loads the notification and rejects the request when it belongs
// to a different user, so a user cannot change another user's notification state.
func (uc *notificationUseCase) assertOwnership(ctx context.Context, userID, notificationID string) error {
	n, err := uc.notificationRepo.Get(ctx, notificationID)
	if err != nil {
		return fmt.Errorf("failed to load notification: %w", err)
	}
	if n.UserID != userID {
		return apperr.New(codes.PermissionDenied, "notification does not belong to the requesting user")
	}
	return nil
}
