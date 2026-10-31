package entity

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// OrderID is the opaque identifier for a single Order: one fan's application
// for tickets of one TicketStock and, once paid, its purchase record.
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

// OrderStatus is the lifecycle of an Order. Values mirror the proto enum
// liverty_music.entity.v1.OrderStatus.
//
//	Pending --> Paid --> Refunded
//	Pending --> Void
type OrderStatus int16

const (
	// OrderStatusUnspecified is the zero value and is never persisted.
	OrderStatusUnspecified OrderStatus = 0
	// OrderStatusPaid means the payment was captured (held on the platform
	// balance) and the Order's Tickets are issued.
	OrderStatusPaid OrderStatus = 1
	// OrderStatusRefunded means the captured payment was refunded (event
	// cancellation or dispute). ⑤ owns the refund policy and sets this status;
	// the Refund + transfer_reversal money movement is executed by
	// ticket-settlement-and-payout.
	OrderStatusRefunded OrderStatus = 2
	// OrderStatusPending means the fan applied through a LotteryEntry or a
	// Checkout and the payment is not captured yet. Value 3 was Failed and is
	// unused.
	OrderStatusPending OrderStatus = 4
	// OrderStatusVoid means the Order will never be paid: its lottery entry
	// lost, or its checkout expired or was released. It holds no tickets.
	OrderStatusVoid OrderStatus = 5
)

// String returns the lowercase status name.
func (s OrderStatus) String() string {
	switch s {
	case OrderStatusPaid:
		return "paid"
	case OrderStatusRefunded:
		return "refunded"
	case OrderStatusPending:
		return "pending"
	case OrderStatusVoid:
		return "void"
	default:
		return "UNSPECIFIED"
	}
}

// IsValid reports whether s is a recognized, persistable status.
func (s OrderStatus) IsValid() bool {
	switch s {
	case OrderStatusPaid, OrderStatusRefunded, OrderStatusPending, OrderStatusVoid:
		return true
	default:
		return false
	}
}

// Payment is which provider holds an Order's payment and, once the Order is
// Paid, the opaque reference to the captured payment, plus the display-only
// facets safe to retain. It holds only tokens
// meaningful to the provider and NEVER raw card data (no PAN, CVC, or expiry).
// Mirrors liverty_music.entity.v1.Payment.
type Payment struct {
	// Provider backs the captured payment; drives which adapter interprets the
	// opaque references below.
	Provider PaymentProvider
	// ProviderPaymentID is the payment provider's ID for the captured payment
	// (for Stripe a "pi_..." id); empty until the Order is Paid. Opaque; it is
	// referenced but never charged again.
	ProviderPaymentID string
	// ProviderPaymentMethodID is the payment provider's ID for the payment method
	// (for Stripe a "pm_..." id). Optional, opaque.
	ProviderPaymentMethodID string
	// CardBrand is a display-only card brand facet (e.g. "visa"). Optional.
	CardBrand string
	// CardLast4 is the display-only last four digits of the card. Optional; this
	// is NOT the PAN.
	CardLast4 string
}

