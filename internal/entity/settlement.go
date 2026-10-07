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

// TransferParams carries the parameters for a single Stripe Transfer in the
// payout flow. All provider-specific concerns (idempotency key derivation,
// API version) are handled by the adapter.
type TransferParams struct {
	// SettlementID is used to derive a stable idempotency key so a retried
	// sweep never double-transfers the same split.
	SettlementID SettlementID
	// PayeeAccountRef is the connected account that receives the transfer
	// (the Organizer's "acct_..." id).
	PayeeAccountRef string
	// SourceTransactionRef is the captured charge ("ch_..." id) that backs
	// this transfer. Stripe requires source_transaction to come from the
	// platform balance and pins the transfer amount to the charge's settled
	// funds.
	SourceTransactionRef string
	// Amount is the transfer amount in the Order currency's smallest unit
	// (whole yen for JPY).
	Amount int64
	// Currency is the ISO 4217 currency code (JPY for the MVP).
	Currency string
}

// RefundParams carries the parameters for a platform-balance Refund. The
// refund is issued against the Charge (ch_) rather than the PaymentIntent so
// that the settlement's source_transaction reference remains coherent.
type RefundParams struct {
	// OrderID is always present and used to derive a stable, per-order
	// idempotency key ("order-refund:<orderID>") so retried refunds on the
	// same order never double-charge the platform. Using OrderID (not
	// SettlementID) avoids the "no-settlement" placeholder collision where
	// multiple orders without a settlement row would share the same key.
	OrderID OrderID
	// ChargeRef is the captured charge reference (ch_...) to refund.
	ChargeRef string
	// Amount is the refund amount in the Order currency's smallest unit.
	// For a cancellation this is the full Order amount (face + system fee;
	// processor fee retained). Must be positive.
	Amount int64
}

// ReverseTransferParams carries the parameters for a per-split transfer
// reversal. Each split that was paid out via Transfer must be individually
// reversed when a refund or dispute clawback occurs.
type ReverseTransferParams struct {
	// SettlementID is used to derive a stable idempotency key together with
	// the TransferRef.
	SettlementID SettlementID
	// TransferRef is the provider Transfer reference (tr_...) to reverse.
	TransferRef string
	// Amount is the reversal amount. For a full refund this equals the split's
	// original transfer amount. Must be positive.
	Amount int64
}

