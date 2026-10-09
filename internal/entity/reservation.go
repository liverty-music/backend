package entity

import (
	"context"
	"time"
)

// ReservationID is the opaque identifier for a single Reservation (one
// checkout). Server-generated (UUIDv7).
//
// Mirrors liverty_music.entity.v1.ReservationId.value.
type ReservationID string

// ReservationStatus is the lifecycle of a Reservation. Values mirror the proto
// enum liverty_music.entity.v1.ReservationStatus.
//
//	Held --> Committed --> Completed
//	Held --> Expired | Released
//	Committed --> Released   (only when the card was never charged)
type ReservationStatus int16

const (
	// ReservationStatusUnspecified is the zero value and is never persisted.
	ReservationStatusUnspecified ReservationStatus = 0
	// ReservationStatusHeld holds the tickets until the hold expiry.
	ReservationStatusHeld ReservationStatus = 1
	// ReservationStatusCommitted means the tickets were committed against the
	// stock while the hold lasted; the card is charged and the Order issued next.
	ReservationStatusCommitted ReservationStatus = 2
	// ReservationStatusCompleted means the card was charged and the Order issued.
	ReservationStatusCompleted ReservationStatus = 3
	// ReservationStatusExpired means the hold lapsed before a commit.
	ReservationStatusExpired ReservationStatus = 4
	// ReservationStatusReleased means the checkout ended without a charge: it
	// was replaced by a newer one, or its commit was given back.
	ReservationStatusReleased ReservationStatus = 5
)

// String returns the lowercase status name.
func (s ReservationStatus) String() string {
	switch s {
	case ReservationStatusHeld:
		return "held"
	case ReservationStatusCommitted:
		return "committed"
	case ReservationStatusCompleted:
		return "completed"
	case ReservationStatusExpired:
		return "expired"
	case ReservationStatusReleased:
		return "released"
	default:
		return "UNSPECIFIED"
	}
}

// ReservationHoldDuration is how long a new Reservation holds its tickets. It
// is never extended.
const ReservationHoldDuration = 15 * time.Minute

// ReservationStalledCommitAge is how long a Committed Reservation may stay
// uncompleted before the stalled-checkout job finishes it.
const ReservationStalledCommitAge = time.Minute

// Reservation is one fan's checkout on a TicketSale: it holds a count of
// tickets for 15 minutes while the fan enters their 本人確認 details and
// authorizes their card. It ends with the tickets committed, charged and
// issued, or with the hold released and any card hold given back.
//
// Mirrors liverty_music.entity.v1.Reservation.
type Reservation struct {
	// ID is the surrogate primary key (UUIDv7).
	ID ReservationID
	// TicketSaleID is the sale this checkout buys from.
	TicketSaleID TicketSaleID
	// UserID is the fan checking out.
	UserID UserID
	// TicketCount is the number of tickets held, 1 to the sale's per-account
	// limit.
	TicketCount int
	// Amount is the sale's price × TicketCount in yen, fixed at creation.
	Amount int64
	// HolderIdentity is the 本人確認 for the tickets' face; nil until the fan
	// authorizes.
	HolderIdentity *HolderIdentity
	// AuthorizationRef is the payment provider's reference of the card hold
	// (a Stripe "pi_..." id); empty until the fan authorizes, then set once.
	AuthorizationRef string
	// AuthorizationReleaseTime is when the card hold was given back without a
	// charge; nil unless released. Set once.
	AuthorizationReleaseTime *time.Time
	// Status is the checkout's lifecycle status.
	Status ReservationStatus
	// HoldExpireTime is 15 minutes after the checkout started; never extended.
	HoldExpireTime time.Time
	// CommitTime is when the tickets were committed; nil until committed and
	// kept when a commit is given back.
	CommitTime *time.Time
	// CaptureTime is when the card hold (AuthorizationRef) was charged; nil
	// until charged. Set once.
	CaptureTime *time.Time
}

// NewReservation returns a Held Reservation for count tickets of sale, with a
// generated UUIDv7 id, an amount of the sale's price × count and a hold expiry
// 15 minutes after now.
func NewReservation(sale *TicketSale, userID UserID, count int, now time.Time) *Reservation {
	return &Reservation{
		ID:             ReservationID(NewID()),
		TicketSaleID:   sale.ID,
		UserID:         userID,
		TicketCount:    count,
		Amount:         sale.Price * int64(count),
		Status:         ReservationStatusHeld,
		HoldExpireTime: now.Add(ReservationHoldDuration),
	}
}

// IsHoldingAt reports whether the Reservation is Held and t is before its hold
// expiry. Only holding Reservations count against a sale's remaining tickets,
// and only a holding Reservation can be committed.
func (r *Reservation) IsHoldingAt(t time.Time) bool {
	return r.Status == ReservationStatusHeld && t.Before(r.HoldExpireTime)
}

