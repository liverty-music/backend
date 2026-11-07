package usecase

import (
	"context"
	"errors"
	"fmt"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
)

// NotificationUseCase is the single entry point through which every user-facing
// notification is produced and listed. A Notification records what it is about
// before the push is sent, so the fan's notification center shows it whether or
// not the push arrived; the message is rendered from its subject each time.
//
// Producers never call Deliver directly: they start the deliver-notification
// workflow, which the workflow worker runs through RunDeliver, so no usecase
// depends on NotificationUseCase's interface.
// TicketSaleReminderDeliveryUseCase.DeliverReminder is the one exception — it
// composes the same delivery ports directly (see notification_delivery.go)
// rather than going through NotificationUseCase.
type NotificationUseCase interface {
	// Deliver renders the requested notification from the records its subject
	// names, records it, sends it to the fan's web-push subscriptions, and
	// returns and logs the outcome without storing it.
	//
	// Nothing is sent before the record exists. When a record the subject names
	// no longer exists, nothing is recorded or sent and the outcome is Failed
	// with [NotificationFailureReasonSubjectGone]. A send failure is a Failed
	// outcome, not an error.
	//
	// # Possible errors
	//
	//   - InvalidArgument: the subject is invalid for the type.
	//   - Any other read failure or failure to record, unchanged; nothing was
	//     sent and the caller runs it again.
	Deliver(ctx context.Context, req DeliverRequest) (DeliveryOutcome, error)

	// List returns the fan's newest Notifications (at most [notificationListLimit]),
	// newest first, each rendered in the fan's current language. One whose
	// subject's record no longer exists is left out.
	//
	// # Possible errors
	//
	//   - Any read failure, unchanged.
	List(ctx context.Context, user *entity.User) ([]*ListedNotification, error)

	// RunDeliver is the deliver-notification workflow: it runs Deliver for the
	// request and returns the Notification's id. Producers start it by name
	// ([WorkflowDeliverNotification]) instead of calling Deliver.
	RunDeliver(ctx context.Context, f Flow, in DeliverRequest) (string, error)

	// MarkRead marks the (userID, notificationID) notification as read. It is
	// idempotent and user-scoped: marking another user's notification is rejected.
	//
	// # Possible errors
	//
	//   - NotFound: no notification with that id exists.
	//   - PermissionDenied: the notification belongs to a different user.
	MarkRead(ctx context.Context, userID, notificationID string) error

	// SendOrderConfirmation tells the fan of a paid Order that the purchase
	// went through: the confirmation email (at most once per Order) in the
	// fan's language, then, for an Order of a won lottery or resale entry, a
	// lottery_win or resale_win push that opens the Tickets screen. An Order of
	// a Checkout is not pushed: the fan has just paid on screen. It runs for
	// each ORDER.paid announcement.
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

	// SendLotteryLoss tells the fan of a Lost lottery entry that it lost and
	// that nothing is charged: the loss email, at most once per entry, in the
	// user's language, then a lottery_loss push that opens the event's page,
	// named after the entry so a repeated announcement pushes once. It runs
	// for each entry announced Lost (ORDER.lottery_entry_drawn); an entry that
	// is not Lost sends nothing.
	//
	// # Possible errors
	//
	//   - Any read or send failure, unchanged; the announcement is redelivered.
	SendLotteryLoss(ctx context.Context, entryID entity.LotteryEntryID) error

	// SendResaleEntryEnded tells the fan of a Lapsed or Lost resale entry
	// that it ended and nothing was charged: an email sent at most once per
	// entry (ResaleEntry.SendEndedEmail) and a push started as Deliver named
	// after the type and the entry. An entry in another state sends nothing.
	//
	// # Possible errors
	//
	//  - Errors of reading the entry, its user, stock or concert, or of
	//    sending, unchanged; the caller runs it again.
	SendResaleEntryEnded(ctx context.Context, entryID entity.ResaleEntryID) error

	// SendEventCancelled tells each fan with a Paid Order of the cancelled
	// event that the concert is cancelled and the tickets they still hold are
	// refunded: it lists the event's Orders and starts, for each Paid Order,
	// the notify-event-cancelled workflow named after the Order, so a
	// repeated announcement starts nothing new. A failure to start one
	// Order's notice does not stop the others.
	//
	// # Possible errors
	//
	//  - Errors of listing the Orders, unchanged.
	//  - Unavailable: a notice could not be started; the caller runs it again.
	SendEventCancelled(ctx context.Context, eventID string) error

	// RunNotifyEventCancelled is the notify-event-cancelled workflow of one
	// Order: it reads the Order, its user, concert and Tickets in one step,
	// sends the cancellation email (Order.SendCancellationEmail) in a second,
	// then starts Deliver named after the type concert_cancelled and the Order.
	// An Order that no longer exists, is no longer Paid or holds no Issued
	// Ticket gets nothing.
	RunNotifyEventCancelled(ctx context.Context, f Flow, in OrderNotice) (struct{}, error)

	// SendOrderRefunded starts the notify-order-refunded workflow of the
	// refunded Order, named after the Order, so a repeated announcement
	// starts nothing new.
	//
	// # Possible errors
	//
	//  - Unavailable: the notice could not be started; the caller runs it
	//    again.
	SendOrderRefunded(ctx context.Context, orderID entity.OrderID) error

	// RunNotifyOrderRefunded is the notify-order-refunded workflow of one
	// Order: it reads the Order, its user and concert in one step, sends the
	// refund email (Order.SendRefundEmail) in a second, then starts Deliver
	// named after the type order_refunded and the Order. An Order that no
	// longer exists, was refunded for a dispute or refunded 0 gets nothing.
	RunNotifyOrderRefunded(ctx context.Context, f Flow, in OrderNotice) (struct{}, error)

	// NotifyNewConcerts sends Web Push notifications to followers of the given
	// artist for the specified newly created concerts. The delivery pipeline
	// hydrates the artist and concert entities internally, applies hype-level
	// filtering, and requests a push notification for all eligible followers.
	//
	// For each eligible recipient, NotifyNewConcerts starts the
	// deliver-notification workflow, which records the durable Notification and
	// dispatches the push; it is named after the recipient and the
	// announcement, so a redelivered CONCERT.created starts nothing new. Only a
	// failure to start surfaces as an error, so the consumer's at-least-once
	// retry re-drives the batch; per-channel delivery errors (including 410
	// Gone responses) happen later, inside the workflow, and never reach this
	// method.
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
	workflows         Workflows
	metrics           PushMetrics
	logger            *logging.Logger
	orderConfirmation OrderConfirmationDeps
	artistRepo        entity.ArtistRepository
	concertRepo       entity.ConcertRepository
	followRepo        entity.FollowRepository
	subjects          SubjectDeps
}

