package entity

import (
	"context"
	"time"
)

// OrderID is the opaque identifier for a single Order — one winning lottery
// application's purchase record.
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
	// cancellation, a postponement holder-initiated refund, or dispute). ⑤ owns
	// the refund policy and sets this status; the Refund + transfer_reversal money
	// movement is executed by ticket-settlement-and-payout.
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
	// PaymentIntentRef is the provider's PaymentIntent reference (e.g. a Stripe
	// "pi_..." id) for ④'s captured manual-capture authorization. Opaque; ⑤
	// references but never re-charges it.
	PaymentIntentRef string
	// PaymentMethodRef is the provider's PaymentMethod reference (e.g. a Stripe
	// "pm_..." id) when distinct from the PaymentIntent. Optional, opaque.
	PaymentMethodRef string
	// CardBrand is a display-only card brand facet (e.g. "visa"). Optional.
	CardBrand string
	// CardLast4 is the display-only last four digits of the card. Optional; this
	// is NOT the PAN.
	CardLast4 string
}

// Order is a provider- and method-agnostic purchase record for one winning
// lottery application: the opaque reference to ④'s captured winning payment,
// its own status, total amount and currency, the capture time, and display
// facets. ONE Order covers the N tickets of the winning application.
//
// It NEVER stores a PAN, CVC, or expiry — only opaque provider tokens and safe
// display facets (PCI SAQ A). Mirrors liverty_music.entity.v1.Order.
type Order struct {
	// ID is the surrogate primary key (UUIDv7).
	ID OrderID
	// BuyerID is the winning applicant's account. The issued tickets are bound
	// to this account.
	BuyerID UserID
	// ApplicationID is the winning application this Order derives from (④'s
	// Won-captured record). The one-Order-per-captured-application invariant
	// (issuance idempotency) is keyed on this reference.
	ApplicationID TicketApplicationID
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
	// PaidTime is when the payment was captured (= ④'s capture time at the draw).
	// Because the Order is created already paid, this is effectively its creation
	// time.
	PaidTime time.Time
}

// IssuanceRepository is the atomic write path for ⑤ issuance: it persists an
// Order and its N tickets in a single transaction so a capture never yields an
// Order without its tickets (or vice versa). Implementations live in
// internal/infrastructure/database/rdb/.
//
// Interfaces are defined where consumed (AGENTS.md rule).
type IssuanceRepository interface {
	// Issue atomically inserts the Order and its N account-bound tickets in one
	// transaction. The one-Order-per-application invariant is enforced by a
	// unique index on orders.application_id; a duplicate surfaces as AlreadyExists
	// so a replayed Won-captured signal re-reads the existing Order rather than
	// double-issuing.
	//
	// # Possible errors
	//
	//  - AlreadyExists: an Order already exists for order.ApplicationID (idempotent
	//    replay — the caller re-reads via [OrderRepository.GetByApplicationID]).
	//  - Internal: database transaction or query failure.
	Issue(ctx context.Context, order *Order, tickets []*Ticket) error

	// ListApplicationIDsAwaitingIssuance returns the IDs of Won-captured
	// applications that do not yet have an Order — the work-list the issuance
	// sweeper processes (mirrors the draw sweeper's due-phase scan). An empty
	// result returns (nil, nil).
	//
	// # Possible errors
	//
	//  - Internal: database query failure.
	ListApplicationIDsAwaitingIssuance(ctx context.Context) ([]TicketApplicationID, error)
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

	// GetByApplicationID returns the Order created for the given winning
	// application, if any. It is the idempotency lookup the issuance path uses to
	// detect an already-issued application.
	//
	// # Possible errors
	//
	//  - NotFound: no Order exists for the application (issuance has not run).
	//  - Internal: database query failure.
	GetByApplicationID(ctx context.Context, applicationID TicketApplicationID) (*Order, error)

	// UpdateStatus changes the status of the Order identified by id (e.g. to
	// Refunded on a cancellation refund, or Failed on the issuance-refund edge).
	//
	// # Possible errors
	//
	//  - NotFound: no Order with the id exists.
	//  - Internal: database execution failure.
	UpdateStatus(ctx context.Context, id OrderID, status OrderStatus) error
}
