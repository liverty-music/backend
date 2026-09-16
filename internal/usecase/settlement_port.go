package usecase

import (
	"context"
	"time"

	"github.com/liverty-music/backend/internal/entity"
)

// TransferParams carries the parameters for a single Stripe Transfer in the
// payout flow. All provider-specific concerns (idempotency key derivation,
// API version) are handled by the adapter.
type TransferParams struct {
	// SettlementID is used to derive a stable idempotency key so a retried
	// sweep never double-transfers the same split.
	SettlementID entity.SettlementID
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
	OrderID entity.OrderID
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
	SettlementID entity.SettlementID
	// TransferRef is the provider Transfer reference (tr_...) to reverse.
	TransferRef string
	// Amount is the reversal amount. For a full refund this equals the split's
	// original transfer amount. Must be positive.
	Amount int64
}

// PaymentSettlementPort abstracts the money-movement operations needed by the
// settlement use cases. The Stripe implementation lives in
// internal/infrastructure/payment/stripe_settlement.go; the no-op fallback
// for local dev lives alongside it. Interfaces are defined where consumed
// (AGENTS.md rule).
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
	// entity.PayoutOnboardingStatus enum without exposing raw provider status.
	//
	// # Possible errors
	//
	//  - NotFound: the account does not exist.
	//  - Unavailable: the payment provider is unreachable.
	GetAccountStatus(ctx context.Context, accountRef string) (entity.PayoutOnboardingStatus, error)

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

// ProcessedWebhookEventRepository stores the provider event ids that have
// already been applied so that duplicate webhook deliveries are no-ops.
// Interfaces are defined where consumed (AGENTS.md rule).
type ProcessedWebhookEventRepository interface {
	// IsProcessed returns true if the provider event id has already been
	// applied.
	//
	// # Possible errors
	//
	//  - Internal: database query failure.
	IsProcessed(ctx context.Context, providerEventID string) (bool, error)

	// MarkProcessed records that the provider event id has been applied.
	// Idempotent: a duplicate insert is silently accepted (ON CONFLICT DO NOTHING).
	//
	// # Possible errors
	//
	//  - Internal: database execution failure.
	MarkProcessed(ctx context.Context, providerEventID string) error
}

// EventStartTimeRepository reads the event start time from the database. A
// minimal interface so the settlement use case does not depend on the full
// concert repository. Interfaces are defined where consumed (AGENTS.md rule).
type EventStartTimeRepository interface {
	// GetEventStartTime returns the start_at timestamp for the event
	// identified by eventID. A nil result means the event's start time is not
	// yet published; the settlement sweeper withholds payout rather than
	// erroring.
	//
	// # Possible errors
	//
	//  - NotFound: no event with the given id exists.
	//  - Internal: database query failure.
	GetEventStartTime(ctx context.Context, eventID string) (*time.Time, error)
}