// Compile-time interface compliance check.
var _ NotificationUseCase = (*notificationUseCase)(nil)

// NewNotificationUseCase wires the notification use case. workflows starts the
// deliver-notification workflow for each notification a producer requests; a
// process that only runs Deliver may pass nil.
func NewNotificationUseCase(
	notificationRepo entity.NotificationRepository,
	pushSubRepo entity.PushSubscriptionRepository,
	sender entity.PushNotificationSender,
	workflows Workflows,
	metrics PushMetrics,
	logger *logging.Logger,
	orderConfirmation OrderConfirmationDeps,
	newConcerts NewConcertsDeps,
	subjects SubjectDeps,
) NotificationUseCase {
	return &notificationUseCase{
		notificationRepo:  notificationRepo,
		pushSubRepo:       pushSubRepo,
		sender:            sender,
		workflows:         workflows,
		metrics:           metrics,
		logger:            logger,
		orderConfirmation: orderConfirmation,
		artistRepo:        newConcerts.ArtistRepo,
		concertRepo:       newConcerts.ConcertRepo,
		followRepo:        newConcerts.FollowRepo,
		subjects:          subjects,
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
		subjects:         uc.subjects,
		metrics:          uc.metrics,
		logger:           uc.logger,
	}
}

// Deliver implements [NotificationUseCase].
func (uc *notificationUseCase) Deliver(ctx context.Context, req DeliverRequest) (DeliveryOutcome, error) {
	return deliverNotification(ctx, uc.deps(), req)
}

// notificationListLimit is how many Notifications the notification center shows.
const notificationListLimit = 50

// ListedNotification is a stored Notification with its message as rendered now.
type ListedNotification struct {
	// Notification is the stored record.
	Notification *entity.Notification
	// Message is rendered in the fan's current language.
	Message *entity.NotificationPayload
}

// List implements [NotificationUseCase].
func (uc *notificationUseCase) List(ctx context.Context, user *entity.User) ([]*ListedNotification, error) {
	notifications, err := uc.notificationRepo.ListByUser(ctx, user.ID, notificationListLimit)
	if err != nil {
		return nil, fmt.Errorf("failed to list notifications: %w", err)
	}
	if len(notifications) == 0 {
		return []*ListedNotification{}, nil
	}
	follows, err := uc.subjects.Follows.ListByUser(ctx, user.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to read the follows of the notifications' user: %w", err)
	}
	listed := make([]*ListedNotification, 0, len(notifications))
	for _, n := range notifications {
		subject, err := uc.subjects.load(ctx, n, user, follows)
		if errors.Is(err, errSubjectGone) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("failed to read the subject of notification %s: %w", n.ID, err)
		}
		listed = append(listed, &ListedNotification{Notification: n, Message: n.Message(user.PreferredLanguage, subject)})
	}
	return listed, nil
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
