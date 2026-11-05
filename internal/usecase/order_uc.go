package usecase

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"

	"github.com/liverty-music/backend/internal/entity"
)

// OrderUseCase handles Orders: a fan reads their own Order and places the
// order of their Checkout, and an admin reads one with its Settlement; the
// issue workflow charges a Pending Order (a won lottery entry's, or a
// first-come checkout's after committing it) and makes it Paid with N
// account-bound covered tickets and a Held Settlement for the event's
// Organizer, together with the Order's ORDER.paid announcement, and owns
// issuance idempotency (one Order per source); and the refund policy delegates
// the money movement to the settlement layer.
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
	// Settlement's splits. A Pending or Void Order, never paid, is returned
	// with a nil Settlement, without reading one.
	//
	// # Possible errors
	//
	//  - NotFound: no Order has the id.
	//  - Internal: the Order is Paid or Refunded and has no Settlement.
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
	// tickets, sets Order status=refunded, and reverses any paid-out
	// Transfer(s) of the Order's Settlement and marks it reversed.
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
	//  - Internal: database or payment provider failure, or the Paid Order
	//    has no Settlement (nothing is refunded).
	Refund(ctx context.Context, orderID entity.OrderID, reason RefundReason, now time.Time) (*entity.Order, error)

	// Issue is the issue workflow of one Order's source ([WorkflowIssue],
	// named [IssueWorkflowID] after the Order): a Won lottery entry, or a
	// Checkout whose order [Place] placed. It reads everything it needs first;
	// for a Checkout it then commits the tickets at the time the commit runs.
	// It charges the Order's card authorization once and makes the Pending
	// Order Paid with its account-bound covered tickets (bound to the user's
	// IdentityVerification when the sale requires verification) and a Held
	// Settlement, with Order.Issue, which announces it as paid. A hold that was
	// over at the commit ends with the Order not Paid, and a card
	// authorization that can no longer be charged reverts the win with
	// LotteryEntry.RevertWin or the commit with Checkout.RevertCommit; both are
	// results that say so, not errors. A run again for a Paid Order returns it.
	//
	// # Possible errors
	//
	//  - InvalidArgument: the request names no source or both.
	//  - NotFound: no entry or Checkout has the id, the event's Organizer
	//    cannot be resolved, or the sale requires verification and the user
	//    has no IdentityVerification. Nothing is committed or charged.
	//  - FailedPrecondition: the entry is not Won or its Order is not Pending.
	//    Nothing is charged.
	//  - Errors of the reads, of the charge or of the issuance, unchanged;
	//    Unavailable is tried again until the workflow's deadline.
	Issue(ctx context.Context, f Flow, in IssueRequest) (IssueResult, error)

	// ReleaseCardAuthorization is the release workflow of one Void Order
	// ([WorkflowReleaseCardAuthorization], named [ReleaseWorkflowID]): it
	// gives back the Order's card authorization without charging. A Void
	// Order without a card authorization succeeds without effect.
	//
	// # Possible errors
	//
	//  - FailedPrecondition: the Order is not Void, or its card authorization
	//    was charged. Nothing is released.
	//  - NotFound: no Order has the id.
	//  - Errors of the release, unchanged; Unavailable is tried again until
	//    the workflow's deadline.
	ReleaseCardAuthorization(ctx context.Context, f Flow, orderID entity.OrderID) (struct{}, error)

	// StartDrawnEntryWorkflow starts the workflow that follows one drawn
	// entry, as the consumer of its announcement: Issue for a Won entry's
	// Order and ReleaseCardAuthorization for a Lost entry's Order. A repeated
	// announcement starts nothing new.
	//
	// # Possible errors
	//
	//  - Errors of starting the workflow, unchanged.
	StartDrawnEntryWorkflow(ctx context.Context, entry *entity.LotteryEntry) error

	// StartResaleEntryWorkflow starts the workflow that follows a resale
	// entry's change, as the consumer of its announcement: Issue for a Won
	// entry's Order and ReleaseCardAuthorization for the Order of a Lapsed,
	// Withdrawn or Lost entry, except a Lost entry whose payment was not
	// completed before the show, which Issue released already. A repeated
	// announcement starts nothing new.
	//
	// # Possible errors
	//
	//  - Errors of starting the workflow, unchanged.
	StartResaleEntryWorkflow(ctx context.Context, entry *entity.ResaleEntry) error

	// Place places the calling fan's order for their Checkout at now. It
	// checks first what the fan can still fix: while the Checkout is Held, that
	// it is holding, that the concert is published, that the Order's card
	// authorization is Authorized for its amount, and that the fan has an
	// IdentityVerification when the sale requires verification; each refusal
	// leaves the Checkout Held and its Order Pending. It then runs [Issue] for
	// the Checkout ([WorkflowIssue], named [IssueWorkflowID] after its Order,
	// on [CheckoutQueue]) and returns the Paid Order. Every Place for the same
	// Checkout shares that one run. An Order already Paid is returned as it is.
	//
	// # Possible errors
	//
	//  - PermissionDenied: the Checkout does not exist or is another fan's
	//    (non-revealing).
	//  - FailedPrecondition: the Checkout Expired or was Released, the hold
	//    lapsed, the concert is not published, the card was not authenticated,
	//    or Issue ended without a Paid Order (the hold was over at the commit,
	//    or the card could no longer be charged).
	//  - InvalidArgument: the card authorization's amount differs from the
	//    Order's.
	//  - NotFound: the sale requires verification and the user has no
	//    IdentityVerification.
	//  - Unavailable: Issue has not ended 30 seconds after it started; it
	//    goes on.
	//  - Errors of the reads and of starting or awaiting Issue, unchanged.
	Place(ctx context.Context, checkoutID entity.CheckoutID, userID entity.UserID, now time.Time) (*entity.Order, error)
}