// IsCharged reports whether the Reservation's card was charged.
func (r *Reservation) IsCharged() bool {
	return r.CaptureTime != nil
}

// CanBecome reports whether the Reservation may move to status next. A
// charged Reservation can only become Completed: it is never Released or
// Expired.
func (r *Reservation) CanBecome(next ReservationStatus) bool {
	switch r.Status {
	case ReservationStatusHeld:
		return next == ReservationStatusCommitted || next == ReservationStatusExpired || next == ReservationStatusReleased
	case ReservationStatusCommitted:
		if r.IsCharged() {
			return next == ReservationStatusCompleted
		}
		return next == ReservationStatusCompleted || next == ReservationStatusReleased
	default:
		return false
	}
}

// Validate reports whether the Reservation's holder identity, when present,
// follows the holder identity rules.
func (r *Reservation) Validate() error {
	if r.HolderIdentity == nil {
		return nil
	}
	return r.HolderIdentity.Validate()
}

// CommitOutcome is what [ReservationRepository.Commit] reports.
type CommitOutcome int

const (
	// CommitOutcomeCommitted means the Reservation is Committed (now or before)
	// or Completed.
	CommitOutcomeCommitted CommitOutcome = 1
	// CommitOutcomeNotHeld means the Reservation was not holding: its hold
	// lapsed, or it is Expired or Released. Nothing changed.
	CommitOutcomeNotHeld CommitOutcome = 2
)

// ReservationRepository persists Reservations and keeps the sale's sold count
// in step with them. Implementations live in internal/infrastructure/database/rdb/.
//
// Interfaces are defined where consumed (AGENTS.md rule).
type ReservationRepository interface {
	// GetOrCreateHeld returns the user's holding Reservation for the sale when
	// its count equals count; otherwise it creates a new Held Reservation, in
	// one indivisible step under the sale's row lock. The new one is created
	// only when the remaining count at now, counting the user's own holding
	// Reservation as free, is at least count, and when the user's Committed and
	// Completed tickets on the sale plus count stay within the per-account
	// limit. On creation, the user's holding Reservation for the sale becomes
	// Released and a lapsed Held one becomes Expired. On failure nothing
	// changes.
	//
	// # Possible errors
	//
	//  - ResourceExhausted: not enough tickets remain.
	//  - FailedPrecondition: the per-account limit would be exceeded.
	//  - NotFound: no TicketSale has the id.
	//  - Internal: database failure.
	GetOrCreateHeld(ctx context.Context, saleID TicketSaleID, userID UserID, count int, now time.Time) (*Reservation, error)

	// Get returns the Reservation with the given id, whatever its status.
	//
	// # Possible errors
	//
	//  - NotFound: no Reservation has the id.
	//  - Internal: database failure.
	Get(ctx context.Context, id ReservationID) (*Reservation, error)

	// GetByAuthorizationRef returns the Reservation whose card hold has the
	// given reference.
	//
	// # Possible errors
	//
	//  - NotFound: no Reservation has the reference.
	//  - Internal: database failure.
	GetByAuthorizationRef(ctx context.Context, authorizationRef string) (*Reservation, error)

	// SetAuthorization stores the holder identity and the card hold's
	// reference of a Held Reservation. With the same reference already set it
	// stores the identity and succeeds.
	//
	// # Possible errors
	//
	//  - InvalidArgument: the identity breaks the holder identity rules.
	//  - FailedPrecondition: the Reservation has another reference or is not Held.
	//  - NotFound: no Reservation has the id.
	//  - Internal: database failure.
	SetAuthorization(ctx context.Context, id ReservationID, identity HolderIdentity, authorizationRef string) error

	// Commit makes a holding Reservation Committed at now and adds its count to
	// the sale's sold count, in one indivisible step that succeeds only while
	// the Reservation is Held and before its hold expiry. A Committed or
	// Completed Reservation reports Committed without a change; any other
	// reports NotHeld.
	//
	// # Possible errors
	//
	//  - NotFound: no Reservation has the id.
	//  - Internal: database failure.
	Commit(ctx context.Context, id ReservationID, now time.Time) (CommitOutcome, error)

	// Release makes a Held Reservation whose hold expired at or before now
	// Expired, in one indivisible step, and reports whether it changed it. Any
	// other Reservation is left unchanged.
	//
	// # Possible errors
	//
	//  - NotFound: no Reservation has the id.
	//  - Internal: database failure.
	Release(ctx context.Context, id ReservationID, now time.Time) (bool, error)

	// RevertCommit makes a Committed, uncharged Reservation Released and takes
	// its count off the sale's sold count, in one indivisible step, keeping its
	// committed time. A Reservation that is not Committed is left unchanged.
	//
	// # Possible errors
	//
	//  - FailedPrecondition: the Reservation has a capture time.
	//  - NotFound: no Reservation has the id.
	//  - Internal: database failure.
	RevertCommit(ctx context.Context, id ReservationID) error

	// RecordCapture stores the capture time of a Committed Reservation; the
	// charged payment is its card hold. It changes nothing when a capture time
	// is set.
	//
	// # Possible errors
	//
	//  - FailedPrecondition: the Reservation is not Committed.
	//  - NotFound: no Reservation has the id.
	//  - Internal: database failure.
	RecordCapture(ctx context.Context, id ReservationID, at time.Time) error

	// RecordAuthorizationRelease sets the authorization release time of an
	// Expired or Released Reservation that has an authorization reference. It
	// changes nothing when the time is already set.
	//
	// # Possible errors
	//
	//  - FailedPrecondition: the Reservation is in another status or has no
	//    authorization reference.
	//  - NotFound: no Reservation has the id.
	//  - Internal: database failure.
	RecordAuthorizationRelease(ctx context.Context, id ReservationID, at time.Time) error

	// ListDue returns, oldest hold first, the Held Reservations whose hold expired
	// at or before now, the Expired and Released Reservations with an
	// authorization reference and no authorization release time, and the
	// Committed Reservations committed more than 1 minute before now.
	//
	// # Possible errors
	//
	//  - Internal: database failure.
	ListDue(ctx context.Context, now time.Time) ([]*Reservation, error)

	// Serialize runs fn while holding an exclusive lock keyed on the
	// Reservation, so calls for the same Reservation run one after the other
	// and a second caller waits for the first. The lock is released when fn
	// returns, and also when the process holding it dies.
	//
	// # Possible errors
	//
	//  - Internal: the lock could not be taken.
	//  - Any error returned by fn, unchanged.
	Serialize(ctx context.Context, id ReservationID, fn func(ctx context.Context) error) error
}