// PaymentSettlementPort abstracts the money-movement operations needed by the
// settlement use cases. The Stripe implementation lives in
// internal/infrastructure/payment/stripe_settlement.go; the no-op fallback
// for local dev lives alongside it. Defined here (entity package) so every
// usecase that needs it (and every implementation) depends on the same
// declaration.
type PaymentSettlementPort interface {
	// ResolveChargeRef retrieves the PaymentIntent identified by
	// paymentIntentRef and returns the charge id ("ch_...") of its captured
	// charge. The charge id is used as source_transaction on the Transfer.
	//
	// # Possible errors
	//
	//  - FailedPrecondition: the PaymentIntent is not in a succeeded state.
	//  - NotFound: the PaymentIntent does not exist.
	//  - Unavailable: the payment provider is unreachable.
	ResolveChargeRef(ctx context.Context, paymentIntentRef string) (chargeRef string, err error)

	// CreateTransfer creates a Stripe Transfer from the platform balance to
	// the Organizer's connected account (source_transaction = the charge).
	// The transfer carries NO on_behalf_of and NO statement descriptor:
	// the platform is the Stripe settlement merchant; the statement descriptor
	// is platform account configuration owned by cloud-provisioning task 5.1.
	//
	// # Possible errors
	//
	//  - InvalidArgument: amount is zero or negative.
	//  - Unavailable: the payment provider is unreachable.
	CreateTransfer(ctx context.Context, params TransferParams) (transferRef string, err error)

	// CreateConnectedAccount provisions a new Accounts v2 payout-recipient
	// connected account for the Organizer. The account requests ONLY the
	// transfers capability on stripe_balance (never card_payments) and is
	// configured with losses_collector = application so the platform absorbs
	// negative balances. Returns the opaque account reference ("acct_...").
	//
	// contactEmail is the Organizer's business contact address. Stripe rejects
	// a recipient configuration without one ("If configuration.recipient is
	// supplied, the Account must have a contact email"), and it is the address
	// Stripe uses to reach the account. Apart from it the request carries no
	// personal data: the Organizer supplies name, date of birth, address and
	// documents directly to Stripe through the hosted onboarding link, so the
	// account is created unverified and becomes payout-eligible only once
	// Stripe reports the transfers capability active.
	//
	// # Possible errors
	//
	//  - Unavailable: the payment provider is unreachable or platform Connect
	//    is not yet enabled.
	CreateConnectedAccount(ctx context.Context, organizerID string, contactEmail string) (accountRef string, err error)

	// GetAccountStatus retrieves the payout-onboarding status of the
	// connected account identified by accountRef. It maps the provider's
	// transfers capability state to the platform's own
	// PayoutOnboardingStatus enum without exposing raw provider status.
	//
	// # Possible errors
	//
	//  - NotFound: the account does not exist.
	//  - Unavailable: the payment provider is unreachable.
	GetAccountStatus(ctx context.Context, accountRef string) (PayoutOnboardingStatus, error)

	// CreateOnboardingLink returns a provider-hosted URL where the Organizer
	// can start or continue KYC/KYB verification. The link is single-use and
	// short-lived; callers must not cache it.
	//
	// # Possible errors
	//
	//  - NotFound: the account does not exist.
	//  - Unavailable: the payment provider is unreachable.
	CreateOnboardingLink(ctx context.Context, accountRef string, returnURL string) (onboardingURL string, err error)

	// CreateRefund issues a platform-balance Refund against the captured
	// Charge. The refund amount is the full Order amount minus the processor
	// fee (retained as the JP norm). Returns the opaque refund reference
	// ("re_..."). Idempotency is keyed on the SettlementID so a replay never
	// double-refunds.
	//
	// # Possible errors
	//
	//  - InvalidArgument: amount is zero or negative.
	//  - FailedPrecondition: the charge has already been fully refunded.
	//  - Unavailable: the payment provider is unreachable.
	CreateRefund(ctx context.Context, params RefundParams) (refundRef string, err error)

	// ReverseTransfer creates a transfer_reversal for one split's Transfer,
	// clawing back the Organizer's share to the platform balance. Returns the
	// opaque reversal reference ("trr_..."). Idempotency is keyed on
	// SettlementID+TransferRef so a replay never double-reverses.
	//
	// # Possible errors
	//
	//  - InvalidArgument: amount is zero or negative.
	//  - Unavailable: the payment provider is unreachable.
	ReverseTransfer(ctx context.Context, params ReverseTransferParams) (reversalRef string, err error)
}

// platformFeeRateNumerator and platformFeeRateDenominator define the
// platform's flat fee rate: 5% of an Order's amount (specification#778). Kept
// as a numerator/denominator pair so [PlatformFee] can use exact integer
// division rather than floating-point arithmetic on money.
const (
	platformFeeRateNumerator   = 5
	platformFeeRateDenominator = 100
)

// PlatformFee returns the platform's fee retained from an Order's amount: a
// flat 5% of amountJPY, rounded down to the nearest whole yen using integer
// arithmetic (floor(amountJPY * 5 / 100)). The Organizer's Settlement split is
// the remainder (amountJPY - PlatformFee(amountJPY)), so the Organizer
// absorbs the rounding remainder, never the platform.
//
// For very small amounts the fee rounds down to 0 (e.g. any amount under 20
// yen at the current 5% rate); the Organizer then receives the full amount as
// their split, which still satisfies the Settlement split invariants (every
// split greater than 0, the sum of splits no greater than the Order's
// amount) as long as amountJPY itself is positive.
func PlatformFee(amountJPY int64) int64 {
	return amountJPY * platformFeeRateNumerator / platformFeeRateDenominator
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