// Order is one fan account's application for tickets of one TicketStock and,
// once its payment is captured, the provider- and method-agnostic purchase
// record. It is created Pending when the fan applies through a LotteryEntry
// or a Checkout, its source, which names it; it becomes Paid when the payment
// is captured and its Tickets are issued, or Void when it will never be paid.
// ONE Order covers all tickets of its application.
//
// It NEVER stores a PAN, CVC, or expiry — only opaque provider tokens and safe
// display facets (PCI SAQ A). Mirrors liverty_music.entity.v1.Order.
type Order struct {
	// ID is the surrogate primary key (UUIDv7).
	ID OrderID
	// UserID is the fan account that applied. The issued tickets are bound to
	// this account.
	UserID UserID
	// TicketStockID is the TicketStock the tickets are of.
	TicketStockID TicketStockID
	// TicketCount is the tickets applied for, a companion group: 1 to the
	// per-account limit of the stock's event.
	TicketCount int
	// ReferralCode is the code of the link the fan came from; nil without one.
	// Set at creation and never changed.
	ReferralCode *ReferralCode
	// CardAuthorizationID is the payment provider's ID for the card
	// authorization on the fan's card for the amount (a Stripe "pi_..." id);
	// empty until the fan authorizes, then set once.
	CardAuthorizationID string
	// LotteryEntryID is the LotteryEntry that names the Order; empty when its
	// source is a Checkout. Read from the entry, never stored on the Order.
	LotteryEntryID LotteryEntryID
	// CheckoutID is the Checkout that names the Order; empty when its source
	// is a LotteryEntry. Read from the checkout, never stored on the Order.
	CheckoutID CheckoutID
	// Payment is the provider and, once Paid, the opaque reference to the
	// captured payment plus safe display facets. Never raw card data.
	Payment Payment
	// Status is the order's own status — Pending on creation.
	Status OrderStatus
	// Amount is the total to pay in the currency's smallest unit: for JPY (no
	// minor unit) the yen total, the seat type's price × the ticket count.
	// Fixed at creation.
	Amount int64
	// Currency is the ISO 4217 currency code of Amount (JPY for the MVP).
	Currency string
	// PayTime is when the payment was captured and the Order became Paid; nil
	// until Paid.
	PayTime *time.Time
	// ProviderRefundID is the payment provider's ID for the refund ("re_...") set when the
	// Order is refunded via CANCELLATION. Empty for orders
	// refunded via DISPUTE (the chargeback reversed the charge at the card
	// network; no Stripe Refund object is created) and for orders not yet
	// refunded.
	ProviderRefundID string
	// ConfirmationSendTime is when the purchase confirmation email was sent;
	// nil until sent. It keeps the email to one per Order.
	ConfirmationSendTime *time.Time
}

// OrderCurrency is the currency of every Order for the MVP.
const OrderCurrency = "JPY"

// OrderPaymentProvider is the payment provider every new Order is paid
// through: the MVP's provider, which holds every CardAuthorization.
const OrderPaymentProvider = PaymentProviderStripe

// NewPendingOrder returns a Pending Order of the user for count tickets of the
// stock, with a generated UUIDv7 id, the amount of the stock's SeatType for
// the count, the referral code and the payment provider, and no payment yet.
// stock must carry its SeatType, as every TicketStock read does. The
// LotteryEntry or Checkout that is stored with it names it.
func NewPendingOrder(userID UserID, stock *TicketStock, count int, referralCode *ReferralCode, provider PaymentProvider) *Order {
	return &Order{
		ID:            OrderID(NewID()),
		UserID:        userID,
		TicketStockID: stock.ID,
		TicketCount:   count,
		ReferralCode:  referralCode,
		Payment:       Payment{Provider: provider},
		Status:        OrderStatusPending,
		Amount:        stock.SeatType.AmountFor(count),
		Currency:      OrderCurrency,
	}
}

// ValidateTicketCount reports whether count is at least 1 and at most the
// per-account limit of the event. The entity layer returns stdlib errors;
// callers wrap them with an apperr code.
func ValidateTicketCount(count int, event *Event) error {
	if count < 1 || count > event.Limit() {
		return fmt.Errorf("ticket count must be 1 to %d", event.Limit())
	}
	return nil
}

// ValidateSource reports whether the Order is applied for through exactly one
// source: a LotteryEntry or a Checkout.
func (o *Order) ValidateSource() error {
	hasEntry, hasCheckout := o.LotteryEntryID != "", o.CheckoutID != ""
	if hasEntry == hasCheckout {
		return errors.New("an order must come from exactly one of a lottery entry and a checkout")
	}
	return nil
}

// CanBePaid reports whether the Order has what a Paid Order needs: its card
// authorization, the provider payment ID and the pay time.
func (o *Order) CanBePaid() bool {
	return o.CardAuthorizationID != "" && o.Payment.ProviderPaymentID != "" && o.PayTime != nil
}