// AuthorizationMetadata is the correlation data recorded on a checkout's card
// hold. It never carries personal data.
type AuthorizationMetadata struct {
	ReservationID ReservationID
	TicketSaleID  TicketSaleID
	EventID       string
}

// ReservationAuthorizationPort is the card hold of a first-come checkout: a
// manual-capture authorization in yen for the Reservation's amount, accepting
// every card brand (American Express included). One hold per Reservation.
// The Stripe implementation lives in internal/infrastructure/payment/.
type ReservationAuthorizationPort interface {
	// CreateAuthorization opens a hold for amountJPY that charges nothing until
	// CaptureAuthorization runs, and returns its reference and the confirmation
	// secret the browser authenticates with. Repeating it for the same
	// Reservation returns the same hold.
	//
	// # Possible errors
	//
	//  - InvalidArgument: amountJPY is zero or negative.
	//  - Unavailable: card payments cannot be reached.
	CreateAuthorization(ctx context.Context, amountJPY int64, meta AuthorizationMetadata) (authorizationRef string, clientSecret string, err error)

	// VerifyAuthorization succeeds only when the hold completed card
	// authentication and waits to be captured, for exactly expectedAmountJPY in
	// yen. Every card brand is accepted.
	//
	// # Possible errors
	//
	//  - FailedPrecondition: the hold is not authenticated or not capturable.
	//  - InvalidArgument: the amount differs or the currency is not yen.
	//  - NotFound: no hold has the reference.
	//  - Unavailable: card payments cannot be reached.
	VerifyAuthorization(ctx context.Context, authorizationRef string, expectedAmountJPY int64) error

	// CaptureAuthorization charges the held amount and returns the charged
	// payment. Its outcome follows the hold's state: an already charged hold
	// succeeds without charging again, however long ago it was charged.
	//
	// # Possible errors
	//
	//  - FailedPrecondition: the hold was released or expired, or the card can
	//    no longer be charged. Nothing is charged.
	//  - Unavailable: the outcome is not known yet (the provider is unreachable
	//    or another capture of the hold is in progress).
	//  - NotFound: no hold has the reference.
	CaptureAuthorization(ctx context.Context, authorizationRef string) (*CapturedPayment, error)

	// CancelAuthorization releases the hold. Repeating it, or calling it for an
	// expired hold, succeeds without a further effect.
	//
	// # Possible errors
	//
	//  - FailedPrecondition: the hold has already been charged.
	//  - NotFound: no hold has the reference.
	//  - Unavailable: card payments cannot be reached.
	CancelAuthorization(ctx context.Context, authorizationRef string) error
}
