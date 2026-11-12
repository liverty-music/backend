package entity

import (
	"context"
	"log/slog"
	"time"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
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

// ValidateCheckoutSale reports whether a Checkout can belong to sale for
// stock: a FirstCome sale that names the stock's event. It fails with
// FailedPrecondition, which callers return as it is.
func ValidateCheckoutSale(sale *TicketSale, stock *TicketStock) error {
	if sale == nil {
		return apperr.New(codes.FailedPrecondition, "a checkout belongs to a ticket sale")
	}
	if sale.Method != TicketSaleMethodFirstCome {
		return apperr.New(codes.FailedPrecondition, "a checkout belongs to a first-come sale")
	}
	if !sale.NamesEvent(stock.EventID) {
		return apperr.New(codes.FailedPrecondition, "a checkout's sale must name the event of its ticket stock")
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
// hold has lapsed is Expired, since it can never be committed, even before
// CheckoutUseCase.Expire stores that; any other status is returned as stored.
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

// HoldingCheckoutOf returns the Checkout among checkouts that holds the stock
// at now, which a new start resumes or replaces, or nil when none does.
func HoldingCheckoutOf(checkouts []*Checkout, stockID TicketStockID, now time.Time) *Checkout {
	for _, c := range checkouts {
		if c.Order.TicketStockID == stockID && c.IsHoldingAt(now) {
			return c
		}
	}
	return nil
}

// CheckoutStep is the workflow a Checkout announcement starts.
type CheckoutStep int8

const (
	// CheckoutStepNone means the Checkout's status starts nothing.
	CheckoutStepNone CheckoutStep = iota
	// CheckoutStepExpire means a Held Checkout starts its expiry, which ends
	// the hold at its expiry time.
	CheckoutStepExpire
	// CheckoutStepReleaseCardAuthorization means an Expired or Released
	// Checkout gives its card authorization back.
	CheckoutStepReleaseCardAuthorization
)

// NextStep returns the workflow the Checkout's status starts.
func (r *Checkout) NextStep() CheckoutStep {
	switch r.Status {
	case CheckoutStatusHeld:
		return CheckoutStepExpire
	case CheckoutStatusExpired, CheckoutStatusReleased:
		return CheckoutStepReleaseCardAuthorization
	}
	return CheckoutStepNone
}

// CheckPlace reports what placing the Checkout needs: checks is true for a Held
// one, whose hold, concert, card authorization and verification are checked
// before the issue starts; a Committed one was placed before, and its issue is
// waited for. It fails with FailedPrecondition for one that has ended.
func (r *Checkout) CheckPlace() (checks bool, err error) {
	switch r.Status {
	case CheckoutStatusHeld:
		return true, nil
	case CheckoutStatusCommitted:
		return false, nil
	}
	return false, apperr.New(codes.FailedPrecondition, "the checkout has ended",
		slog.String("checkout_id", string(r.ID)), slog.String("status", r.Status.String()))
}

// CommitOutcome returns what [CheckoutRepository.Commit] reports for the
// Checkout as read, when that is already decided: Committed for a Committed or
// Completed one, NotHeld for an Expired or Released one. decided is false for a
// Held one, which the commit has to try.
func (r *Checkout) CommitOutcome() (outcome CommitOutcome, decided bool) {
	switch r.Status {
	case CheckoutStatusCommitted, CheckoutStatusCompleted:
		return CommitOutcomeCommitted, true
	case CheckoutStatusHeld:
		return 0, false
	default:
		return CommitOutcomeNotHeld, true
	}
}

// CheckNotCharged fails with FailedPrecondition for a charged Checkout, which
// is never reverted.
func (r *Checkout) CheckNotCharged() error {
	if r.IsCharged() {
		return apperr.New(codes.FailedPrecondition, "a charged checkout cannot be reverted",
			slog.String("checkout_id", string(r.ID)))
	}
	return nil
}

// CheckRevert reports whether a commit can be given back: revert is true for a
// Committed Checkout that was not charged. Any other status has nothing to
// revert (revert false, no error), except that a charged Committed Checkout
// fails with FailedPrecondition ([Checkout.CheckNotCharged]).
func (r *Checkout) CheckRevert() (revert bool, err error) {
	if r.Status != CheckoutStatusCommitted {
		return false, nil
	}
	if err := r.CheckNotCharged(); err != nil {
		return false, err
	}
	return true, nil
}

// CheckCaptureRecorded reports, for a Checkout whose capture time could not be
// stored, whether that is as it should be: nil when it was charged already
// (the capture time is set once), FailedPrecondition when it is not
// Committed.
func (r *Checkout) CheckCaptureRecorded() error {
	if r.IsCharged() {
		return nil
	}
	return apperr.New(codes.FailedPrecondition, "checkout is not Committed", slog.String("checkout_id", string(r.ID)))
}

// CheckoutEnd is one Held Checkout a new Checkout of the same fan replaces,
// with the change that ends it.
type CheckoutEnd struct {
	// Checkout is the Held Checkout, as read.
	Checkout *Checkout
	// Transition moves it from Held to Released when it is still holding, to
	// Expired when its hold lapsed.
	Transition Transition[CheckoutStatus]
}

// Subject is the announcement subject of the end: CHECKOUT.released or
// CHECKOUT.expired.
func (e CheckoutEnd) Subject() string {
	if e.Transition.To == CheckoutStatusReleased {
		return SubjectCheckoutReleased
	}
	return SubjectCheckoutExpired
}

// CheckoutStart is what starting a Checkout does with the fan's Held
// Checkouts of the stock at now.
type CheckoutStart struct {
	// Reuse is the holding Checkout of the same sale and ticket count, which
	// starting again returns as it is; nil when a new one is created.
	Reuse *Checkout
	// Replaced is the holding Checkout whose tickets count as free for the new
	// one; nil when none is holding.
	Replaced *Checkout
	// Ends are the Held Checkouts the new one replaces, holding or lapsed.
	Ends []CheckoutEnd
}

// PlanCheckoutStart decides, from the fan's Held Checkouts of a stock, whether
// starting a Checkout of the sale for count tickets reuses the holding one or
// replaces them: every Held Checkout is ended, Released while holding at now
// and Expired when lapsed, and the holding one's tickets count as free.
func PlanCheckoutStart(held []*Checkout, saleID TicketSaleID, count int, now time.Time) CheckoutStart {
	var plan CheckoutStart
	for _, c := range held {
		if c.IsHoldingAt(now) {
			plan.Replaced = c
		}
	}
	if plan.Replaced != nil && plan.Replaced.TicketSaleID == saleID && plan.Replaced.Order.TicketCount == count {
		plan.Reuse = plan.Replaced
		return plan
	}
	for _, c := range held {
		next := CheckoutStatusExpired
		if c.IsHoldingAt(now) {
			next = CheckoutStatusReleased
		}
		plan.Ends = append(plan.Ends, CheckoutEnd{
			Checkout:   c,
			Transition: Transition[CheckoutStatus]{From: []CheckoutStatus{CheckoutStatusHeld}, To: next},
		})
	}
	return plan
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
	//  - Unavailable: an announcement could not be recorded; nothing changed.
	//  - Internal: database failure.
	//
	// # Announces
	//
	// On creation, CHECKOUT.held with the new Checkout as Held, and
	// CHECKOUT.released or CHECKOUT.expired with each Checkout it
	// ends as it is after the change, all together with the creation. Nothing
	// when it returns an existing holding Checkout.
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
	//  - Unavailable: the announcement could not be recorded; nothing
	//    changes.
	//  - Internal: database failure.
	//
	// # Announces
	//
	// CHECKOUT.expired with the Checkout as Expired, together with the
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
	//  - Unavailable: the announcement could not be recorded; nothing changed.
	//  - Internal: database failure.
	//
	// # Announces
	//
	// CHECKOUT.released with the Checkout as Released, together with the
	// revert. Nothing when it changed nothing.
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
}
