package usecase

import (
	"context"
	"errors"
	"time"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"

	"github.com/liverty-music/backend/internal/entity"
)

// OrderUseCase handles Orders: a fan reads their own Order and an admin reads
// one with its Settlement; ⑤'s post-capture pipeline turns a captured payment
// (a won lottery entry's, or a first-come checkout's after committing and
// charging it) into an Order plus N account-bound covered tickets and a Held
// Settlement for the event's Organizer, together with the Order's ORDER.paid
// announcement, and owns issuance idempotency (one Order per source); and the
// refund policy delegates the money movement to the settlement layer.
type OrderUseCase interface {
	// Get returns one of the calling fan's own Orders. An Order whose user
	// is another account is reported as NotFound (non-revealing), never
	// disclosed.
	//
	// # Possible errors
	//
	//  - NotFound: no such Order for this caller.
	//  - Internal: database query failure.
	Get(ctx context.Context, userID entity.UserID, orderID entity.OrderID) (*entity.Order, error)

	// GetWithSettlement returns the Order and its Settlement with the
	// Settlement's splits. The Settlement is nil when the Order has none; that
	// is not an error.
	//
	// # Possible errors
	//
	//  - NotFound: no Order has the id.
	//  - Errors of reading the Order or the Settlement, unchanged.
	GetWithSettlement(ctx context.Context, orderID entity.OrderID) (*entity.Order, *entity.Settlement, error)

	// ListByEvent returns every Order of the Event, in any status, oldest pay
	// time first, so an admin can see and refund the purchases of a cancelled
	// Event. An Event with no Order, and an id no Event has, yield an empty
	// list.
	//
	// # Possible errors
	//
	//  - InvalidArgument: no Event is given.
	//  - Internal: database query failure.
	ListByEvent(ctx context.Context, eventID string) ([]*entity.Order, error)

	// Refund applies the refund policy for the given reason and executes
	// the money-movement: issues a platform-balance Refund, voids the Order's
	// tickets, sets Order status=refunded, and (if a Settlement exists) reverses
	// any paid-out Transfer(s) and marks the Settlement reversed.
	//
	// It is IDEMPOTENT: a replayed call on an already-refunded Order is a
	// no-op that returns the existing Order.
	//
	// # Policy by reason
	//
	//   - CANCELLATION: allowed while the Order is Paid and its Event is
	//     CANCELLED. Refunds the fan (face + system/発券 fee; processor fee
	//     retained).
	//   - DISPUTE: always allowed (platform absorbs via negative-balance
	//     responsibility); funds already paid out are clawed back via
	//     transfer_reversal.
	//
	// # Atomicity
	//
	// Stripe money movements (CreateRefund, ReversePayout) are performed
	// before the DB commit. Both are idempotent, so a crash between Stripe and
	// the DB commit is recovered safely: on retry the Stripe calls replay via
	// idempotency keys and the DB commit is reattempted.
	//
	// # Possible errors
	//
	//  - NotFound: the Order does not exist.
	//  - FailedPrecondition: the Order is not refundable (not Paid), or the
	//    reason is CANCELLATION and the Order's Event is not CANCELLED.
	//  - InvalidArgument: the reason is UNSPECIFIED.
	//  - Internal: database or payment provider failure.
	Refund(ctx context.Context, orderID entity.OrderID, reason RefundReason, now time.Time) (*entity.Order, error)

	// IssueFromCapturedWin creates the Order from ④'s captured winning payment,
	// issues the N account-bound covered tickets for the won lottery entry
	// (bound to the user's IdentityVerification when the sale requires verification) and
	// records a Held Settlement (one split paying the event's Organizer at its
	// platform fee rate).
	//
	// It is IDEMPOTENT: a replayed Won-captured signal (or a retry) creates the
	// Order once and issues tickets exactly once for the same entry — on a
	// replay it returns the already-created Order unchanged.
	//
	// # Possible errors
	//
	//  - FailedPrecondition: the entry is not Won, or its payment is not
	//    captured — no Order is created.
	//  - NotFound: no entry with the given ID exists, the event's Organizer
	//    cannot be resolved, or the sale requires verification and the user
	//    has no IdentityVerification — no Order is created.
	//  - Internal: persistence failure after capture.
	IssueFromCapturedWin(ctx context.Context, lotteryEntryID entity.LotteryEntryID) (*entity.Order, error)

	// IssueDueWins issues Orders + tickets for every Won lottery entry that
	// does not yet have an Order. A per-entry failure is logged and skipped so
	// one bad entry never blocks the rest of the sweep.
	//
	// # Possible errors
	//
	//  - Internal: the work-list query failed.
	IssueDueWins(ctx context.Context) error

	// IssueFromCheckout finishes a checkout: while its hold lasts it commits
	// the held tickets, charges the card once and issues the Order and its
	// tickets, bound to the user's IdentityVerification when the sale requires verification.
	// caller is the fan placing the order, or nil for the stalled-checkout job
	// and the payment webhook. Calls for the same Checkout run one after the
	// other, and a Checkout that already has an Order returns it.
	//
	// # Possible errors
	//
	//  - PermissionDenied: the Checkout does not exist or is another fan's
	//    (non-revealing; only with a caller).
	//  - FailedPrecondition: the hold lapsed, the concert is not published, the
	//    card was not authenticated, the commit found the Checkout not
	//    holding, or the card could no longer be charged (the commit is given
	//    back). Nothing is charged.
	//  - NotFound: the sale requires verification and the user has no IdentityVerification.
	//    Nothing is charged.
	//  - Unavailable: the charge's outcome is not known yet, or issuance failed
	//    after the charge; the stalled-checkout job finishes it.
	IssueFromCheckout(ctx context.Context, checkoutID entity.CheckoutID, caller *entity.UserID) (*entity.Order, error)

	// IssueDueCheckouts runs IssueFromCheckout for every Committed Checkout
	// committed more than 1 minute ago, and reports each charged one still
	// unissued 10 minutes after its charge as needing an admin.
	//
	// # Possible errors
	//
	//  - The listing's error; nothing is issued then.
	IssueDueCheckouts(ctx context.Context) error

	// FulfillPayment turns a charge the payment provider reports as completed
	// into an issued Order and announces it as ORDER.paid. It finds the charge's source — a Checkout by its
	// card authorization, else a LotteryEntry by its authorization — and runs the
	// idempotent issuance for it, so a repeated report finds the same Order. A
	// charge of no source succeeds without effect.
	//
	// # Possible errors
	//
	//  - The issuance's or the announcement's error, unchanged; the provider
	//    reports the charge again.
	FulfillPayment(ctx context.Context, cardAuthorizationID string) error
}

