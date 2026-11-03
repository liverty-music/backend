package usecase

import (
	"time"

	"github.com/liverty-music/backend/internal/entity"
)

const (
	// WorkflowExpireCheckout is the stable name of the workflow that ends a
	// Held Checkout's hold at its expiry.
	WorkflowExpireCheckout = "expire-checkout"

	// expireWorkflowDeadline is how long Expire tries after the hold expiry
	// before it stops and an admin is told.
	expireWorkflowDeadline = 24 * time.Hour
)

// ExpireWorkflowID names the expiry of a Held Checkout: one per Checkout, so
// a repeated announcement starts nothing new.
func ExpireWorkflowID(checkoutID entity.CheckoutID) string {
	return "expire:" + string(checkoutID)
}

// ExpireRequest is the input of [CheckoutUseCase.Expire]. The tags pin the
// recorded names, which a resumed run reads.
type ExpireRequest struct {
	// CheckoutID is the Held Checkout whose hold ends.
	CheckoutID entity.CheckoutID `json:"checkout_id"`
	// HoldExpireTime is the Checkout's hold expiry, the time the release is
	// judged at.
	HoldExpireTime time.Time `json:"hold_expire_time"`
}
