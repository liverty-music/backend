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
// holds it and is its only record, with the purchase it pays for; the
// purchase's Order refers to it by its id.
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
	// Purchase is what the card authorization pays for, fixed at creation.
	Purchase CardAuthorizationPurchase
}

// CardAuthorizationPurpose is the kind of purchase a CardAuthorization pays
// for.
type CardAuthorizationPurpose int

const (
	// CardAuthorizationPurposeUnspecified is a card authorization the platform
	// did not open for a purchase it knows.
	CardAuthorizationPurposeUnspecified CardAuthorizationPurpose = iota
	// CardAuthorizationPurposeCheckout pays for a first-come Checkout.
	CardAuthorizationPurposeCheckout
	// CardAuthorizationPurposeLotteryEntry pays for a lottery entry, which is
	// made from it at Confirm.
	CardAuthorizationPurposeLotteryEntry
)

// CardAuthorizationPurchase is the purchase a CardAuthorization pays for: a
// Checkout, or a lottery entry of a fan in a Lottery TicketSale for a ticket
// count of a TicketStock, with the stock's event. It carries what the lottery
// entry and its Order are made from at Confirm. It never carries personal
// data.
type CardAuthorizationPurchase struct {
	// Purpose is the kind of purchase.
	Purpose CardAuthorizationPurpose
	// CheckoutID is the checkout; set only for a Checkout.
	CheckoutID CheckoutID
	// UserID is the fan who pays.
	UserID UserID
	// TicketSaleID is the Lottery sale a lottery entry is made for; empty for
	// a Checkout.
	TicketSaleID TicketSaleID
	// TicketStockID is the purchase's TicketStock.
	TicketStockID TicketStockID
	// EventID is the TicketStock's event.
	EventID string
	// TicketCount is the number of tickets paid for.
	TicketCount int
	// ReferralCode is the code of the link the fan came from; set only for a
	// lottery entry, and nil without one.
	ReferralCode *ReferralCode
}

// IsLotteryEntry reports whether the purchase is a lottery entry.
func (p CardAuthorizationPurchase) IsLotteryEntry() bool {
	return p.Purpose == CardAuthorizationPurposeLotteryEntry
}

// AcceptsCardBrand reports whether a card of the brand may pay for the
// purchase: a Checkout accepts every brand, a LotteryEntry every brand but
// American Express ([IsAcceptedCardBrand]); an unknown brand is accepted for
// both.
func (p CardAuthorizationPurchase) AcceptsCardBrand(brand CardBrand) bool {
	return p.Purpose == CardAuthorizationPurposeCheckout || IsAcceptedCardBrand(brand)
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
// idempotent per card authorization, and Create per Checkout.
type CardAuthorizationRepository interface {
	// Create opens a Pending CardAuthorization in yen for amountJPY that
	// carries the purchase, charging nothing until Capture runs, and returns
	// it with its card authorization secret. Repeating it for the same
	// Checkout returns the same CardAuthorization; each call for a lottery
	// entry opens a new one.
	//
	// # Possible errors
	//
	//  - InvalidArgument: amountJPY is zero or negative.
	//  - Unavailable: the payment provider cannot be reached.
	//  - Internal: unexpected provider error.
	Create(ctx context.Context, purchase CardAuthorizationPurchase, amountJPY int64) (*CardAuthorization, error)

	// Get returns the CardAuthorization with its card authorization secret,
	// the purchase it was opened with, its amount, card brand and status as
	// the payment provider reports them now, and, when it is Captured, the
	// captured payment an Order records. It never charges the card.
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