// orderUseCase implements [OrderUseCase].
type orderUseCase struct {
	issuanceRepo             entity.IssuanceRepository
	orderRepo                entity.OrderRepository
	entryRepo                entity.LotteryEntryRepository
	eventOrganizerRepo       EventOrganizerRepository
	organizerRepo            entity.OrganizerRepository
	identityVerificationRepo entity.IdentityVerificationRepository
	checkoutRepo             entity.CheckoutRepository
	ticketSaleRepo           entity.TicketSaleRepository
	ticketRepo               entity.TicketRepository
	eventState               EventPublishStatePort
	eventRepo                entity.EventRepository
	cardAuthorizations       entity.CardAuthorizationRepository
	publisher                EventPublisher
	clock                    Clock
	refundRepo               entity.RefundRepository
	settlementRepo           entity.SettlementRepository
	logger                   *logging.Logger
}

// Compile-time interface compliance check.
var _ OrderUseCase = (*orderUseCase)(nil)

// OrderDeps are the dependencies of [NewOrderUseCase]. All are required.
type OrderDeps struct {
	IssuanceRepo             entity.IssuanceRepository
	OrderRepo                entity.OrderRepository
	EntryRepo                entity.LotteryEntryRepository
	EventOrganizerRepo       EventOrganizerRepository
	OrganizerRepo            entity.OrganizerRepository
	IdentityVerificationRepo entity.IdentityVerificationRepository
	CheckoutRepo             entity.CheckoutRepository
	TicketSaleRepo           entity.TicketSaleRepository
	// TicketRepo reads an Order's Tickets to refund only those still Issued.
	TicketRepo entity.TicketRepository
	EventState EventPublishStatePort
	// EventRepo reads the Event of an Order a cancellation refund is for.
	EventRepo entity.EventRepository
	// CardAuthorizations is the checkout's card authorization repository; it
	// also reads a lottery entry's captured payment.
	CardAuthorizations entity.CardAuthorizationRepository
	Publisher          EventPublisher
	Clock              Clock
	RefundRepo         entity.RefundRepository
	SettlementRepo     entity.SettlementRepository
	Logger             *logging.Logger
}

// NewOrderUseCase constructs an OrderUseCase with the given dependencies.
func NewOrderUseCase(d OrderDeps) OrderUseCase {
	return &orderUseCase{
		issuanceRepo:             d.IssuanceRepo,
		orderRepo:                d.OrderRepo,
		entryRepo:                d.EntryRepo,
		eventOrganizerRepo:       d.EventOrganizerRepo,
		organizerRepo:            d.OrganizerRepo,
		identityVerificationRepo: d.IdentityVerificationRepo,
		checkoutRepo:             d.CheckoutRepo,
		ticketSaleRepo:           d.TicketSaleRepo,
		ticketRepo:               d.TicketRepo,
		eventState:               d.EventState,
		eventRepo:                d.EventRepo,
		cardAuthorizations:       d.CardAuthorizations,
		publisher:                d.Publisher,
		clock:                    d.Clock,
		refundRepo:               d.RefundRepo,
		settlementRepo:           d.SettlementRepo,
		logger:                   d.Logger,
	}
}

// Get implements [OrderUseCase].
func (uc *orderUseCase) Get(ctx context.Context, userID entity.UserID, orderID entity.OrderID) (*entity.Order, error) {
	order, err := uc.orderRepo.Get(ctx, orderID)
	if err != nil {
		return nil, err // propagates NotFound
	}
	// Ownership check: an Order belonging to another account is reported exactly
	// like a missing one (non-revealing), never disclosed.
	if order.UserID != userID {
		return nil, apperr.New(codes.NotFound, "order not found")
	}
	return order, nil
}

// ListByEvent implements [OrderUseCase].
func (uc *orderUseCase) ListByEvent(ctx context.Context, eventID string) ([]*entity.Order, error) {
	return uc.orderRepo.ListByEvent(ctx, eventID)
}

// GetWithSettlement implements [OrderUseCase].
func (uc *orderUseCase) GetWithSettlement(ctx context.Context, orderID entity.OrderID) (*entity.Order, *entity.Settlement, error) {
	order, err := uc.orderRepo.Get(ctx, orderID)
	if err != nil {
		return nil, nil, err
	}
	settlement, err := uc.settlementRepo.GetByOrderID(ctx, orderID)
	if errors.Is(err, apperr.ErrNotFound) {
		return order, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	return order, settlement, nil
}
