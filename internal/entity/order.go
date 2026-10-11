package entity

import (
	"context"
	"errors"
	"time"
)

// OrderID is the opaque identifier for a single Order — the purchase record of
// one won lottery entry or one completed checkout.
//
// Mirrors liverty_music.entity.v1.OrderId.value (a UUID string); mapped to the
// proto wrapper at the handler boundary.
type OrderID string

// PaymentProvider names which payment provider backs an Order's captured
// payment. The Order is provider-agnostic (it stores only opaque references),
// so switching provider is a config/adapter change. Values mirror the proto
// enum liverty_music.entity.v1.PaymentProvider.
type PaymentProvider int16

const (
	// PaymentProviderUnspecified is the zero value and is never persisted.
	PaymentProviderUnspecified PaymentProvider = 0
	// PaymentProviderStripe is Stripe — the MVP provider. ④'s charge is a plain
	// platform-account charge, so captured funds are platform-held; the post-event
	// Transfer of the Organizer's share is owned by ticket-settlement-and-payout,
	// not by ⑤.
	PaymentProviderStripe PaymentProvider = 1
	// PaymentProviderKOMOJU is the KOMOJU PoC challenger, kept swappable behind
	// the opaque references.
	PaymentProviderKOMOJU PaymentProvider = 2
)

// String returns the lowercase provider name.
func (p PaymentProvider) String() string {
	switch p {
	case PaymentProviderStripe:
		return "stripe"
	case PaymentProviderKOMOJU:
		return "komoju"
	default:
		return "UNSPECIFIED"
	}
}

// IsValid reports whether p is a recognized, persistable provider.
func (p PaymentProvider) IsValid() bool {
	return p == PaymentProviderStripe || p == PaymentProviderKOMOJU
}

// OrderStatus is the lifecycle of an Order. There is no `pending` state: ⑤
// creates the Order from an already-captured payment, so it is Paid on
// creation. Values mirror the proto enum liverty_music.entity.v1.OrderStatus.
type OrderStatus int16

const (
	// OrderStatusUnspecified is the zero value and is never persisted.
	OrderStatusUnspecified OrderStatus = 0
	// OrderStatusPaid is the normal state on creation — built from ④'s captured
	// winning payment, so funds are already captured (held on the platform
	// balance) when the Order exists.
	OrderStatusPaid OrderStatus = 1
	// OrderStatusRefunded means the captured payment was refunded (event
	// cancellation or dispute). ⑤ owns the refund policy and sets this status;
	// the Refund + transfer_reversal money movement is executed by
	// ticket-settlement-and-payout.
	OrderStatusRefunded OrderStatus = 2
	// OrderStatusFailed is the capture-succeeded-but-issuance-refunded edge: the
	// capture succeeded but issuance could not complete, so the captured payment
	// was refunded to avoid money-captured-with-no-ticket.
	OrderStatusFailed OrderStatus = 3
)

// String returns the lowercase status name.
func (s OrderStatus) String() string {
	switch s {
	case OrderStatusPaid:
		return "paid"
	case OrderStatusRefunded:
		return "refunded"
	case OrderStatusFailed:
		return "failed"
	default:
		return "UNSPECIFIED"
	}
}

// IsValid reports whether s is a recognized, persistable status.
func (s OrderStatus) IsValid() bool {
	return s >= OrderStatusPaid && s <= OrderStatusFailed
}

