package entity

import (
	"context"
	"time"
)

// ResaleListingID is the opaque identifier for a single ResaleListing.
// Server-generated (UUIDv7).
//
// Mirrors liverty_music.entity.v1.ResaleListingId.value.
type ResaleListingID string

// ResaleListingStatus is the lifecycle of a ResaleListing. Values mirror the
// proto enum liverty_music.entity.v1.ResaleListingStatus.
//
//	Listed --> Sold | Withdrawn | Cancelled
//	Listed --> Expired   (derived when read, never stored)
type ResaleListingStatus int16

const (
	// ResaleListingStatusUnspecified is the zero value and is never persisted.
	ResaleListingStatusUnspecified ResaleListingStatus = 0
	// ResaleListingStatusListed is offered for resale.
	ResaleListingStatusListed ResaleListingStatus = 1
	// ResaleListingStatusSold means another fan's Order bought it.
	ResaleListingStatusSold ResaleListingStatus = 2
	// ResaleListingStatusWithdrawn means the fan withdrew it.
	ResaleListingStatusWithdrawn ResaleListingStatus = 3
	// ResaleListingStatusCancelled means it ended because its Ticket's Order
	// was refunded.
	ResaleListingStatusCancelled ResaleListingStatus = 4
	// ResaleListingStatusExpired is a Listed listing at or after its deadline.
	// It is derived by [ResaleListing.StatusAt] and never stored.
	ResaleListingStatusExpired ResaleListingStatus = 5
)

// ResaleFeePercent is the resale fee as a percentage of a listing's price. It
// is stated in the Terms of Service and on the listing screen, so changing it
// is a spec change.
const ResaleFeePercent = 10

// ResaleDeadlineLead is how long before its event's start time a listing's
// deadline falls. It equals the admission window's lead, so resale closes the
// moment admission opens.
const ResaleDeadlineLead = admissionWindowLead

// ResaleDeadlineOf returns the resale deadline of the event: 3 hours before
// its current start time, which is when its admission window opens. ok is
// false when the event has no start time.
func ResaleDeadlineOf(event *Event) (deadline time.Time, ok bool) {
	if event == nil || event.StartTime == nil {
		return time.Time{}, false
	}
	return event.StartTime.Add(-ResaleDeadlineLead), true
}

// ResaleFee returns the resale fee of a listing priced price in yen: 10% of
// the price, rounded down to the yen.
func ResaleFee(price int64) int64 {
	return price * ResaleFeePercent / 100
}

// ResaleListing is a fan's offer of one of their Tickets for official resale
// (公式リセール) at face value, after the Ticket's TicketStock sold out,
// whether the Ticket was bought first come or won in a lottery. When another
// fan buys it, the listing's Ticket is Voided, the other fan receives a new
// Ticket, and the listing's user is refunded the price minus the resale fee.
// Until buying listed tickets ships (resell-tickets-by-lottery), listings are
// made but not bought.
//
// Mirrors liverty_music.entity.v1.ResaleListing.
type ResaleListing struct {
	// ID is the surrogate primary key (UUIDv7).
	ID ResaleListingID
	// TicketID is the Ticket offered.
	TicketID TicketID
	// OrderID is the Order that issued the Ticket, whose charge the user is
	// refunded from.
	OrderID OrderID
	// UserID is the fan offering it: the Ticket's user.
	UserID UserID
	// EventID is the event the Ticket admits to, whose start time sets the
	// listing's deadline.
	EventID string
	// TicketStockID is the TicketStock the Ticket is of.
	TicketStockID TicketStockID
	// Price is the Ticket's face value (券面価格) in yen, tax-inclusive: the
	// price of the stock's SeatType.
	Price int64
	// Status is the stored lifecycle status; never Expired.
	Status ResaleListingStatus
	// CreateTime is when the fan listed the Ticket.
	CreateTime time.Time
	// SoldTime is when another fan's Order bought it; nil unless Sold.
	SoldTime *time.Time
	// WithdrawTime is when the fan withdrew it; nil unless Withdrawn.
	WithdrawTime *time.Time
	// ProviderRefundID is the payment provider's ID for the refund to the
	// user; empty until refunded, then set once.
	ProviderRefundID string
	// RefundTime is when the user was refunded; nil until refunded, then set
	// once.
	RefundTime *time.Time
	// SoldEmailSendTime is when the email telling the user the Ticket sold
	// was sent; nil until sent, then set once.
	SoldEmailSendTime *time.Time
}

// NewResaleListing returns a Listed ResaleListing with a generated UUIDv7 id
// for the ticket of the stock, priced at the seat type's price, created at
// now.
func NewResaleListing(ticket *Ticket, stockID TicketStockID, seatType *SeatType, now time.Time) *ResaleListing {
	return &ResaleListing{
		ID:            ResaleListingID(NewID()),
		TicketID:      ticket.ID,
		OrderID:       ticket.OrderID,
		UserID:        ticket.UserID,
		EventID:       ticket.EventID,
		TicketStockID: stockID,
		Price:         seatType.Price,
		Status:        ResaleListingStatusListed,
		CreateTime:    now,
	}
}

