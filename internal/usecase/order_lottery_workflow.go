package usecase

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"

	"github.com/liverty-music/backend/internal/entity"
)

const (
	// WorkflowIssue is the stable name of the workflow that charges the
	// Order of a Won lottery entry or a placed Checkout and issues it.
	WorkflowIssue = "issue"

	// WorkflowReleaseCardAuthorization is the stable name of the workflow
	// that releases a Void Order's card authorization.
	WorkflowReleaseCardAuthorization = "release-card-authorization"

	// PaymentQueue runs the workflows that call the payment provider. Its
	// global concurrency is set where it is registered, in the workflow
	// worker.
	PaymentQueue = "payment"

	// PaymentQueueConcurrency is how many workflows of [PaymentQueue] run at
	// once: each step holds at most one of the worker's 5 database
	// connections, so 2 leave room for DBOS and the draw.
	PaymentQueueConcurrency = 2

	// CheckoutQueue runs the issue of a placed Checkout, apart from
	// [PaymentQueue], so a fan waiting on the screen never waits behind a
	// large lottery's issues.
	CheckoutQueue = "checkout"

	// CheckoutQueueConcurrency is how many workflows of [CheckoutQueue] run at
	// once, for the same reason as [PaymentQueueConcurrency].
	CheckoutQueueConcurrency = 2

	// paymentWorkflowDeadline is how long Issue and ReleaseCardAuthorization
	// try before they stop and an admin is told. A JPY card authorization in
	// Japan holds for up to 30 days, so a resumed capture still finds it.
	paymentWorkflowDeadline = 24 * time.Hour
)

// IssueWorkflowID names the issue of an Order: one per Order, so a repeated
// announcement or a second Place starts nothing new. Each Order has exactly
// one source, so the two sources never share an id.
func IssueWorkflowID(orderID entity.OrderID) string {
	return "issue:" + string(orderID)
}

// ReleaseWorkflowID names the release of a Void Order's card authorization.
func ReleaseWorkflowID(orderID entity.OrderID) string {
	return "release:" + string(orderID)
}

// IssueRequest is the input of [OrderUseCase.Issue]: the Order's source,
// exactly one of a Won entry and a placed Checkout. The tags pin the recorded
// names, which a resumed run reads.
type IssueRequest struct {
	// LotteryEntryID is the Won entry whose Order is issued.
	LotteryEntryID entity.LotteryEntryID `json:"lottery_entry_id,omitempty"`
	// CheckoutID is the placed Checkout whose Order is issued.
	CheckoutID entity.CheckoutID `json:"checkout_id,omitempty"`
	// ResaleEntryID is the Won resale entry whose Order is issued.
	ResaleEntryID entity.ResaleEntryID `json:"resale_entry_id,omitempty"`
}

// IssueResult is the outcome of [OrderUseCase.Issue].
type IssueResult struct {
	// OrderID is the source's Order.
	OrderID entity.OrderID `json:"order_id"`
	// Paid is true when the Order is Paid. It is false when the card
	// authorization could no longer be charged and the win or the commit was
	// reverted, the Order Void, and when a Checkout's hold was over at the
	// commit, the Order left Pending until the hold's expiry makes it Void.
	Paid bool `json:"paid"`
}

// issuePlan is what Issue reads before the charge, as its step records it.
type issuePlan struct {
	OrderID                entity.OrderID `json:"order_id"`
	Paid                   bool           `json:"paid"`
	CardAuthorizationID    string         `json:"card_authorization_id"`
	EventID                string         `json:"event_id"`
	OrganizerID            string         `json:"organizer_id"`
	PlatformFeeRateBps     int            `json:"platform_fee_rate_bps"`
	IdentityVerificationID string         `json:"identity_verification_id"`
}

// capturedFacets are the facets of the captured payment the Order records,
// as the step that reads them records them.
type capturedFacets struct {
	Provider  entity.PaymentProvider `json:"provider"`
	CardBrand string                 `json:"card_brand"`
	CardLast4 string                 `json:"card_last4"`
	// ProviderPaymentID is the payment provider's ID for the captured
	// payment. A run recorded before it was added has none; the issue step
	// then reads it again.
	ProviderPaymentID string `json:"provider_payment_id,omitempty"`
}