// orderUseCase implements [OrderUseCase].
type orderUseCase struct {
	issuanceRepo             entity.IssuanceRepository
	orderRepo                entity.OrderRepository
	entryRepo                entity.LotteryEntryRepository
	resaleEntryRepo          entity.ResaleEntryRepository
	eventOrganizerRepo       EventOrganizerRepository
	organizerRepo            entity.OrganizerRepository
	identityVerificationRepo entity.IdentityVerificationRepository
	checkoutRepo             entity.CheckoutRepository
	ticketSaleRepo           entity.TicketSaleRepository
	stockRepo                entity.TicketStockRepository
	ticketRepo               entity.TicketRepository
	eventState               EventPublishStatePort
	eventRepo                entity.EventRepository
	cardAuthorizations       entity.CardAuthorizationRepository
	clock                    Clock
	refundRepo               entity.RefundRepository
	settlementRepo           entity.SettlementRepository
	workflows                Workflows
	logger                   *logging.Logger
}

// Compile-time interface compliance check.
var _ OrderUseCase = (*orderUseCase)(nil)

// OrderDeps are the dependencies of [NewOrderUseCase]. All are required.
type OrderDeps struct {
	IssuanceRepo entity.IssuanceRepository
	OrderRepo    entity.OrderRepository
	EntryRepo    entity.LotteryEntryRepository
	// ResaleEntryRepo reads and reverts a Won resale entry.
	ResaleEntryRepo          entity.ResaleEntryRepository
	EventOrganizerRepo       EventOrganizerRepository
	OrganizerRepo            entity.OrganizerRepository
	IdentityVerificationRepo entity.IdentityVerificationRepository
	CheckoutRepo             entity.CheckoutRepository
	TicketSaleRepo           entity.TicketSaleRepository
	// StockRepo reads a Won entry's TicketStock for its event.
	StockRepo entity.TicketStockRepository
	// TicketRepo reads an Order's Tickets to refund only those still Issued.
	TicketRepo entity.TicketRepository
	EventState EventPublishStatePort
	// EventRepo reads the Event of an Order a cancellation refund is for.
	EventRepo entity.EventRepository
	// CardAuthorizations reads, captures and releases the card authorization
	// of an Order of either source.
	CardAuthorizations entity.CardAuthorizationRepository
	Clock              Clock
	RefundRepo         entity.RefundRepository
	SettlementRepo     entity.SettlementRepository
	// Workflows starts the workflows that follow a drawn entry and the issue
	// of a placed Checkout; a process that never starts them may leave it nil.
	Workflows Workflows
	Logger    *logging.Logger
}

// NewOrderUseCase constructs an OrderUseCase with the given dependencies.
func NewOrderUseCase(d OrderDeps) OrderUseCase {
	return &orderUseCase{
		issuanceRepo:             d.IssuanceRepo,
		orderRepo:                d.OrderRepo,
		entryRepo:                d.EntryRepo,
		resaleEntryRepo:          d.ResaleEntryRepo,
		eventOrganizerRepo:       d.EventOrganizerRepo,
		organizerRepo:            d.OrganizerRepo,
		identityVerificationRepo: d.IdentityVerificationRepo,
		checkoutRepo:             d.CheckoutRepo,
		ticketSaleRepo:           d.TicketSaleRepo,
		stockRepo:                d.StockRepo,
		ticketRepo:               d.TicketRepo,
		eventState:               d.EventState,
		eventRepo:                d.EventRepo,
		cardAuthorizations:       d.CardAuthorizations,
		clock:                    d.Clock,
		refundRepo:               d.RefundRepo,
		settlementRepo:           d.SettlementRepo,
		workflows:                d.Workflows,
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
	if !isPaid(order) {
		// A Pending or Void Order was never paid and has no Settlement.
		return order, nil, nil
	}
	settlement, err := uc.settlementRepo.GetByOrderID(ctx, orderID)
	if errors.Is(err, apperr.ErrNotFound) {
		// Order.Issue stores the Settlement with the Paid Order.
		return nil, nil, apperr.Wrap(err, codes.Internal, "the paid Order has no Settlement",
			slog.String("order_id", string(orderID)))
	}
	if err != nil {
		return nil, nil, err
	}
	return order, settlement, nil
}