// Fee returns the listing's resale fee: 10% of its price, rounded down to the
// yen.
func (l *ResaleListing) Fee() int64 {
	return ResaleFee(l.Price)
}

// RefundAmount returns what the listing's user is refunded when it sells: its
// price minus its resale fee.
func (l *ResaleListing) RefundAmount() int64 {
	return l.Price - l.Fee()
}

// IsOpenAt reports whether the listing is Listed and t is before its
// deadline.
func (l *ResaleListing) IsOpenAt(t, deadline time.Time) bool {
	return l.Status == ResaleListingStatusListed && t.Before(deadline)
}

// StatusAt returns the listing's status as of t: a Listed listing at or after
// its deadline is Expired; any other status is returned as stored.
func (l *ResaleListing) StatusAt(t, deadline time.Time) ResaleListingStatus {
	if l.Status == ResaleListingStatusListed && !l.IsOpenAt(t, deadline) {
		return ResaleListingStatusExpired
	}
	return l.Status
}

// IsWithdrawableAt reports whether the fan can withdraw the listing at t: it
// is open at t.
func (l *ResaleListing) IsWithdrawableAt(t, deadline time.Time) bool {
	return l.IsOpenAt(t, deadline)
}

// ResaleListingSoldEmail is the finished "your ticket sold" message for one
// listing, already rendered in the language of the listing's user.
type ResaleListingSoldEmail struct {
	// Subject is the email subject line.
	Subject string
	// TextBody is the plain-text email body.
	TextBody string
}

// ResaleListingRepository persists ResaleListings. Implementations live in
// internal/infrastructure/database/rdb/.
//
// Interfaces are defined where consumed (AGENTS.md rule).
type ResaleListingRepository interface {
	// Create stores the Listed listing and returns it. Checking and storing
	// are one indivisible step: the Ticket must be Issued without an admit
	// time and have no Listed listing at that moment. A Ticket whose earlier
	// listing was Withdrawn, Cancelled or expired can be listed again.
	//
	// # Possible errors
	//
	//  - AlreadyExists: the Ticket already has a Listed listing.
	//  - FailedPrecondition: the Ticket is not Issued or has an admit time.
	//  - NotFound: no Ticket has the id, or the listing's user does not
	//    exist, also when it was deleted while the call waited.
	//  - Internal: database failure.
	Create(ctx context.Context, listing *ResaleListing) (*ResaleListing, error)

	// Get returns the listing with the given id.
	//
	// # Possible errors
	//
	//  - NotFound: no listing has the id.
	//  - Internal: database failure.
	Get(ctx context.Context, id ResaleListingID) (*ResaleListing, error)

	// Withdraw makes a listing that is open at now Withdrawn with that
	// withdraw time; the deadline is read from its Ticket's event. A Withdrawn
	// listing is left unchanged.
	//
	// # Possible errors
	//
	//  - FailedPrecondition: the listing is Sold, Cancelled or expired.
	//  - NotFound: no listing has the id.
	//  - Internal: database failure.
	Withdraw(ctx context.Context, id ResaleListingID, now time.Time) error

	// ListByUser returns every listing of the user, in every status, newest
	// create time first; an empty slice when there are none.
	//
	// # Possible errors
	//
	//  - Internal: database failure.
	ListByUser(ctx context.Context, userID UserID) ([]*ResaleListing, error)

	// ListSoldUnrefunded returns every Sold listing that has no refund time,
	// oldest sold time first; an empty slice when there are none.
	//
	// # Possible errors
	//
	//  - Internal: database failure.
	ListSoldUnrefunded(ctx context.Context) ([]*ResaleListing, error)

	// RecordRefund sets the provider refund ID and refund time of a Sold
	// listing that has no refund time. A listing with a refund time is left
	// unchanged.
	//
	// # Possible errors
	//
	//  - FailedPrecondition: the listing is not Sold.
	//  - NotFound: no listing has the id.
	//  - Internal: database failure.
	RecordRefund(ctx context.Context, id ResaleListingID, providerRefundID string, at time.Time) error
}

// ResaleListingSoldEmailSender sends a listing's "your ticket sold" email at
// most once. The implementation lives in internal/infrastructure/mail/.
//
// Interfaces are defined where consumed (AGENTS.md rule).
type ResaleListingSoldEmailSender interface {
	// SendSoldEmail sends msg to the address when the listing has no sold
	// email send time, then sets that time. When the time is already set it
	// sends nothing and succeeds.
	//
	// # Possible errors
	//
	//  - InvalidArgument: the address or the message is empty.
	//  - Unavailable: email cannot be sent; the send time stays unset.
	//  - NotFound: no listing has the id.
	SendSoldEmail(ctx context.Context, id ResaleListingID, to string, msg ResaleListingSoldEmail) error
}