// Payment is the opaque, provider-agnostic reference to ④'s captured winning
// payment, plus the display-only facets safe to retain. It holds only tokens
// meaningful to the provider and NEVER raw card data (no PAN, CVC, or expiry).
// Mirrors liverty_music.entity.v1.Payment.
type Payment struct {
	// Provider backs the captured payment; drives which adapter interprets the
	// opaque references below.
	Provider PaymentProvider
	// ProviderPaymentID is the provider's PaymentIntent reference (e.g. a Stripe
	// "pi_..." id) for ④'s captured manual-capture authorization. Opaque; ⑤
	// references but never re-charges it.
	ProviderPaymentID string
	// ProviderPaymentMethodID is the provider's PaymentMethod reference (e.g. a Stripe
	// "pm_..." id) when distinct from the PaymentIntent. Optional, opaque.
	ProviderPaymentMethodID string
	// CardBrand is a display-only card brand facet (e.g. "visa"). Optional.
	CardBrand string
	// CardLast4 is the display-only last four digits of the card. Optional; this
	// is NOT the PAN.
	CardLast4 string
}

// Order is a provider- and method-agnostic purchase record: the opaque
// reference to the captured payment, its own status, total amount and
// currency, the capture time, and display facets. It has exactly one source —
// a Won LotteryEntry or a Committed, charged Checkout — and ONE
// Order covers all N tickets of that source.
//
// It NEVER stores a PAN, CVC, or expiry — only opaque provider tokens and safe
// display facets (PCI SAQ A). Mirrors liverty_music.entity.v1.Order.
type Order struct {
	// ID is the surrogate primary key (UUIDv7).
	ID OrderID
	// UserID is the fan account that bought the Order. The issued tickets are
	// bound to this account.
	UserID UserID
	// LotteryEntryID is the Won entry this Order derives from. Empty when the
	// source is a Checkout. The one-Order-per-entry invariant is keyed on this
	// reference.
	LotteryEntryID LotteryEntryID
	// CheckoutID is the checkout this Order completes. Empty when the source
	// is a lottery entry. The one-Order-per-Checkout invariant is keyed on this
	// reference.
	CheckoutID CheckoutID
	// Payment is the opaque reference to ④'s captured winning payment plus safe
	// display facets. Never raw card data.
	Payment Payment
	// Status is the order's own status — Paid on creation.
	Status OrderStatus
	// Amount is the total captured amount in the currency's smallest unit. For
	// JPY (no minor unit) this is the yen total (ticket price × ticket count).
	Amount int64
	// Currency is the ISO 4217 currency code of Amount (JPY for the MVP).
	Currency string
	// PayTime is when the payment was captured (= ④'s capture time at the draw).
	// Because the Order is created already paid, this is effectively its creation
	// time.
	PayTime time.Time
	// ProviderRefundID is the opaque provider Refund reference ("re_...") set when the
	// Order is refunded via CANCELLATION. Empty for orders
	// refunded via DISPUTE (the chargeback reversed the charge at the card
	// network; no Stripe Refund object is created) and for orders not yet
	// refunded.
	ProviderRefundID string
	// ConfirmationSendTime is when the purchase confirmation email was sent;
	// nil until sent. It keeps the email to one per Order.
	ConfirmationSendTime *time.Time
}

// ValidateSource reports whether the Order names exactly one source: a
// LotteryEntry or a Checkout.
func (o *Order) ValidateSource() error {
	hasEntry, hasCheckout := o.LotteryEntryID != "", o.CheckoutID != ""
	if hasEntry == hasCheckout {
		return errors.New("an order must come from exactly one of a lottery entry and a checkout")
	}
	return nil
}

// RefundCommit bundles all of the DB state that must be persisted atomically
// when an Order is refunded. The refund use case performs the Stripe money
// movement (CreateRefund + ReversePayout) first — those are idempotent — and
// then calls [RefundRepository.CommitRefund] to atomically commit the DB side.
// This ensures the observable invariant: an Order is either fully-Paid (nothing
// applied) or fully-Refunded (all three mutations committed together), never in
// a split state.
type RefundCommit struct {
	// OrderID is the order to flip to Refunded.
	OrderID OrderID
	// ProviderRefundID is the opaque provider Refund reference ("re_...") returned by
	// CreateRefund. Empty for DISPUTE reason — the chargeback already moved
	// funds back to the cardholder and we do not issue a separate Refund call.
	ProviderRefundID string
	// SettlementID is the settlement to flip to Reversed. Empty means no
	// settlement row exists yet (payout sweep hasn't run); in that case no
	// settlement mutation is performed but the order and tickets still flip.
	SettlementID SettlementID
	// ReversedSplits are the settlement splits with their ProviderPayoutReversalID
	// values populated after ReversePayout calls. Ignored when SettlementID
	// is empty. Only splits with a non-empty ProviderPayoutReversalID are updated.
	ReversedSplits []SettlementSplit
}

