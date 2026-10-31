package entity

import (
	"context"
	"errors"
	"time"
)

// CheckoutID is the opaque identifier for a single Checkout. Server-generated
// (UUIDv7).
//
// Mirrors liverty_music.entity.v1.CheckoutId.value.
type CheckoutID string

// CheckoutStatus is the lifecycle of a Checkout. Values mirror the proto
// enum liverty_music.entity.v1.CheckoutStatus.
//
//	Held --> Committed --> Completed
//	Held --> Expired | Released
//	Committed --> Released   (only when the card was never charged)
type CheckoutStatus int16

const (
	// CheckoutStatusUnspecified is the zero value and is never persisted.
	CheckoutStatusUnspecified CheckoutStatus = 0
	// CheckoutStatusHeld holds the tickets until the hold expiry.
	CheckoutStatusHeld CheckoutStatus = 1
	// CheckoutStatusCommitted means the tickets were committed against the
	// stock while the hold lasted; the card is charged and the Order issued next.
	CheckoutStatusCommitted CheckoutStatus = 2
	// CheckoutStatusCompleted means the card was charged and the Order issued.
	CheckoutStatusCompleted CheckoutStatus = 3
	// CheckoutStatusExpired means the hold lapsed before a commit.
	CheckoutStatusExpired CheckoutStatus = 4
	// CheckoutStatusReleased means the checkout ended without a charge: it
	// was replaced by a newer one, or its commit was given back.
	CheckoutStatusReleased CheckoutStatus = 5
)

// String returns the lowercase status name.
func (s CheckoutStatus) String() string {
	switch s {
	case CheckoutStatusHeld:
		return "held"
	case CheckoutStatusCommitted:
		return "committed"
	case CheckoutStatusCompleted:
		return "completed"
	case CheckoutStatusExpired:
		return "expired"
	case CheckoutStatusReleased:
		return "released"
	default:
		return "UNSPECIFIED"
	}
}

// CheckoutHoldDuration is how long a new Checkout holds its tickets. It
// is never extended.
const CheckoutHoldDuration = 15 * time.Minute

// CheckoutStalledCommitAge is how long a Committed Checkout may stay
// uncompleted before the stalled-checkout job finishes it.
const CheckoutStalledCommitAge = time.Minute

// Checkout is how one fan's Order is bought first come, first served: during
// a FirstCome TicketSale it holds the Order's tickets of its TicketStock for
// 15 minutes while the fan confirms their 本人確認 (identity check) details and
// authorizes their card. It ends with the tickets committed, charged and
// issued, or with the hold released, any card authorization given back and
// the Order Void. The fan, the TicketStock, the ticket count, the amount, the
// card authorization and the referral code are the Order's. The fan's full
// name and phone number are the User's.
//
// Mirrors liverty_music.entity.v1.Checkout.
type Checkout struct {
	// ID is the surrogate primary key (UUIDv7).
	ID CheckoutID
	// OrderID is the Order the checkout buys; one Checkout per Order.
	OrderID OrderID
	// TicketSaleID is the FirstCome TicketSale the checkout buys from; it
	// names the event of the Order's TicketStock.
	TicketSaleID TicketSaleID
	// CardAuthorizationReleaseTime is when the Order's card authorization was
	// given back without a charge; nil unless released. Set once.
	CardAuthorizationReleaseTime *time.Time
	// Status is the checkout's lifecycle status.
	Status CheckoutStatus
	// HoldExpireTime is 15 minutes after the checkout started; never extended.
	HoldExpireTime time.Time
	// CommitTime is when the tickets were committed; nil until committed and
	// kept when a commit is given back.
	CommitTime *time.Time
	// CaptureTime is when the Order's card authorization was charged; nil
	// until charged. Set once.
	CaptureTime *time.Time

	// Order is the Order the checkout buys, read with every Checkout.
	Order *Order
}

// NewCheckout returns a Held Checkout of the sale that buys order, with a
// generated UUIDv7 id and a hold expiry 15 minutes after now, and names it as
// order's source.
func NewCheckout(order *Order, saleID TicketSaleID, now time.Time) *Checkout {
	c := &Checkout{
		ID:             CheckoutID(NewID()),
		OrderID:        order.ID,
		TicketSaleID:   saleID,
		Status:         CheckoutStatusHeld,
		HoldExpireTime: now.Add(CheckoutHoldDuration),
		Order:          order,
	}
	order.CheckoutID = c.ID
	return c
}

