package entity

import (
	"context"
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

// Checkout is one fan's purchase in progress on a TicketType of a FirstCome
// TicketSale: it holds a count of tickets for 15 minutes while the fan
// confirms their 本人確認 (identity check) details and authorizes their card.
// It ends with the tickets committed, charged and issued, or with the hold
// released and any card authorization given back. The fan's full name and phone number
// are the User's.
//
// Mirrors liverty_music.entity.v1.Checkout.
type Checkout struct {
	// ID is the surrogate primary key (UUIDv7).
	ID CheckoutID
	// TicketTypeID is the TicketType this checkout buys from; its sale's
	// method is FirstCome.
	TicketTypeID TicketTypeID
	// UserID is the fan checking out.
	UserID UserID
	// TicketCount is the number of tickets held, 1 to the ticket type's
	// per-account limit.
	TicketCount int
	// Amount is the ticket type's price × TicketCount in yen, fixed at creation.
	Amount int64
	// CardAuthorizationID is the payment provider's ID for the card authorization
	// (a Stripe "pi_..." id); empty until the fan authorizes, then set once.
	CardAuthorizationID string
	// AuthorizationReleaseTime is when the card authorization was given back without a
	// charge; nil unless released. Set once.
	CardAuthorizationReleaseTime *time.Time
	// Status is the checkout's lifecycle status.
	Status CheckoutStatus
	// HoldExpireTime is 15 minutes after the checkout started; never extended.
	HoldExpireTime time.Time
	// CommitTime is when the tickets were committed; nil until committed and
	// kept when a commit is given back.
	CommitTime *time.Time
	// CaptureTime is when the card authorization (CardAuthorizationID) was charged; nil
	// until charged. Set once.
	CaptureTime *time.Time
}

// NewCheckout returns a Held Checkout for count tickets of ticketType, with a
// generated UUIDv7 id, an amount of the ticket type's price × count and a hold
// expiry 15 minutes after now.
func NewCheckout(ticketType *TicketType, userID UserID, count int, now time.Time) *Checkout {
	return &Checkout{
		ID:             CheckoutID(NewID()),
		TicketTypeID:   ticketType.ID,
		UserID:         userID,
		TicketCount:    count,
		Amount:         ticketType.AmountFor(count),
		Status:         CheckoutStatusHeld,
		HoldExpireTime: now.Add(CheckoutHoldDuration),
	}
}

// IsHoldingAt reports whether the Checkout is Held and t is before its hold
// expiry. Only holding Checkouts count against a ticket type's remaining tickets,
// and only a holding Checkout can be committed.
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

// CheckoutRepository persists Checkouts and keeps the ticket type's sold count
// in step with them. Implementations live in internal/infrastructure/database/rdb/.
//
// Interfaces are defined where consumed (AGENTS.md rule).
type CheckoutRepository interface {
	// GetOrCreateHeld returns the user's holding Checkout for the ticket type
	// when its count equals count, changing nothing; otherwise it creates a new
	// Held Checkout, in one indivisible step under the ticket type's row lock.
	// The new one is created only when the remaining count at now, counting the
	// user's own holding Checkout as free, is at least count, and when count
	// fits the per-account limit counting the user's Issued Tickets for the
	// ticket type's event and the tickets on the user's Committed Checkouts for
	// the ticket type that are not issued yet. On creation, the user's holding
	// Checkout for the ticket type becomes Released and a lapsed Held one
	// becomes Expired. On failure nothing changes.
	//
	// # Possible errors
	//
	//  - ResourceExhausted: not enough tickets remain.
	//  - FailedPrecondition: the per-account limit would be exceeded.
	//  - NotFound: no TicketType has the id.
	//  - Internal: database failure.
	GetOrCreateHeld(ctx context.Context, ticketTypeID TicketTypeID, userID UserID, count int, now time.Time) (*Checkout, error)

	// Get returns the Checkout with the given id, whatever its status.
	//
	// # Possible errors
	//
	//  - NotFound: no Checkout has the id.
	//  - Internal: database failure.
	Get(ctx context.Context, id CheckoutID) (*Checkout, error)

	// GetByCardAuthorizationID returns the Checkout that references the given
	// card authorization.
	//
	// # Possible errors
	//
	//  - NotFound: no Checkout has the reference.
	//  - Internal: database failure.
	GetByCardAuthorizationID(ctx context.Context, cardAuthorizationID string) (*Checkout, error)

	// SetAuthorization stores the card authorization ID of a Held Checkout; it
	// changes no other attribute. With the same reference already set it
	// changes nothing and succeeds.
	//
	// # Possible errors
	//
	//  - FailedPrecondition: the Checkout has another reference or is not Held.
	//  - NotFound: no Checkout has the id.
	//  - Internal: database failure.
	SetCardAuthorization(ctx context.Context, id CheckoutID, cardAuthorizationID string) error

	// Commit makes a holding Checkout Committed at now and adds its count to
	// the ticket type's sold count, in one indivisible step that succeeds only while
	// the Checkout is Held and before its hold expiry. A Committed or
	// Completed Checkout reports Committed without a change; any other
	// reports NotHeld.
	//
	// # Possible errors
	//
	//  - NotFound: no Checkout has the id.
	//  - Internal: database failure.
	Commit(ctx context.Context, id CheckoutID, now time.Time) (CommitOutcome, error)

	// Release makes a Held Checkout whose hold expired at or before now
	// Expired, in one indivisible step, and reports whether it changed it. Any
	// other Checkout is left unchanged.
	//
	// # Possible errors
	//
	//  - NotFound: no Checkout has the id.
	//  - Internal: database failure.
	Release(ctx context.Context, id CheckoutID, now time.Time) (bool, error)

	// RevertCommit makes a Committed, uncharged Checkout Released and takes
	// its count off the ticket type's sold count, in one indivisible step, keeping its
	// committed time. A Checkout that is not Committed is left unchanged.
	//
	// # Possible errors
	//
	//  - FailedPrecondition: the Checkout has a capture time.
	//  - NotFound: no Checkout has the id.
	//  - Internal: database failure.
	RevertCommit(ctx context.Context, id CheckoutID) error

	// RecordCapture stores the capture time of a Committed Checkout; the
	// charged payment is its card authorization. It changes nothing when a capture time
	// is set.
	//
	// # Possible errors
	//
	//  - FailedPrecondition: the Checkout is not Committed.
	//  - NotFound: no Checkout has the id.
	//  - Internal: database failure.
	RecordCapture(ctx context.Context, id CheckoutID, at time.Time) error

	// RecordAuthorizationRelease sets the authorization release time of an
	// Expired or Released Checkout that has an authorization reference. It
	// changes nothing when the time is already set.
	//
	// # Possible errors
	//
	//  - FailedPrecondition: the Checkout is in another status or has no
	//    authorization reference.
	//  - NotFound: no Checkout has the id.
	//  - Internal: database failure.
	RecordCardAuthorizationRelease(ctx context.Context, id CheckoutID, at time.Time) error

	// ListDue returns, oldest hold first, the Held Checkouts whose hold expired
	// at or before now, the Expired and Released Checkouts with an
	// authorization reference and no authorization release time, and the
	// Committed Checkouts committed more than 1 minute before now.
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
