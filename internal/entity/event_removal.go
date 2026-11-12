package entity

import (
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
)

// BlocksEventRemoval reports whether the Order keeps its event from being
// removed: a Paid Order, or a Pending one, a purchase in progress. A Refunded
// or Void Order does not; a Void Order's card authorization is released by its
// own workflow.
func (o *Order) BlocksEventRemoval() bool {
	return o.Status == OrderStatusPaid || o.Status == OrderStatusPending
}

// BlocksEventRemoval reports whether the Settlement keeps its event from being
// removed: it is not Reversed.
func (s *Settlement) BlocksEventRemoval() bool {
	return s.Status != SettlementStatusReversed
}

// EventRemovalBlockers is what keeps events from being removed, as a read of
// their purchase records finds it.
type EventRemovalBlockers struct {
	// PaidOrder is true when an event has a Paid Order.
	PaidOrder bool
	// UnreversedSettlement is true when an event has a Settlement that is not
	// Reversed.
	UnreversedSettlement bool
	// PurchaseInProgress is true when an event has a Pending Order.
	PurchaseInProgress bool
}

// Check fails with FailedPrecondition for the first blocker, in the order a
// Paid Order, a Settlement that is not Reversed, a purchase in progress.
func (b EventRemovalBlockers) Check() error {
	switch {
	case b.PaidOrder:
		return apperr.New(codes.FailedPrecondition, "an event has a paid order")
	case b.UnreversedSettlement:
		return apperr.New(codes.FailedPrecondition, "an event has a settlement that is not reversed")
	case b.PurchaseInProgress:
		return apperr.New(codes.FailedPrecondition, "an event has a purchase in progress")
	}
	return nil
}
