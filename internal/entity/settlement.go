package entity

import (
	"context"
	"fmt"
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
	// ProviderPayoutID is the payment provider's ID for the payout (e.g. a Stripe "tr_..."
	// id) that paid this split. Empty until the split is released.
	ProviderPayoutID string
	// ProviderPayoutReversalID is the payment provider's ID for the payout reversal (e.g.
	// a Stripe "trr_..." id) when the split was clawed back. Empty unless
	// reversed.
	ProviderPayoutReversalID string
}

// Settlement is the payout record for one Order's captured funds.
//
// ④'s captured charge lands on the PLATFORM balance (separate charges &
// transfers); after the release gate this record's split(s) are transferred to
// the Organizer's payout account (source_transaction = the charge) and the
// platform retains its fee as the un-transferred remainder. It is
// provider-agnostic: it stores only opaque provider tokens and its own status,
// never raw provider status. Mirrors liverty_music.entity.v1.Settlement.
type Settlement struct {
	// ID is the surrogate primary key (UUIDv7).
	ID SettlementID
	// OrderID is the Order this settlement pays out. One Settlement per Order.
	OrderID OrderID
	// OrganizerID is the Organizer that receives the payout. Stored denormalized
	// so the sweeper can look up the Organizer's payout account without
	// joining through the lottery entry/ticket type/event chain.
	OrganizerID string
	// EventID is the event this settlement is for. Stored denormalized so the
	// release-gate check can read events.start_at without joining through
	// order → lottery entry or checkout → ticket type.
	EventID string
	// ProviderChargeID is the payment provider's ID for the charge (e.g. a Stripe "ch_..." id)
	// for ④'s captured payment, used as each Transfer's source_transaction.
	// Resolved from the Order's provider payment ID at payout time. Empty until
	// resolved.
	ProviderChargeID string
	// Splits are the payout splits for this settlement. MVP = exactly one
	// (the Organizer); the platform fee is the retained remainder, not a split.
	Splits []SettlementSplit
	// Status is the settlement's own lifecycle status.
	Status SettlementStatus
	// ReleaseTime is when the payout was released (Transfer(s) created). Zero
	// while still held.
	ReleaseTime time.Time
	// CreateTime is when this settlement row was created (= Order issuance
	// time; inserted atomically by [IssuanceRepository.Issue]).
	CreateTime time.Time
}

// NewHeldSettlement returns a Held Settlement for the Order, paying the
// Organizer the Order's amount minus the platform fee at rateBps. The split is
// fixed here, so a later change to the Organizer's rate never alters it.
func NewHeldSettlement(order *Order, organizerID, eventID string, rateBps int, now time.Time) *Settlement {
	return &Settlement{
		ID:          SettlementID(NewID()),
		OrderID:     order.ID,
		OrganizerID: organizerID,
		EventID:     eventID,
		Status:      SettlementStatusHeld,
		Splits: []SettlementSplit{
			{PayeeOrganizerID: organizerID, Amount: order.Amount - PlatformFee(order.Amount, rateBps)},
		},
		CreateTime: now,
	}
}

// GiveUpResoldShare takes the share of k resold tickets off the Held
// Settlement of an Order that had m Issued Tickets before the resale: each
// split falls by its amount × k ÷ m, rounded down to the yen, so rounding
// favours the Organizer. When k equals m nothing is left to pay out for the
// Order, and the Settlement becomes Reversed instead, keeping its splits. The
// entity layer returns stdlib errors; callers wrap them with an apperr code.
func (s *Settlement) GiveUpResoldShare(k, m int) error {
	if s.Status != SettlementStatusHeld {
		return fmt.Errorf("only a Held settlement gives up a resold share, not a %s one", s.Status)
	}
	if k < 1 || k > m {
		return fmt.Errorf("resold tickets must be 1 to %d, not %d", m, k)
	}
	if k == m {
		s.Status = SettlementStatusReversed
		return nil
	}
	for i := range s.Splits {
		s.Splits[i].Amount -= s.Splits[i].Amount * int64(k) / int64(m)
	}
	return nil
}