// StartDrawnEntryWorkflow starts the work that follows one entry's draw
// result: [OrderUseCase.Issue] for a Won entry's Order and
// [OrderUseCase.ReleaseCardAuthorization] for a Lost entry's Order, on
// [PaymentQueue] with a 24-hour deadline, each named after the Order so a
// repeated announcement starts nothing new. An entry in any other state
// starts nothing.
//
// # Possible errors
//
//   - Errors of starting the workflow, unchanged.
func (uc *orderUseCase) StartDrawnEntryWorkflow(ctx context.Context, entry *entity.LotteryEntry) error {
	opts := []StartOption{WithQueue(PaymentQueue), WithDeadline(uc.clock().Add(paymentWorkflowDeadline))}
	switch entry.State {
	case entity.LotteryEntryStateWon:
		return uc.workflows.Start(ctx, WorkflowIssue, IssueWorkflowID(entry.OrderID), IssueRequest{LotteryEntryID: entry.ID}, opts...)
	case entity.LotteryEntryStateLost:
		return uc.workflows.Start(ctx, WorkflowReleaseCardAuthorization, ReleaseWorkflowID(entry.OrderID), entry.OrderID, opts...)
	default:
		return nil
	}
}

// StartResaleEntryWorkflow starts the work that follows a resale entry's
// change: [OrderUseCase.Issue] for a Won entry's Order and
// [OrderUseCase.ReleaseCardAuthorization] for the Order of a Lapsed,
// Withdrawn or Lost entry, on [PaymentQueue] with a 24-hour deadline, each
// named after the Order so a repeated announcement starts nothing new. A
// Lost entry whose payment was not completed before the show was released by
// Issue already, and one whose listing was cancelled after the charge was
// charged and refunded; neither starts anything, as an Entered entry does
// not.
//
// # Possible errors
//
//   - Errors of starting the workflow, unchanged.
func (uc *orderUseCase) StartResaleEntryWorkflow(ctx context.Context, entry *entity.ResaleEntry) error {
	opts := []StartOption{WithQueue(PaymentQueue), WithDeadline(uc.clock().Add(paymentWorkflowDeadline))}
	switch {
	case entry.State == entity.ResaleEntryStateWon:
		return uc.workflows.Start(ctx, WorkflowIssue, IssueWorkflowID(entry.OrderID), IssueRequest{ResaleEntryID: entry.ID}, opts...)
	case entry.State == entity.ResaleEntryStateLost && entry.LostReason == entity.ResaleEntryLostReasonPaymentNotCompletedBeforeShow,
		entry.State == entity.ResaleEntryStateLost && entry.LostReason == entity.ResaleEntryLostReasonListingCancelled:
		return nil
	case entry.State == entity.ResaleEntryStateLapsed, entry.State == entity.ResaleEntryStateLost, entry.State == entity.ResaleEntryStateWithdrawn:
		return uc.workflows.Start(ctx, WorkflowReleaseCardAuthorization, ReleaseWorkflowID(entry.OrderID), entry.OrderID, opts...)
	default:
		return nil
	}
}

// Issue implements [OrderUseCase].
func (uc *orderUseCase) Issue(ctx context.Context, f Flow, in IssueRequest) (IssueResult, error) {
	sources := 0
	for _, named := range []bool{in.LotteryEntryID != "", in.CheckoutID != "", in.ResaleEntryID != ""} {
		if named {
			sources++
		}
	}
	switch {
	case sources != 1:
		return IssueResult{}, apperr.New(codes.InvalidArgument, "an issue names exactly one source")
	case in.CheckoutID != "":
		return uc.issueCheckout(ctx, f, in.CheckoutID)
	case in.ResaleEntryID != "":
		return uc.issueResaleEntry(ctx, f, in.ResaleEntryID)
	default:
		return uc.issueEntry(ctx, f, in.LotteryEntryID)
	}
}

