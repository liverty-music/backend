package usecase

import (
	"context"
	"fmt"

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
//
// Producers never call Deliver directly: they publish NOTIFICATION.requested
// (via EventPublisher.PublishEventWithID) and the deliver-notification event
// consumer calls Deliver, so no usecase depends on NotificationUseCase's
// interface. SalesReminderDeliveryUseCase.DeliverReminder is the one exception —
// it stays synchronous and composes the same delivery ports directly (see
// notification_delivery.go) rather than going through NotificationUseCase.
type NotificationUseCase interface {
	// Deliver records a notification for userID and dispatches it to the user's
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
	Deliver(ctx context.Context, userID string, typ entity.NotificationType, payload *entity.NotificationPayload) (*entity.Notification, error)

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

// notificationUseCase implements NotificationUseCase.
type notificationUseCase struct {
	notificationRepo entity.NotificationRepository
	pushSubRepo      entity.PushSubscriptionRepository
	sender           entity.PushNotificationSender
	publisher        EventPublisher
	metrics          PushMetrics
	logger           *logging.Logger
}

// Compile-time interface compliance check.
var _ NotificationUseCase = (*notificationUseCase)(nil)

// NewNotificationUseCase wires the notification use case. publisher is used to
// emit the non-fatal NOTIFICATION.delivered analytics event at the delivered
// transition; analytics must never affect delivery, so publish failures are
// logged and swallowed.
func NewNotificationUseCase(
	notificationRepo entity.NotificationRepository,
	pushSubRepo entity.PushSubscriptionRepository,
	sender entity.PushNotificationSender,
	publisher EventPublisher,
	metrics PushMetrics,
	logger *logging.Logger,
) NotificationUseCase {
	return &notificationUseCase{
		notificationRepo: notificationRepo,
		pushSubRepo:      pushSubRepo,
		sender:           sender,
		publisher:        publisher,
		metrics:          metrics,
		logger:           logger,
	}
}

// deps returns the entity ports bundled for [deliverNotification].
func (uc *notificationUseCase) deps() notificationDeliveryDeps {
	return notificationDeliveryDeps{
		notificationRepo: uc.notificationRepo,
		pushSubRepo:      uc.pushSubRepo,
		sender:           uc.sender,
		publisher:        uc.publisher,
		metrics:          uc.metrics,
		logger:           uc.logger,
	}
}

// Deliver implements [NotificationUseCase].
func (uc *notificationUseCase) Deliver(ctx context.Context, userID string, typ entity.NotificationType, payload *entity.NotificationPayload) (*entity.Notification, error) {
	if payload == nil {
		return nil, apperr.New(codes.InvalidArgument, "notification payload must not be nil")
	}
	return deliverNotification(ctx, uc.deps(), userID, typ, payload)
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