// ValidateCheckoutSale reports whether a Checkout can belong to sale for stock: a
// FirstCome sale that names the stock's event. The entity layer returns
// stdlib errors; callers wrap them with an apperr code.
func ValidateCheckoutSale(sale *TicketSale, stock *TicketStock) error {
	if sale == nil {
		return errors.New("a checkout belongs to a ticket sale")
	}
	if sale.Method != TicketSaleMethodFirstCome {
		return errors.New("a checkout belongs to a first-come sale")
	}
	if !sale.NamesEvent(stock.EventID) {
		return errors.New("a checkout's sale must name the event of its ticket stock")
	}
	return nil
}

// IsHoldingAt reports whether the Checkout is Held and t is before its hold
// expiry. Only holding Checkouts count in a TicketStock's held count, and only
// a holding Checkout can be committed.
func (r *Checkout) IsHoldingAt(t time.Time) bool {
	return r.Status == CheckoutStatusHeld && t.Before(r.HoldExpireTime)
}

// StatusAt returns the Checkout's status as of t: a Held Checkout whose
// hold has lapsed is Expired, since it can never be committed, even before the
// sweeper stores that; any other status is returned as stored.
func (r *Checkout) StatusAt(t time.Time) CheckoutStatus {
	if r.Status == CheckoutStatusHeld && !r.IsHoldingAt(t) {
		return CheckoutStatusExpired
	}
	return r.Status
}

// IsCharged reports whether the Checkout's card was charged.
func (r *Checkout) IsCharged() bool {
	return r.CaptureTime != nil
}

// CanBecome reports whether the Checkout may move to status next. A
// charged Checkout can only become Completed: it is never Released or
// Expired.
func (r *Checkout) CanBecome(next CheckoutStatus) bool {
	switch r.Status {
	case CheckoutStatusHeld:
		return next == CheckoutStatusCommitted || next == CheckoutStatusExpired || next == CheckoutStatusReleased
	case CheckoutStatusCommitted:
		if r.IsCharged() {
			return next == CheckoutStatusCompleted
		}
		return next == CheckoutStatusCompleted || next == CheckoutStatusReleased
	default:
		return false
	}
}

// CommitOutcome is what [CheckoutRepository.Commit] reports.
type CommitOutcome int

const (
	// CommitOutcomeCommitted means the Checkout is Committed (now or before)
	// or Completed.
	CommitOutcomeCommitted CommitOutcome = 1
	// CommitOutcomeNotHeld means the Checkout was not holding: its hold
	// lapsed, or it is Expired or Released. Nothing changed.
	CommitOutcomeNotHeld CommitOutcome = 2
)