// SettlementRepository defines the persistence contract for Settlement records.
// The row is created by [IssuanceRepository.Issue], not by this interface —
// SettlementRepository only reads and updates settlements after issuance.
// Interfaces are defined where consumed (AGENTS.md rule).
type SettlementRepository interface {

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
	// resolved ProviderChargeID, and stores the Transfer reference(s) on each split.
	// The update is conditional on the current status being Held to prevent a
	// double-release (idempotency guard).
	//
	// # Possible errors
	//
	//  - NotFound: no Settlement with the id exists.
	//  - FailedPrecondition: settlement is not in Held status (already released
	//    or reversed).
	//  - Internal: database execution failure.
	MarkReleased(ctx context.Context, id SettlementID, providerChargeID string, releasedAt time.Time, splits []SettlementSplit) error

	// CreatePayout creates a Stripe Transfer from the platform balance to
	// the Organizer's payout account (source_transaction = the charge).
	// The transfer carries NO on_behalf_of and NO statement descriptor:
	// the platform is the Stripe settlement merchant; the statement descriptor
	// is platform account configuration owned by cloud-provisioning task 5.1.
	//
	// # Possible errors
	//
	//  - InvalidArgument: amount is zero or negative.
	//  - Unavailable: the payment provider is unreachable.
	CreatePayout(ctx context.Context, params PayoutParams) (providerPayoutID string, err error)

	// ReversePayout creates a transfer_reversal for one split's Transfer,
	// clawing back the Organizer's share to the platform balance. Returns the
	// payment provider's ID for the reversal ("trr_..."). Idempotency is keyed on
	// SettlementID+ProviderPayoutID so a replay never double-reverses.
	//
	// # Possible errors
	//
	//  - InvalidArgument: amount is zero or negative.
	//  - Unavailable: the payment provider is unreachable.
	ReversePayout(ctx context.Context, params ReversePayoutParams) (providerPayoutReversalID string, err error)
}

// PayoutParams carries the parameters for a single Stripe Transfer in the
// payout flow. All provider-specific concerns (idempotency key derivation,
// API version) are handled by the adapter.
type PayoutParams struct {
	// SettlementID is used to derive a stable idempotency key so a retried
	// sweep never double-transfers the same split.
	SettlementID SettlementID
	// PayeeProviderAccountID is the payout account that receives the transfer
	// (the Organizer's "acct_..." id).
	PayeeProviderAccountID string
	// ProviderChargeID is the captured charge ("ch_..." id) that backs
	// this transfer. Stripe requires source_transaction to come from the
	// platform balance and pins the transfer amount to the charge's settled
	// funds.
	ProviderChargeID string
	// Amount is the transfer amount in the Order currency's smallest unit
	// (whole yen for JPY).
	Amount int64
	// Currency is the ISO 4217 currency code (JPY for the MVP).
	Currency string
}

// RefundParams carries the parameters for a platform-balance Refund. The
// refund is issued against the Charge (ch_) rather than the payment so
// that the settlement's source_transaction reference remains coherent.
//
// A refund has a purpose: the Order's own refund (cancellation), or the
// refund of one ResaleListing of the Order. Each purpose refunds at most once,
// and refunds of different purposes are separate refunds of parts of the same
// charge.
type RefundParams struct {
	// OrderID is always present. For the Order's own refund it derives a
	// stable, per-order idempotency key ("order-refund:<orderID>") so retried
	// refunds on the same order never double-charge the platform. Using
	// OrderID (not SettlementID) avoids the "no-settlement" placeholder
	// collision where multiple orders without a settlement row would share
	// the same key.
	OrderID OrderID
	// ResaleListingID names the purpose: empty for the Order's own refund, or
	// the ResaleListing of the Order whose refund amount this refunds, keyed
	// "resale-listing-refund:<listingID>".
	ResaleListingID ResaleListingID
	// ProviderChargeID is the captured charge reference (ch_...) to refund.
	ProviderChargeID string
	// Amount is the refund amount in the Order currency's smallest unit.
	// For a cancellation this is the full Order amount (face + system fee;
	// processor fee retained). Must be positive.
	Amount int64
}

// ReversePayoutParams carries the parameters for a per-split transfer
// reversal. Each split that was paid out via Transfer must be individually
// reversed when a refund or dispute clawback occurs.
type ReversePayoutParams struct {
	// SettlementID is used to derive a stable idempotency key together with
	// the ProviderPayoutID.
	SettlementID SettlementID
	// ProviderPayoutID is the provider Transfer reference (tr_...) to reverse.
	ProviderPayoutID string
	// Amount is the reversal amount. For a full refund this equals the split's
	// original transfer amount. Must be positive.
	Amount int64
}

// basisPointsPerUnit is the denominator of a rate in basis points (hundredths
// of a percent).
const basisPointsPerUnit = 10000

// PlatformFee returns the platform's fee retained from an Order's amount at
// the Organizer's rate in basis points, rounded down to the nearest whole yen
// using integer arithmetic (floor(amountJPY * rateBps / 10000)). The
// Organizer's Settlement split is the remainder, so the Organizer absorbs the
// rounding remainder, never the platform.
//
// For very small amounts the fee rounds down to 0 (e.g. 12 yen at 8%); the
// Organizer then receives the full amount as their split, which still
// satisfies the Settlement split invariants as long as amountJPY is positive.
func PlatformFee(amountJPY int64, rateBps int) int64 {
	return amountJPY * int64(rateBps) / basisPointsPerUnit
}

// IsReleaseEligible reports whether a settlement may be released.
//
// The release gate requires both:
//  1. The event's current start_time has passed (counter-performance gate).
//  2. A dispute-safety buffer has elapsed since the event started.
//
// The caller reads the event's current start_time from the database on every
// sweep, so a start_time filled in after the purchase takes effect on the next
// run. A nil start_time means the event time is not yet published; the
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
