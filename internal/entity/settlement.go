package entity

import (
	"context"
	"time"
)

// SettlementID is the opaque identifier for a single Settlement.
//
// Mirrors liverty_music.entity.v1.SettlementId.value (a UUID string); mapped
// to the proto wrapper at the handler boundary.
type SettlementID string

// SettlementStatus is the lifecycle of a Settlement (an Order's payout).
// Values mirror the proto enum liverty_music.entity.v1.SettlementStatus.
type SettlementStatus int16

const (
	// SettlementStatusUnspecified is the zero value and is never persisted.
	SettlementStatusUnspecified SettlementStatus = 0
	// SettlementStatusHeld means the charge is captured and funds are on the
	// platform balance; the release gate has not yet passed.
	SettlementStatusHeld SettlementStatus = 1
	// SettlementStatusReleased means the release gate passed, the Organizer's
	// net share was transferred, and the platform retained its fee.
	SettlementStatusReleased SettlementStatus = 2
	// SettlementStatusReversed means the Transfer(s) were reversed on a refund
	// or dispute clawback.
	SettlementStatusReversed SettlementStatus = 3
)

// String returns the lowercase status name.
func (s SettlementStatus) String() string {
	switch s {
	case SettlementStatusHeld:
		return "held"
	case SettlementStatusReleased:
		return "released"
	case SettlementStatusReversed:
		return "reversed"
	default:
		return "UNSPECIFIED"
	}
}

// IsValid reports whether s is a recognized, persistable status.
func (s SettlementStatus) IsValid() bool {
	return s >= SettlementStatusHeld && s <= SettlementStatusReversed
}

// SettlementSplit is one payee's share of a Settlement.
//
// The payout is modelled as a set of splits so additional payees (venue,
// artist) can be added later without a schema break. The MVP has exactly one
// split — the Organizer — and the platform fee is the retained remainder.
// Each split is realized as its own Stripe Transfer with source_transaction
// = the Settlement's charge, and the sum of all splits never exceeds the
// charge amount. Mirrors liverty_music.entity.v1.SettlementSplit.
type SettlementSplit struct {
	// PayeeOrganizerID is the Organizer that receives this split.
	PayeeOrganizerID string
	// Amount is the payee's net share in the Order's currency smallest unit
	// (whole yen for JPY). Must be positive.
	Amount int64
	// TransferRef is the provider's Transfer reference (e.g. a Stripe "tr_..."
	// id) that paid this split. Empty until the split is released.
	TransferRef string
	// TransferReversalRef is the provider's transfer-reversal reference (e.g.
	// a Stripe "trr_..." id) when the split was clawed back. Empty unless
	// reversed.
	TransferReversalRef string
}

// Settlement is the payout record for one Order's captured funds.
//
// ④'s captured charge lands on the PLATFORM balance (separate charges &
// transfers); after the release gate this record's split(s) are transferred to
// the Organizer's connected account (source_transaction = the charge) and the
// platform retains its fee as the un-transferred remainder. It is
// provider-agnostic: it stores only opaque provider tokens and its own status,
// never raw provider status. Mirrors liverty_music.entity.v1.Settlement.
type Settlement struct {
	// ID is the surrogate primary key (UUIDv7).
	ID SettlementID
	// OrderID is the Order this settlement pays out. One Settlement per Order.
	OrderID OrderID
	// OrganizerID is the Organizer that receives the payout. Stored denormalized
	// so the sweeper can look up the Organizer's connected account without
	// joining through the application/phase/event chain.
	OrganizerID string
	// EventID is the event this settlement is for. Stored denormalized so the
	// release-gate check can read events.start_at without joining through
	// order → ticket_application → lottery_sales_phase.
	EventID string
	// ChargeRef is the provider's Charge reference (e.g. a Stripe "ch_..." id)
	// for ④'s captured payment, used as each Transfer's source_transaction.
	// Resolved from the Order's PaymentIntent at payout time. Empty until
	// resolved.
	ChargeRef string
	// Splits are the payout splits for this settlement. MVP = exactly one
	// (the Organizer); the platform fee is the retained remainder, not a split.
	Splits []SettlementSplit
	// Status is the settlement's own lifecycle status.
	Status SettlementStatus
	// ReleasedTime is when the payout was released (Transfer(s) created). Zero
	// while still held.
	ReleasedTime time.Time
	// CreatedTime is when this settlement row was created (= Order issuance
	// time; inserted atomically by [IssuanceRepository.Issue]).
	CreatedTime time.Time
}

// SettlementRepository defines the persistence contract for Settlement records.
// The row is created by [IssuanceRepository.Issue], not by this interface —
// SettlementRepository only reads and updates settlements after issuance.
// Interfaces are defined where consumed (AGENTS.md rule).
type SettlementRepository interface {
	// Get returns the Settlement for the given id.
	//
	// # Possible errors
	//
	//  - NotFound: no Settlement with the id exists.
	//  - Internal: database query failure.
	Get(ctx context.Context, id SettlementID) (*Settlement, error)

	// GetByOrderID returns the Settlement for the given Order, if any.
	//
	// # Possible errors
	//
	//  - NotFound: no Settlement exists for the Order (not yet created).
	//  - Internal: database query failure.
	GetByOrderID(ctx context.Context, orderID OrderID) (*Settlement, error)

	// ListHeld returns all Settlement rows in the Held status whose
	// Organizer account is payout-active. The sweeper uses this to build the
	// work list for each tick.
	//
	// # Possible errors
	//
	//  - Internal: database query failure.
	ListHeld(ctx context.Context) ([]*Settlement, error)

	// MarkReleased atomically sets the settlement to Released, records the
	// resolved ChargeRef, and stores the Transfer reference(s) on each split.
	// The update is conditional on the current status being Held to prevent a
	// double-release (idempotency guard).
	//
	// # Possible errors
	//
	//  - NotFound: no Settlement with the id exists.
	//  - FailedPrecondition: settlement is not in Held status (already released
	//    or reversed).
	//  - Internal: database execution failure.
	MarkReleased(ctx context.Context, id SettlementID, chargeRef string, releasedAt time.Time, splits []SettlementSplit) error
}

// IsReleaseEligible reports whether a settlement may be released.
//
// The release gate requires both:
//  1. The event's current start_time has passed (counter-performance gate).
//  2. A dispute-safety buffer has elapsed since the event started.
//
// On a postponement, the caller reads the updated start_time from the database
// so the reset is implicit: if start_time is a future date the gate is not yet
// passed. A nil start_time means the event time is not yet published; the
// settlement is withheld (not failed).
//
// This is a pure function so it can be unit-tested without a database.
func IsReleaseEligible(now time.Time, eventStartTime time.Time, disputeBuffer time.Duration) bool {
	if eventStartTime.IsZero() {
		// start_time not yet published; hold until known.
		return false
	}
	return now.After(eventStartTime.Add(disputeBuffer))
}