// CheckoutRepository persists Checkouts together with their Orders. Every read
// returns the Checkout with its Order. Implementations live in
// internal/infrastructure/database/rdb/.
//
// Interfaces are defined where consumed (AGENTS.md rule).
type CheckoutRepository interface {
	// GetOrCreateHeld returns the user's holding Checkout of the sale for the
	// stock when its Order's count equals count, changing nothing; otherwise
	// it creates a new Held Checkout of the sale with its Pending Order for
	// the stock, the user, the count and the referral code. It creates one
	// only when the stock's remaining count at now, counting the user's own
	// holding Checkout for the stock as free, is at least count, and when the
	// application fits the stock's event for the user
	// ([Event.Fits], with [OrderRepository.GetHeldForEvent] leaving out the
	// Order of the user's holding Checkout that is replaced). On creation, the
	// user's holding Checkout for the stock becomes Released, a lapsed Held one
	// Expired, and the Order of each Void. It locks the user's users row, then
	// the stock's row, so it is serialized with every other Order creation by
	// the user and with every change of the stock. On failure nothing changes.
	//
	// # Possible errors
	//
	//  - ResourceExhausted: not enough tickets remain.
	//  - FailedPrecondition: the application does not fit the event.
	//  - NotFound: no TicketStock has the id, or the user does not exist, also
	//    when it was deleted while the call waited.
	//  - Internal: database failure.
	GetOrCreateHeld(ctx context.Context, stockID TicketStockID, saleID TicketSaleID, userID UserID, count int, referralCode *ReferralCode, now time.Time) (*Checkout, error)

	// Get returns the Checkout with the given id, whatever its status.
	//
	// # Possible errors
	//
	//  - NotFound: no Checkout has the id.
	//  - Internal: database failure.
	Get(ctx context.Context, id CheckoutID) (*Checkout, error)

	// GetByCardAuthorizationID returns the Checkout whose Order's card
	// authorization has the given id.
	//
	// # Possible errors
	//
	//  - NotFound: there is none.
	//  - Internal: database failure.
	GetByCardAuthorizationID(ctx context.Context, cardAuthorizationID string) (*Checkout, error)

	// ListByUser returns every Checkout whose Order's user is the user, in
	// every status and for every TicketStock, newest first. It returns an
	// empty slice when there is none.
	//
	// # Possible errors
	//
	//  - Internal: database failure.
	ListByUser(ctx context.Context, userID UserID) ([]*Checkout, error)

	// SetCardAuthorization stores the card authorization as the card
	// authorization of the Checkout's Order while the Checkout is Held. With
	// the same card authorization already set it changes nothing and succeeds.
	//
	// # Possible errors
	//
	//  - FailedPrecondition: the Order has another card authorization or the
	//    Checkout is not Held.
	//  - NotFound: no Checkout has the id.
	//  - Internal: database failure.
	SetCardAuthorization(ctx context.Context, id CheckoutID, cardAuthorizationID string) error

	// Commit makes a holding Checkout Committed at now under its stock's row
	// lock; its Order's ticket count then counts in the stock's sold count. It
	// is one indivisible step that succeeds only while the Checkout is Held
	// and before its hold expiry. A Committed or Completed Checkout reports
	// Committed without a change; any other reports NotHeld.
	//
	// # Possible errors
	//
	//  - NotFound: no Checkout has the id.
	//  - Internal: database failure.
	Commit(ctx context.Context, id CheckoutID, now time.Time) (CommitOutcome, error)

	// Release makes a Held Checkout whose hold expired at or before now
	// Expired and its Order Void, in one indivisible step, and reports whether
	// it changed it. Any other Checkout is left unchanged.
	//
	// # Possible errors
	//
	//  - NotFound: no Checkout has the id.
	//  - Internal: database failure.
	//
	// # Announces
	//
	// ORDER.checkout_expired with the Checkout as Expired, together with the
	// release. Nothing when it changed nothing.
	Release(ctx context.Context, id CheckoutID, now time.Time) (bool, error)

	// RevertCommit makes a Committed, uncharged Checkout Released and its
	// Order Void under its stock's row lock, in one indivisible step, keeping
	// its commit time. A Checkout that is not Committed is left unchanged.
	//
	// # Possible errors
	//
	//  - FailedPrecondition: the Checkout has a capture time.
	//  - NotFound: no Checkout has the id.
	//  - Internal: database failure.
	RevertCommit(ctx context.Context, id CheckoutID) error

	// RecordCapture stores the capture time of a Committed Checkout; the
	// charged payment is its Order's card authorization. It changes nothing
	// when a capture time is set.
	//
	// # Possible errors
	//
	//  - FailedPrecondition: the Checkout is not Committed.
	//  - NotFound: no Checkout has the id.
	//  - Internal: database failure.
	RecordCapture(ctx context.Context, id CheckoutID, at time.Time) error

	// RecordCardAuthorizationRelease sets the card authorization release time
	// of an Expired or Released Checkout whose Order has a card authorization.
	// It changes nothing when the time is already set.
	//
	// # Possible errors
	//
	//  - FailedPrecondition: the Checkout is in another status or its Order
	//    has no card authorization.
	//  - NotFound: no Checkout has the id.
	//  - Internal: database failure.
	RecordCardAuthorizationRelease(ctx context.Context, id CheckoutID, at time.Time) error

	// ListDue returns, oldest hold first, the Held Checkouts whose hold expired
	// at or before now, the Expired and Released Checkouts whose Order has a
	// card authorization and that have no card authorization release time,
	// and the Committed Checkouts committed more than 1 minute before now.
	//
	// # Possible errors
	//
	//  - Internal: database failure.
	ListDue(ctx context.Context, now time.Time) ([]*Checkout, error)

	// Serialize runs fn while holding an exclusive lock keyed on the
	// Checkout, so calls for the same Checkout run one after the other
	// and a second caller waits for the first. The lock is released when fn
	// returns, and also when the process holding it dies.
	//
	// # Possible errors
	//
	//  - Internal: the lock could not be taken.
	//  - Any error returned by fn, unchanged.
	Serialize(ctx context.Context, id CheckoutID, fn func(ctx context.Context) error) error
}