// issueEntry charges a Won entry's Order and issues it.
func (uc *orderUseCase) issueEntry(ctx context.Context, f Flow, entryID entity.LotteryEntryID) (IssueResult, error) {
	// Every read that can fail for a data reason runs before the charge, so a
	// fan is never charged and then stopped by a missing record.
	plan, err := Step(ctx, f, "read", func(ctx context.Context) (issuePlan, error) {
		return uc.planIssue(ctx, entryID)
	})
	if err != nil {
		return IssueResult{}, err
	}
	if plan.Paid {
		return IssueResult{OrderID: plan.OrderID, Paid: true}, nil
	}

	outcome, err := uc.captureEntry(ctx, f, plan)
	if err != nil {
		return IssueResult{}, err
	}
	if !outcome.Charged {
		// No promotion of a loser (繰上げ): the tickets stay unallocated.
		reason, why := entity.LotteryEntryLostReasonCardNotCharged, "the card could not be charged"
		if outcome.ConcertCancelled {
			reason, why = entity.LotteryEntryLostReasonConcertCancelled, "the concert was cancelled before the charge"
		}
		if _, err := Step(ctx, f, "revert win", func(ctx context.Context) (struct{}, error) {
			return struct{}{}, uc.entryRepo.RevertWin(ctx, entryID, reason)
		}); err != nil {
			return IssueResult{}, err
		}
		uc.logger.Info(ctx, "lottery win reverted: "+why,
			slog.String("lottery_entry_id", string(entryID)),
			slog.String("order_id", string(plan.OrderID)))
		return IssueResult{OrderID: plan.OrderID}, nil
	}

	result, _, err := uc.issueCaptured(ctx, f, plan)
	return result, err
}

// entryCapture is the outcome of the charge of a Won lottery entry, as its
// step records it.
type entryCapture struct {
	// Charged is true when the card authorization was captured.
	Charged bool `json:"charged"`
	// ConcertCancelled is true when the event was no longer PUBLISHED before
	// the charge, so nothing was charged.
	ConcertCancelled bool `json:"concert_cancelled,omitempty"`
}

// captureEntry charges the Order of a Won lottery entry in one step, which is
// retried while the payment provider cannot be reached. Each try first checks
// that the event is still PUBLISHED, so a concert cancelled during an outage
// is not charged; the check is inside the step, so a resumed run does not
// check again after a capture that happened. For a cancelled concert it reads
// the card authorization first, since an earlier try that ended Unavailable
// may have captured it: a captured one goes on as charged, and the
// cancellation refund reaches its Paid Order. A card authorization that can
// no longer be charged is a result, neither charged nor cancelled.
//
// It does not share [orderUseCase.capture] with the issue of a placed
// Checkout, whose recorded step stays as it was.
func (uc *orderUseCase) captureEntry(ctx context.Context, f Flow, plan issuePlan) (entryCapture, error) {
	return Step(ctx, f, "capture", func(ctx context.Context) (entryCapture, error) {
		published, err := uc.eventState.IsEventPublished(ctx, plan.EventID)
		if err != nil {
			return entryCapture{}, err
		}
		if !published {
			authorization, err := uc.cardAuthorizations.Get(ctx, plan.CardAuthorizationID)
			if err != nil {
				return entryCapture{}, err
			}
			if authorization.CountsAsCharged(published) {
				return entryCapture{Charged: true}, nil
			}
			return entryCapture{ConcertCancelled: true}, nil
		}
		err = uc.cardAuthorizations.Capture(ctx, plan.CardAuthorizationID)
		if errors.Is(err, apperr.ErrFailedPrecondition) {
			// The card authorization can no longer be charged: a result.
			return entryCapture{}, nil
		}
		return entryCapture{Charged: err == nil}, err
	})
}

// issued is the outcome of the issue step, as it records it.
type issued struct {
	// ListingCancelled is true when a listing allocated to the Won resale
	// entry was Cancelled, so nothing was issued.
	ListingCancelled bool `json:"listing_cancelled,omitempty"`
}

