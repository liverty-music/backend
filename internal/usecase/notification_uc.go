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
// interface. TicketSaleReminderDeliveryUseCase.DeliverReminder is the one exception —
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

	// Dismiss marks the (userID, notificationID) notification as dismissed.
	// It is idempotent and user-scoped.
	//
	// # Possible errors
	//
	//   - NotFound: no notification with that id exists.
	//   - PermissionDenied: the notification belongs to a different user.
	Dismiss(ctx context.Context, userID, notificationID string) error

	// SendOrderConfirmation tells the fan of a paid Order that the purchase
	// went through: the confirmation email (at most once per Order) in the
	// fan's language, then an order_confirmation push that opens the Tickets
	// screen. It runs for each ORDER.paid announcement, from a checkout or a
	// won lottery.
	//
	// # Possible errors
	//
	//   - Any read or send failure, unchanged; the announcement is redelivered.
	SendOrderConfirmation(ctx context.Context, paid entity.OrderPaidData) error

	// SendResaleSold tells the user of a resold listing that their ticket
	// sold and how much is refunded: the sold email, at most once per listing,
	// in the user's language. It runs for each ORDER.resale_listing_refunded
	// announcement.
	//
	// # Possible errors
	//
	//   - Any read or send failure, unchanged; the announcement is redelivered.
	SendResaleSold(ctx context.Context, refunded entity.ResaleListingRefundedData) error

	// NotifyNewConcerts sends Web Push notifications to followers of the given
	// artist for the specified newly created concerts. The delivery pipeline
	// hydrates the artist and concert entities internally, applies hype-level
	// filtering, and requests a push notification for all eligible followers.
	//
	// For each eligible recipient, NotifyNewConcerts publishes NOTIFICATION.requested
	// (EventPublisher.PublishEventWithID with a deterministic id derived from the
	// artist, the concert batch, and the recipient — see notification_delivery.go)
	// so the deliver-notification consumer records the durable Notification and
	// dispatches the push; the deterministic id makes an at-least-once retry of
	// this same CONCERT.created delivery a broker-side no-op rather than a second
	// request. Only a publish failure surfaces as an error, so the consumer's
	// at-least-once retry re-drives the batch; per-channel delivery errors
	// (including 410 Gone responses) happen later, inside the deliver-notification
	// consumer, and never reach this method.
	//
	// # Possible errors
	//
	//   - Internal: failure to look up artist, concerts, or followers, or to
	//     publish the notification request.
	NotifyNewConcerts(ctx context.Context, data ConcertCreatedData) error
}

// notificationUseCase implements NotificationUseCase.
type notificationUseCase struct {
	notificationRepo  entity.NotificationRepository
	pushSubRepo       entity.PushSubscriptionRepository
	sender            entity.PushNotificationSender
	publisher         EventPublisher
	metrics           PushMetrics
	logger            *logging.Logger
	orderConfirmation OrderConfirmationDeps
	artistRepo        entity.ArtistRepository
	concertRepo       entity.ConcertRepository
	followRepo        entity.FollowRepository
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
	orderConfirmation OrderConfirmationDeps,
	newConcerts NewConcertsDeps,
) NotificationUseCase {
	return &notificationUseCase{
		notificationRepo:  notificationRepo,
		pushSubRepo:       pushSubRepo,
		sender:            sender,
		publisher:         publisher,
		metrics:           metrics,
		logger:            logger,
		orderConfirmation: orderConfirmation,
		artistRepo:        newConcerts.ArtistRepo,
		concertRepo:       newConcerts.ConcertRepo,
		followRepo:        newConcerts.FollowRepo,
	}
}

// NewConcertsDeps are the dependencies of [NotificationUseCase.NotifyNewConcerts].
// A process that never notifies of new concerts may leave them nil.
type NewConcertsDeps struct {
	ArtistRepo  entity.ArtistRepository
	ConcertRepo entity.ConcertRepository
	FollowRepo  entity.FollowRepository
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

// Dismiss implements [NotificationUseCase].
func (uc *notificationUseCase) Dismiss(ctx context.Context, userID, notificationID string) error {
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
