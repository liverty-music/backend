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

// unrefundedListingReportAge is how long a Sold listing may stay unrefunded
// before every run of the refund job reports it to an admin.
const unrefundedListingReportAge = 10 * time.Minute

// ResaleListingAt is a listing with its deadline and its status at the time
// it was read: a Listed listing past its deadline is Expired.
type ResaleListingAt struct {
	// Listing is the stored listing.
	Listing *entity.ResaleListing
	// Deadline is 3 hours before its event's start time.
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

	// RefundSold refunds the user of every Sold listing its refund amount and
	// then announces the refund (ORDER.resale_listing_refunded), again on
	// every run while the sold email is unsent. One listing's failure never stops the
	// others; it is tried again on the next run. A listing left unrefunded
	// for more than 10 minutes after its sale is reported as needing an admin
	// on every run.
	//
	// # Possible errors
	//
	//  - The error of [entity.ResaleListingRepository.ListSoldUnrefunded].
	RefundSold(ctx context.Context) error
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
	Clock      Clock
	Logger     *logging.Logger
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
		if t.ID == ticketID {
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
	deadline, err := uc.deadline(ctx, ticket.EventID)
	if err != nil {
		return nil, err
	}
	if !stock.IsResaleOpenAt(now, deadline) {
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

// RefundSold implements [ResaleListingUseCase].
func (uc *resaleListingUseCase) RefundSold(ctx context.Context) error {
	listings, err := uc.Listings.ListSoldUnrefunded(ctx)
	if err != nil {
		return err
	}
	for _, l := range listings {
		attrs := []slog.Attr{slog.String("resale_listing_id", string(l.ID)), slog.String("order_id", string(l.OrderID))}
		now := uc.Clock()
		if l.SoldTime != nil && now.Sub(*l.SoldTime) > unrefundedListingReportAge {
			uc.Logger.Error(ctx, LogKeyNeedsOperator, errors.New("a resold listing is still not refunded"),
				append(attrs, slog.Time("sold_time", *l.SoldTime))...)
		}
		// ResaleListing.RecordRefund announces the refund, which the sold
		// email is sent from.
		if err := uc.refund(ctx, l, now); err != nil {
			// One failure never stops the others; the next run retries.
			uc.Logger.Warn(ctx, "resale refund: failed to refund listing; skipping", append(attrs, slog.Any("error", err))...)
		}
	}
	return nil
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
	event, err := uc.Events.Get(ctx, eventID)
	if err != nil {
		return time.Time{}, err
	}
	deadline, ok := entity.ResaleDeadlineOf(event)
	if !ok {
		return time.Time{}, apperr.New(codes.FailedPrecondition, "the event has no start time, so it has no resale",
			slog.String("event_id", eventID))
	}
	return deadline, nil
}

// listingAt returns the listing with its deadline and its status at now.
func listingAt(l *entity.ResaleListing, deadline, now time.Time) *ResaleListingAt {
	return &ResaleListingAt{Listing: l, Deadline: deadline, Status: l.StatusAt(now, deadline)}
}
