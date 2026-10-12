package entity

import (
	"context"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
)

// CardAuthorizationStatus is the lifecycle of a CardAuthorization.
type CardAuthorizationStatus int

const (
	// CardAuthorizationStatusUnspecified is the zero value; never reported.
	CardAuthorizationStatusUnspecified CardAuthorizationStatus = iota
	// CardAuthorizationStatusPending is opened and waits for the fan to
	// complete card authentication.
	CardAuthorizationStatusPending
	// CardAuthorizationStatusAuthorized reserves the amount on the card; it
	// can be captured.
	CardAuthorizationStatusAuthorized
	// CardAuthorizationStatusCaptured charged the card the amount.
	CardAuthorizationStatusCaptured
	// CardAuthorizationStatusReleased gave the amount back without charging,
	// by a release or because it lapsed on the card.
	CardAuthorizationStatusReleased
)

// CardAuthorization is the authorization placed on a fan's card for the
// amount of one purchase, a LotteryEntry or a Checkout. The payment provider
// holds it and is its only record; a LotteryEntry or a Checkout refers to it by
// its id.
type CardAuthorization struct {
	// ID is the payment provider's ID for the card authorization (a Stripe
	// "pi_..."), 1 to 255 characters.
	ID string
	// Amount is the amount authorized on the card, in whole yen.
	Amount int64
	// CardBrand is the brand of the card the fan authorized; empty until the
	// fan completes card authentication or when the provider does not report
	// it.
	CardBrand CardBrand
	// Status is the card authorization's lifecycle status.
	Status CardAuthorizationStatus
	// Secret is the card authorization secret the fan's browser completes card
	// authentication with. It is never stored.
	Secret string
	// CapturedPayment is the payment an Order records; set only when Status is
	// Captured.
	CapturedPayment *CapturedPayment
}

// CardAuthorizationPurchase is the purchase a CardAuthorization pays for:
// exactly one of a LotteryEntry and a Checkout, with the TicketType and event
// it buys. It never carries personal data.
type CardAuthorizationPurchase struct {
	// LotteryEntryID is the lottery entry, when that is the purchase.
	LotteryEntryID LotteryEntryID
	// CheckoutID is the checkout, when that is the purchase.
	CheckoutID CheckoutID
	// TicketTypeID is the purchase's TicketType.
	TicketTypeID TicketTypeID
	// EventID is the TicketType's event.
	EventID string
}

// ID returns the id of the purchase: the checkout's or the lottery entry's.
func (p CardAuthorizationPurchase) ID() string {
	if p.CheckoutID != "" {
		return string(p.CheckoutID)
	}
	return string(p.LotteryEntryID)
}

// AcceptsCardBrand reports whether a card of the brand may pay for the
// purchase: a Checkout accepts every brand, a LotteryEntry every brand but
// American Express ([IsAcceptedCardBrand]); an unknown brand is accepted for
// both.
func (p CardAuthorizationPurchase) AcceptsCardBrand(brand CardBrand) bool {
	return p.CheckoutID != "" || IsAcceptedCardBrand(brand)
}

// Confirm returns nil when the card authorization confirms the purchase: it is
// Authorized, for exactly expectedAmount yen, on a card brand the purchase
// accepts.
//
// # Possible errors
//
//   - FailedPrecondition: the card authorization is not Authorized (card
//     authentication is not completed, or it was captured or released), or the
//     card brand is not accepted for the purchase.
//   - InvalidArgument: its amount differs from expectedAmount.
func (a *CardAuthorization) Confirm(purchase CardAuthorizationPurchase, expectedAmount int64) error {
	if a.Status != CardAuthorizationStatusAuthorized {
		return apperr.New(codes.FailedPrecondition,
			"the card authorization is not authorized; card authentication must complete first")
	}
	if a.Amount != expectedAmount {
		return apperr.New(codes.InvalidArgument, "the card authorization's amount differs from the purchase's amount")
	}
	if !purchase.AcceptsCardBrand(a.CardBrand) {
		return apperr.New(codes.FailedPrecondition, "American Express cards are not accepted for lottery entries")
	}
	return nil
}

// CardAuthorizationRepository opens, reads, captures and releases the
// CardAuthorizations held by the payment provider. Each operation is
// idempotent per card authorization (and Create per purchase).
type CardAuthorizationRepository interface {
	// Create opens a Pending CardAuthorization in yen for amountJPY for the
	// purchase, charging nothing until Capture runs, and returns it with its
	// card authorization secret. Repeating it for the same purchase returns
	// the same CardAuthorization.
	//
	// # Possible errors
	//
	//  - InvalidArgument: amountJPY is zero or negative.
	//  - Unavailable: the payment provider cannot be reached.
	//  - Internal: unexpected provider error.
	Create(ctx context.Context, purchase CardAuthorizationPurchase, amountJPY int64) (*CardAuthorization, error)

	// Get returns the CardAuthorization with its card authorization secret,
	// amount, card brand and status as the payment provider reports them now,
	// and, when it is Captured, the captured payment an Order records. It never
	// charges the card.
	//
	// # Possible errors
	//
	//  - NotFound: no CardAuthorization has the id.
	//  - InvalidArgument: it is not in yen.
	//  - Unavailable: the payment provider cannot be reached.
	Get(ctx context.Context, id string) (*CardAuthorization, error)

	// Capture charges the card the CardAuthorization's amount, exactly once
	// however often and however late it is repeated; one already Captured
	// succeeds without charging again.
	//
	// # Possible errors
	//
	//  - NotFound: no CardAuthorization has the id.
	//  - FailedPrecondition: it is Pending or Released, or the card can no
	//    longer be charged; nothing is charged.
	//  - Unavailable: the outcome is not known yet (the payment provider cannot
	//    be reached, or another capture is in progress).
	Capture(ctx context.Context, id string) error

	// Release gives a Pending or Authorized CardAuthorization back without
	// charging. Repeating it, or releasing one that lapsed on the card,
	// succeeds without a further effect.
	//
	// # Possible errors
	//
	//  - NotFound: no CardAuthorization has the id.
	//  - FailedPrecondition: it was already captured; nothing changes.
	//  - Unavailable: the payment provider cannot be reached.
	Release(ctx context.Context, id string) error
}