// issueCaptured reads the captured payment and makes the plan's Order Paid
// with Order.Issue, as the steps after the charge of any source. It reports
// listingCancelled, issuing nothing, when a listing allocated to the Order's
// Won resale entry was Cancelled ([entity.ErrResaleListingCancelled]).
func (uc *orderUseCase) issueCaptured(ctx context.Context, f Flow, plan issuePlan) (result IssueResult, listingCancelled bool, err error) {
	facets, err := Step(ctx, f, "read captured payment", func(ctx context.Context) (capturedFacets, error) {
		captured, err := uc.capturedPayment(ctx, plan.CardAuthorizationID)
		if err != nil {
			return capturedFacets{}, err
		}
		return capturedFacets{Provider: captured.Provider, CardBrand: captured.CardBrand, CardLast4: captured.CardLast4,
			ProviderPaymentID: captured.ProviderPaymentID}, nil
	})
	if err != nil {
		return IssueResult{}, false, err
	}

	outcome, err := Step(ctx, f, "issue", func(ctx context.Context) (issued, error) {
		order, err := uc.orderRepo.Get(ctx, plan.OrderID)
		if err != nil {
			return issued{}, err
		}
		if order.IsPaid() {
			return issued{}, nil
		}
		captured := &entity.CapturedPayment{Provider: facets.Provider, CardBrand: facets.CardBrand, CardLast4: facets.CardLast4,
			ProviderPaymentID: facets.ProviderPaymentID}
		if captured.ProviderPaymentID == "" {
			again, err := uc.capturedPayment(ctx, plan.CardAuthorizationID)
			if err != nil {
				return issued{}, err
			}
			captured.ProviderPaymentID = again.ProviderPaymentID
		}
		_, err = uc.issueWith(ctx, order, plan.EventID, plan.OrganizerID, plan.PlatformFeeRateBps, plan.IdentityVerificationID, captured)
		if errors.Is(err, entity.ErrResaleListingCancelled) {
			// A result, not a failure: the caller gives the charge back.
			return issued{ListingCancelled: true}, nil
		}
		return issued{}, err
	})
	if err != nil {
		return IssueResult{}, false, err
	}
	if outcome.ListingCancelled {
		return IssueResult{OrderID: plan.OrderID}, true, nil
	}
	return IssueResult{OrderID: plan.OrderID, Paid: true}, false, nil
}

// planIssue reads the Won entry, its Pending Order, the Order's stock, the
// entry's sale, the event's Organizer and the IdentityVerification binding.
func (uc *orderUseCase) planIssue(ctx context.Context, entryID entity.LotteryEntryID) (issuePlan, error) {
	entry, err := uc.entryRepo.Get(ctx, entryID)
	if err != nil {
		return issuePlan{}, err
	}
	order, err := uc.orderRepo.Get(ctx, entry.OrderID)
	if err != nil {
		return issuePlan{}, err
	}
	if order.IsPaid() {
		return issuePlan{OrderID: order.ID, Paid: true}, nil
	}
	entry.Order = order
	if err := entry.CheckChargeable(); err != nil {
		return issuePlan{}, err
	}
	return uc.planOrder(ctx, order, entry.TicketSaleID)
}

// planOrder reads, for a Pending Order bought from the sale, the Order's
// stock, the sale, the event's Organizer and the IdentityVerification binding.
func (uc *orderUseCase) planOrder(ctx context.Context, order *entity.Order, saleID entity.TicketSaleID) (issuePlan, error) {
	stock, err := uc.stockRepo.Get(ctx, order.TicketStockID, uc.clock())
	if err != nil {
		return issuePlan{}, err
	}
	sale, err := uc.ticketSaleRepo.Get(ctx, saleID)
	if err != nil {
		return issuePlan{}, err
	}
	organizerID, err := uc.eventOrganizerRepo.GetOrganizerID(ctx, stock.EventID)
	if err != nil {
		return issuePlan{}, err
	}
	organizer, err := uc.organizerRepo.Get(ctx, organizerID)
	if err != nil {
		return issuePlan{}, err
	}
	identityVerificationID, err := uc.identityVerificationBinding(ctx, sale, order.UserID)
	if err != nil {
		return issuePlan{}, err
	}
	return issuePlan{
		OrderID:                order.ID,
		CardAuthorizationID:    order.CardAuthorizationID,
		EventID:                stock.EventID,
		OrganizerID:            organizerID,
		PlatformFeeRateBps:     organizer.PlatformFeeRateBps,
		IdentityVerificationID: identityVerificationID,
	}, nil
}

// ReleaseCardAuthorization implements [OrderUseCase].
func (uc *orderUseCase) ReleaseCardAuthorization(ctx context.Context, f Flow, orderID entity.OrderID) (struct{}, error) {
	cardAuthorizationID, err := Step(ctx, f, "get order", func(ctx context.Context) (string, error) {
		order, err := uc.orderRepo.Get(ctx, orderID)
		if err != nil {
			return "", err
		}
		if order.Status != entity.OrderStatusVoid {
			return "", apperr.New(codes.FailedPrecondition, "only a Void Order's card authorization is released",
				slog.String("order_id", string(orderID)), slog.String("order_status", order.Status.String()))
		}
		return order.CardAuthorizationID, nil
	})
	if err != nil || cardAuthorizationID == "" {
		return struct{}{}, err
	}
	return Step(ctx, f, "release", func(ctx context.Context) (struct{}, error) {
		return struct{}{}, uc.cardAuthorizations.Release(ctx, cardAuthorizationID)
	})
}
