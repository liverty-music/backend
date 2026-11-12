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

// LogKeyNeedsOperator is the log message of a purchase that needs an admin:
// a resold listing is still not refunded. A log-based alert on it pages the admin; the alert in
// cloud-provisioning (monitoring.ts) matches this text, so the constant and
// its text keep their names.
const LogKeyNeedsOperator = "checkout needs an operator"

// ResaleListingAt is a listing with its deadline and its status at the time
// it was read: a Listed listing past its deadline is Expired.
type ResaleListingAt struct {
	// Listing is the stored listing.
	Listing *entity.ResaleListing
	// Deadline is its event's resale deadline ([entity.ResaleDeadlineOf]).
	Deadline time.Time
	// Status is the listing's status at the time it was read.
	Status entity.ResaleListingStatus
}

// ResaleListingUseCase lets a signed-in fan offer their tickets for official
// resale (公式リセール), withdraw them and read them, plus the job that refunds
// and notifies the fans whose tickets sold.
type ResaleListingUseCase interface {
	// Create lists one of the fan's tickets, bought first come or won in a
	// lottery, while its TicketStock's resale is open, and returns the Listed
	// listing.
	//
	// # Possible errors
	//
	//  - PermissionDenied: the Ticket is not among the fan's (non-revealing).
	//  - FailedPrecondition: the Ticket is Voided or admitted, its concert is
	//    not published, or its TicketStock's resale is not open.
	//  - AlreadyExists: the Ticket already has a Listed listing.
	Create(ctx context.Context, userID entity.UserID, ticketID entity.TicketID) (*ResaleListingAt, error)

	// Withdraw takes back the fan's open listing and returns it as read again.
	//
	// # Possible errors
	//
	//  - PermissionDenied: the listing does not exist or is another fan's
	//    (non-revealing).
	//  - FailedPrecondition: the listing is Sold, Cancelled or expired.
	Withdraw(ctx context.Context, userID entity.UserID, id entity.ResaleListingID) (*ResaleListingAt, error)

	// List returns the fan's listings, newest first, each with its deadline;
	// an empty slice when there are none.
	List(ctx context.Context, userID entity.UserID) ([]*ResaleListingAt, error)

	// StartRefundSold starts the refund workflow of a listing Order.Issue made
	// Sold ([WorkflowRefundSold]), named [RefundSoldWorkflowID] so a repeated
	// announcement starts nothing new, with a 24-hour deadline.
	//
	// # Possible errors
	//
	//  - Errors of starting the workflow, unchanged.
	StartRefundSold(ctx context.Context, listingID entity.ResaleListingID) error

	// RefundSold is the refund workflow of one Sold listing: it refunds the
	// listing's user its refund amount of its Order's charge, for the purpose
	// of this listing, and records the refund, which announces it
	// (RESALE_LISTING.refunded). A listing already refunded is left as
	// it is. A step that fails with Unavailable is tried again until the
	// workflow's deadline; a failure ends it and an admin is told.
	//
	// # Possible errors
	//
	//  - FailedPrecondition: the listing is not Sold.
	//  - Errors of reading the listing or its Order, of the refund or of
	//    recording it, unchanged.
	RefundSold(ctx context.Context, f Flow, listingID entity.ResaleListingID) (struct{}, error)
}

// WorkflowRefundSold is the stable name of the workflow that refunds the user
// of one Sold listing.
const WorkflowRefundSold = "refund-sold"

// RefundSoldWorkflowID names the refund of a listing: one per listing.
func RefundSoldWorkflowID(listingID entity.ResaleListingID) string {
	return "refund:" + string(listingID)
}

// ResaleListingDeps are the dependencies of [NewResaleListingUseCase]. All are
// required.
type ResaleListingDeps struct {
	Listings   entity.ResaleListingRepository
	Tickets    entity.TicketRepository
	Orders     entity.OrderRepository
	Stocks     entity.TicketStockRepository
	EventState EventPublishStatePort
	Events     entity.EventRepository
	// Workflows starts the refund workflow of a Sold listing; a process that
	// never starts it may leave it nil.
	Workflows Workflows
	Clock     Clock
	Logger    *logging.Logger
}

// resaleListingUseCase implements [ResaleListingUseCase].
type resaleListingUseCase struct {
	ResaleListingDeps
}

// Compile-time interface compliance check.
var _ ResaleListingUseCase = (*resaleListingUseCase)(nil)

// NewResaleListingUseCase constructs a ResaleListingUseCase.
func NewResaleListingUseCase(d ResaleListingDeps) ResaleListingUseCase {
	return &resaleListingUseCase{d}
}

// Create implements [ResaleListingUseCase].
func (uc *resaleListingUseCase) Create(ctx context.Context, userID entity.UserID, ticketID entity.TicketID) (*ResaleListingAt, error) {
	now := uc.Clock()
	tickets, err := uc.Tickets.ListByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	var ticket *entity.Ticket
	for _, t := range tickets {
		if t.ID == ticketID && t.BelongsTo(userID) {
			ticket = t
			break
		}
	}
	if ticket == nil {
		return nil, apperr.New(codes.PermissionDenied, "the ticket is not the fan's")
	}
	if !ticket.IsAdmissible() {
		return nil, apperr.New(codes.FailedPrecondition, "the ticket is voided or was admitted")
	}
	order, err := uc.Orders.Get(ctx, ticket.OrderID)
	if err != nil {
		return nil, err
	}
	// A ticket bought first come and a ticket won in a lottery are listed
	// alike: both are of the Order's TicketStock.
	stock, err := uc.Stocks.Get(ctx, order.TicketStockID, now)
	if err != nil {
		return nil, err
	}
	published, err := uc.EventState.IsEventPublished(ctx, ticket.EventID)
	if err != nil {
		return nil, err
	}
	if !published {
		return nil, apperr.New(codes.FailedPrecondition, "the concert is not published")
	}
	event, deadline, err := uc.eventAndDeadline(ctx, ticket.EventID)
	if err != nil {
		return nil, err
	}
	if !stock.IsResaleOpenAt(now, event) {
		return nil, apperr.New(codes.FailedPrecondition, "the ticket stock's resale is not open")
	}

	listing, err := uc.Listings.Create(ctx, entity.NewResaleListing(ticket, stock.ID, stock.SeatType, now))
	if err != nil {
		return nil, err
	}
	return listingAt(listing, deadline, now), nil
}