// RefundRepository provides the atomic DB commit for the refund path.
//
// Interfaces are defined where consumed (AGENTS.md rule).
type RefundRepository interface {
	// CommitRefund atomically applies all DB mutations that constitute a
	// completed refund:
	//
	//  1. UPDATE settlements SET status = Reversed (if SettlementID is set).
	//  2. UPDATE settlement_splits SET provider_payout_reversal_id (for each split).
	//  3. UPDATE tickets SET status = Voided for all tickets of the Order.
	//  4. UPDATE orders SET status = Refunded, provider_refund_id = ProviderRefundID.
	//
	// All mutations are committed in a single pgx transaction so a crash between
	// Stripe and DB is recoverable: on retry, CreateRefund and ReversePayout
	// replay idempotently, and CommitRefund re-attempts the DB commit.
	//
	// The settlement guard (status IN (1, 2)) means a Reversed settlement
	// absorbs a concurrent second call: the guarded UPDATE affects 0 rows, which
	// CommitRefund surfaces as FailedPrecondition so the use case can treat it as
	// an idempotent no-op.
	//
	// # Possible errors
	//
	//  - FailedPrecondition: settlement is already Reversed (concurrent refund).
	//  - Internal: database transaction or query failure.
	CommitRefund(ctx context.Context, commit RefundCommit) error
}

// IssuanceRepository is the atomic write path for ⑤ issuance: it persists an
// Order, its N tickets and the Order's Held Settlement in a single
// transaction, and completes a source Checkout,
// so a capture never yields an Order without its tickets or its payout record
// (backend#468). Implementations live in internal/infrastructure/database/rdb/.
//
// Interfaces are defined where consumed (AGENTS.md rule).
type IssuanceRepository interface {
	// Issue atomically inserts the Order, its N account-bound tickets and the
	// Held settlement (with its splits) that pays out the Order's event
	// Organizer, and makes a source Checkout Completed, all in one
	// transaction. One Order per source is enforced by
	// unique indexes on orders.lottery_entry_id and orders.checkout_id; a
	// duplicate surfaces as AlreadyExists so a replay re-reads the existing
	// Order rather than double-issuing.
	//
	// # Possible errors
	//
	//  - AlreadyExists: an Order already exists for the same source (the caller
	//    re-reads it).
	//  - FailedPrecondition: the source Checkout is not Committed with a
	//    capture time; nothing is stored.
	//  - InvalidArgument: the Order does not name exactly one source.
	//  - Internal: database transaction or query failure.
	Issue(ctx context.Context, order *Order, tickets []*Ticket, settlement *Settlement) error

	// ListLotteryEntryIDsAwaitingIssuance returns the id of every Won lottery
	// entry that has no Order yet, and no other id — the work-list the
	// issuance sweeper processes. An empty result returns (nil, nil).
	//
	// # Possible errors
	//
	//  - Internal: database query failure.
	ListLotteryEntryIDsAwaitingIssuance(ctx context.Context) ([]LotteryEntryID, error)
}