// CanBecomeVoid reports whether the Order may become Void: only a Pending
// Order may, when its source ends without it ever being paid.
func (o *Order) CanBecomeVoid() bool {
	return o.Status == OrderStatusPending
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
	// ProviderRefundID is the payment provider's ID for the refund ("re_...") returned by
	// CreateRefund. Empty for DISPUTE reason — the chargeback already moved
	// funds back to the fan and we do not issue a separate Refund call.
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
	//  3. UPDATE resale_listings SET status = Cancelled for the Listed
	//     listings of the Order's Tickets, and tickets SET status = Voided for
	//     its Issued Tickets; a Ticket Voided by a resale and its Sold listing
	//     stay as they are.
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

// IssuanceRepository is the atomic write path for issuance: it makes a Pending
// Order Paid and stores its N tickets and the Order's Held Settlement in a
// single transaction, and completes a source Checkout, so a capture never
// yields a Paid Order without its tickets or its payout record (backend#468).
// Implementations live in internal/infrastructure/database/rdb/.
//
// Interfaces are defined where consumed (AGENTS.md rule).
type IssuanceRepository interface {
	// Issue makes the Pending Order Paid with its captured payment (provider
	// payment ID, payment method, card brand, card last four and pay time),
	// stores its N account-bound tickets and the Held settlement (with its
	// split) that pays out the Order's event Organizer, and, when the Order's
	// source is a Checkout, makes that Checkout Completed, all in one
	// transaction.
	//
	// # Possible errors
	//
	//  - AlreadyExists: the Order is already Paid or Refunded (the caller
	//    re-reads it).
	//  - FailedPrecondition: the Order is Void, or its source Checkout is not
	//    Committed with a capture time; nothing is stored.
	//  - NotFound: no Order has the id.
	//  - Internal: database transaction or query failure.
	//
	// # Announces
	//
	// ORDER.paid with the Order as Paid, together with the issuance. Nothing when
	// Issue fails, including with AlreadyExists.
	Issue(ctx context.Context, order *Order, tickets []*Ticket, settlement *Settlement) error

	// ListLotteryEntryIDsAwaitingIssuance returns the id of every Won lottery
	// entry whose Order is Pending, and no other id — the work-list the
	// issuance sweeper processes. An empty result returns (nil, nil).
	//
	// # Possible errors
	//
	//  - Internal: database query failure.
	ListLotteryEntryIDsAwaitingIssuance(ctx context.Context) ([]LotteryEntryID, error)
}

// OrderRepository defines the read/status-update contract for [Order] records.
// A Pending Order is stored by the LotteryEntry or Checkout operation that
// stores its source; [IssuanceRepository.Issue] makes it Paid. Implementations live in
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

	// GetByLotteryEntryID returns the Order the given entry names, in any
	// status; a Paid Order tells that the entry has been issued.
	//
	// # Possible errors
	//
	//  - NotFound: no LotteryEntry has the id.
	//  - Internal: database query failure.
	GetByLotteryEntryID(ctx context.Context, lotteryEntryID LotteryEntryID) (*Order, error)

	// GetByCheckoutID returns the Order the given Checkout names, in any
	// status.
	//
	// # Possible errors
	//
	//  - NotFound: no Checkout has the id.
	//  - Internal: database query failure.
	GetByCheckoutID(ctx context.Context, checkoutID CheckoutID) (*Order, error)

	// GetByProviderPaymentID returns the Order whose Payment.ProviderPaymentID
	// matches the given pi_ value. Used by the Stripe webhook service to resolve
	// a dispute's charge → provider payment ID → Order without exposing repo access
	// to the HTTP handler layer.
	//
	// # Possible errors
	//
	//  - NotFound: no Order exists for the given provider payment ID.
	//  - Internal: database query failure.
	GetByProviderPaymentID(ctx context.Context, providerPaymentID string) (*Order, error)

	// ListByUser returns every Paid or Refunded Order whose user is the given
	// User; Pending and Void Orders are not returned. It returns an empty slice
	// when the User bought nothing.
	//
	// # Possible errors
	//
	//  - Internal: database query failure.
	ListByUser(ctx context.Context, userID UserID) ([]*Order, error)

	// ListByEvent returns every Paid or Refunded Order whose TicketStock is of
	// the given Event, oldest pay time first; Pending and Void Orders are not
	// returned. It returns an empty slice when the Event has no such Order or
	// no Event has the id.
	//
	// # Possible errors
	//
	//  - InvalidArgument: no Event is given.
	//  - Internal: database query failure.
	ListByEvent(ctx context.Context, eventID string) ([]*Order, error)

	// ListReferralStatsByEvent counts the Paid Orders whose TicketStock is of
	// the event per referral code, with the Orders without a referral code as
	// one direct group (nil Code); Pending, Void and Refunded Orders are not
	// counted. Only groups with at least one Order are
	// returned, the most Orders first, then by code compared character by
	// character. An event without Paid Orders, or unknown, yields an empty
	// list.
	//
	// # Possible errors
	//
	//  - Internal: database query failure.
	ListReferralStatsByEvent(ctx context.Context, eventID string) ([]ReferralCodeStats, error)

	// GetEventID returns the id of the Event of the Order's TicketStock.
	//
	// # Possible errors
	//
	//  - NotFound: no Order with the id exists.
	//  - Internal: database query failure.
	GetEventID(ctx context.Context, id OrderID) (string, error)

	// MarkConfirmationSent sets the Order's confirmation-sent time when it is
	// unset; an already set time is kept.
	//
	// # Possible errors
	//
	//  - NotFound: no Order with the id exists.
	//  - Internal: database execution failure.
	MarkConfirmationSent(ctx context.Context, id OrderID, at time.Time) error

	// GetHeldForEvent returns what the user holds for the event: the Issued
	// Tickets of the user's Paid Orders for the event plus the ticket counts
	// of the user's Pending Orders for the event that still hold or may still
	// receive tickets (a Checkout holding at `at` or Committed, or a
	// LotteryEntry Entered or Won), and, when that count is above 0, the
	// SeatType of the Orders counted. The Order except names, when given, is
	// not counted: it is the Order of the user's own holding Checkout that the
	// new application resumes or replaces. Void and Refunded Orders, Pending
	// Orders whose Checkout's hold has lapsed and Voided Tickets are not
	// counted.
	//
	// # Possible errors
	//
	//  - Internal: database query failure.
	GetHeldForEvent(ctx context.Context, userID UserID, eventID string, except *OrderID, at time.Time) (HeldTickets, error)

	// CreateRefund refunds params.Amount of the captured Charge to the fan for
	// the purpose params names (the Order's own refund, or one ResaleListing
	// of the Order) and returns the payment provider's ID for the refund
	// ("re_..."). A repeated call for the same Order and purpose refunds only
	// once and returns the same ID; refunds for different purposes are
	// separate refunds, each of part of the charge.
	//
	// # Possible errors
	//
	//  - InvalidArgument: amount is zero or negative.
	//  - FailedPrecondition: the charge has already been fully refunded, or
	//    the amount exceeds what is left of it.
	//  - Unavailable: the payment provider is unreachable.
	CreateRefund(ctx context.Context, params RefundParams) (providerRefundID string, err error)

	// GetProviderChargeID retrieves the payment identified by
	// providerPaymentID and returns the charge id ("ch_...") of its captured
	// charge. The charge id is used as source_transaction on the Transfer.
	//
	// # Possible errors
	//
	//  - FailedPrecondition: the payment is not captured.
	//  - NotFound: the payment does not exist.
	//  - Unavailable: the payment provider is unreachable.
	GetProviderChargeID(ctx context.Context, providerPaymentID string) (providerChargeID string, err error)
}

// CapturedPayment is the read-back of ④'s captured winning payment from the
// provider: the authoritative amount/currency actually captured plus the
// display-only card facets. It NEVER carries raw card data (no PAN/CVC/expiry).
// Returned by [CardAuthorizationRepository.Get] for a Captured card
// authorization.
type CapturedPayment struct {
	// Provider backs the captured payment (Stripe for the MVP).
	Provider PaymentProvider
	// ProviderPaymentID is the payment provider's ID for the charged payment
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