// Withdraw implements [ResaleListingUseCase].
func (uc *resaleListingUseCase) Withdraw(ctx context.Context, userID entity.UserID, id entity.ResaleListingID) (*ResaleListingAt, error) {
	now := uc.Clock()
	listing, err := uc.Listings.Get(ctx, id)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return nil, apperr.New(codes.PermissionDenied, "the listing is not the fan's")
		}
		return nil, err
	}
	if listing.UserID != userID {
		return nil, apperr.New(codes.PermissionDenied, "the listing is not the fan's")
	}
	if err := uc.Listings.Withdraw(ctx, id, now); err != nil {
		return nil, err
	}
	listing, err = uc.Listings.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	deadline, err := uc.deadline(ctx, listing.EventID)
	if err != nil {
		return nil, err
	}
	return listingAt(listing, deadline, now), nil
}

// List implements [ResaleListingUseCase].
func (uc *resaleListingUseCase) List(ctx context.Context, userID entity.UserID) ([]*ResaleListingAt, error) {
	now := uc.Clock()
	listings, err := uc.Listings.ListByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	deadlines := map[string]time.Time{}
	out := make([]*ResaleListingAt, 0, len(listings))
	for _, l := range listings {
		deadline, ok := deadlines[l.EventID]
		if !ok {
			deadline, err = uc.deadline(ctx, l.EventID)
			if err != nil {
				return nil, err
			}
			deadlines[l.EventID] = deadline
		}
		out = append(out, listingAt(l, deadline, now))
	}
	return out, nil
}

// StartRefundSold implements [ResaleListingUseCase].
func (uc *resaleListingUseCase) StartRefundSold(ctx context.Context, listingID entity.ResaleListingID) error {
	return uc.Workflows.Start(ctx, WorkflowRefundSold, RefundSoldWorkflowID(listingID), listingID,
		WithQueue(PaymentQueue), WithDeadline(uc.Clock().Add(paymentWorkflowDeadline)))
}

// RefundSold implements [ResaleListingUseCase].
func (uc *resaleListingUseCase) RefundSold(ctx context.Context, f Flow, listingID entity.ResaleListingID) (struct{}, error) {
	return Step(ctx, f, "refund", func(ctx context.Context) (struct{}, error) {
		l, err := uc.Listings.Get(ctx, listingID)
		if err != nil {
			return struct{}{}, err
		}
		needs, err := l.NeedsRefund()
		if err != nil || !needs {
			return struct{}{}, err
		}
		// ResaleListing.RecordRefund announces the refund, which the sold
		// email is sent from.
		return struct{}{}, uc.refund(ctx, l, uc.Clock())
	})
}

// refund refunds the listing's refund amount of its Order's charge, for the
// purpose of this listing, and records it.
func (uc *resaleListingUseCase) refund(ctx context.Context, l *entity.ResaleListing, now time.Time) error {
	order, err := uc.Orders.Get(ctx, l.OrderID)
	if err != nil {
		return err
	}
	chargeID, err := uc.Orders.GetProviderChargeID(ctx, order.Payment.ProviderPaymentID)
	if err != nil {
		return err
	}
	refundID, err := uc.Orders.CreateRefund(ctx, entity.RefundParams{
		OrderID:          order.ID,
		ResaleListingID:  l.ID,
		ProviderChargeID: chargeID,
		Amount:           l.RefundAmount(),
	})
	if err != nil {
		return err
	}
	if err := uc.Listings.RecordRefund(ctx, l.ID, refundID, now); err != nil {
		return err
	}
	l.RefundTime = &now
	return nil
}

// deadline reads the event and returns its resale deadline. An event without
// a start time has none: FailedPrecondition.
func (uc *resaleListingUseCase) deadline(ctx context.Context, eventID string) (time.Time, error) {
	_, deadline, err := uc.eventAndDeadline(ctx, eventID)
	return deadline, err
}

// eventAndDeadline reads the event and returns it with its resale deadline.
// An event without a start time has none: FailedPrecondition.
func (uc *resaleListingUseCase) eventAndDeadline(ctx context.Context, eventID string) (*entity.Event, time.Time, error) {
	event, err := uc.Events.Get(ctx, eventID)
	if err != nil {
		return nil, time.Time{}, err
	}
	deadline, ok := entity.ResaleDeadlineOf(event)
	if !ok {
		return nil, time.Time{}, apperr.New(codes.FailedPrecondition, "the event has no start time, so it has no resale",
			slog.String("event_id", eventID))
	}
	return event, deadline, nil
}

// listingAt returns the listing with its deadline and its status at now.
func listingAt(l *entity.ResaleListing, deadline, now time.Time) *ResaleListingAt {
	return &ResaleListingAt{Listing: l, Deadline: deadline, Status: l.StatusAt(now, deadline)}
}