// OrderRepository defines the read/status-update contract for [Order] records.
// The atomic create path is [IssuanceRepository.Issue]. Implementations live in
// internal/infrastructure/database/rdb/.
//
// Interfaces are defined where consumed (AGENTS.md rule).
type OrderRepository interface {
	// Get returns the Order with the given id.
	//
	// # Possible errors
	//
	//  - NotFound: no Order with the id exists.
	//  - Internal: database query failure.
	Get(ctx context.Context, id OrderID) (*Order, error)

	// GetByLotteryEntryID returns the Order created from the given entry. It
	// is the idempotency lookup the issuance path uses to detect an
	// already-issued entry.
	//
	// # Possible errors
	//
	//  - NotFound: the entry has no Order (issuance has not run).
	//  - Internal: database query failure.
	GetByLotteryEntryID(ctx context.Context, lotteryEntryID LotteryEntryID) (*Order, error)

	// GetByCheckoutID returns the Order whose source is the given Checkout.
	//
	// # Possible errors
	//
	//  - NotFound: the Checkout has no Order.
	//  - Internal: database query failure.
	GetByCheckoutID(ctx context.Context, checkoutID CheckoutID) (*Order, error)

	// GetByProviderPaymentID returns the Order whose Payment.ProviderPaymentID
	// matches the given pi_ value. Used by the Stripe webhook service to resolve
	// a dispute's charge → payment_intent → Order without exposing repo access
	// to the HTTP handler layer.
	//
	// # Possible errors
	//
	//  - NotFound: no Order exists for the given payment intent.
	//  - Internal: database query failure.
	GetByProviderPaymentID(ctx context.Context, providerPaymentID string) (*Order, error)

	// ListByUser returns every Order whose user is the given User, in any
	// status. It returns an empty slice when the User bought nothing.
	//
	// # Possible errors
	//
	//  - Internal: database query failure.
	ListByUser(ctx context.Context, userID UserID) ([]*Order, error)

	// MarkConfirmationSent sets the Order's confirmation-sent time when it is
	// unset; an already set time is kept.
	//
	// # Possible errors
	//
	//  - NotFound: no Order with the id exists.
	//  - Internal: database execution failure.
	MarkConfirmationSent(ctx context.Context, id OrderID, at time.Time) error
}

// CapturedPayment is the read-back of ④'s captured winning payment from the
// provider: the authoritative amount/currency actually captured plus the
// display-only card facets. It NEVER carries raw card data (no PAN/CVC/expiry).
// Returned by [PaymentCapturePort.GetCapturedPayment].
type CapturedPayment struct {
	// Provider backs the captured payment (Stripe for the MVP).
	Provider PaymentProvider
	// ProviderPaymentID is the provider's reference of the charged payment
	// (a Stripe "pi_..." id).
	ProviderPaymentID string
	// AmountJPY is the captured amount in whole yen.
	AmountJPY int64
	// Currency is the ISO 4217 code of the captured amount (JPY for the MVP).
	Currency string
	// CardBrand is a display-only brand facet (e.g. "visa"). May be empty.
	CardBrand string
	// CardLast4 is the display-only last four digits. May be empty.
	CardLast4 string
}

// OrderConfirmationEmail is the finished purchase confirmation message for one
// Order, already rendered in the language of the Order's user.
type OrderConfirmationEmail struct {
	// Subject is the email subject line.
	Subject string
	// TextBody is the plain-text email body.
	TextBody string
}

// OrderConfirmationSender sends an Order's purchase confirmation email at most
// once. The implementation lives in internal/infrastructure/mail/.
//
// Interfaces are defined where consumed (AGENTS.md rule).
type OrderConfirmationSender interface {
	// SendConfirmationEmail sends msg to the address of the Order's user when
	// the Order has no confirmation-sent time, then sets that time. When the
	// time is already set it sends nothing and succeeds. A failure between sending and
	// recording can lead to a second email on retry; no other case sends
	// twice.
	//
	// # Possible errors
	//
	//  - InvalidArgument: the address or the message is empty.
	//  - Unavailable: email cannot be sent; the confirmation-sent time stays
	//    unset.
	//  - NotFound: no Order has the id.
	SendConfirmationEmail(ctx context.Context, orderID OrderID, to string, msg OrderConfirmationEmail) error
}
