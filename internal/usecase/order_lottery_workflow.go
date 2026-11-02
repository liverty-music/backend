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
	// WorkflowIssue is the stable name of the workflow that charges a Won
	// lottery entry's Order and issues it.
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

	// paymentWorkflowDeadline is how long Issue and ReleaseCardAuthorization
	// try before they stop and an admin is told. A JPY card authorization in
	// Japan holds for up to 30 days, so a resumed capture still finds it.
	paymentWorkflowDeadline = 24 * time.Hour
)

// IssueWorkflowID names the issue of a Won entry after its Order: one per
// Order, so a repeated announcement starts nothing new.
func IssueWorkflowID(orderID entity.OrderID) string {
	return "issue:" + string(orderID)
}

// ReleaseWorkflowID names the release of a Void Order's card authorization.
func ReleaseWorkflowID(orderID entity.OrderID) string {
	return "release:" + string(orderID)
}

// IssueRequest is the input of [OrderUseCase.Issue]. The tags pin the
// recorded names, which a resumed run reads.
type IssueRequest struct {
	// LotteryEntryID is the Won entry whose Order is issued.
	LotteryEntryID entity.LotteryEntryID `json:"lottery_entry_id"`
}

// IssueResult is the outcome of [OrderUseCase.Issue].
type IssueResult struct {
	// OrderID is the entry's Order.
	OrderID entity.OrderID `json:"order_id"`
	// Paid is true when the Order is Paid, and false when its card
	// authorization could no longer be charged and the win was reverted, the
	// Order Void.
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

// Issue implements [OrderUseCase].
func (uc *orderUseCase) Issue(ctx context.Context, f Flow, in IssueRequest) (IssueResult, error) {
	// Every read that can fail for a data reason runs before the charge, so a
	// fan is never charged and then stopped by a missing record.
	plan, err := Step(ctx, f, "read", func(ctx context.Context) (issuePlan, error) {
		return uc.planIssue(ctx, in.LotteryEntryID)
	})
	if err != nil {
		return IssueResult{}, err
	}
	if plan.Paid {
		return IssueResult{OrderID: plan.OrderID, Paid: true}, nil
	}

	charged, err := Step(ctx, f, "capture", func(ctx context.Context) (bool, error) {
		err := uc.cardAuthorizations.Capture(ctx, plan.CardAuthorizationID)
		if errors.Is(err, apperr.ErrFailedPrecondition) {
			// The card authorization can no longer be charged: a result.
			return false, nil
		}
		return err == nil, err
	})
	if err != nil {
		return IssueResult{}, err
	}
	if !charged {
		// No promotion of a loser (繰上げ): the tickets stay unallocated.
		if _, err := Step(ctx, f, "revert win", func(ctx context.Context) (struct{}, error) {
			return struct{}{}, uc.entryRepo.RevertWin(ctx, in.LotteryEntryID)
		}); err != nil {
			return IssueResult{}, err
		}
		uc.logger.Info(ctx, "lottery win reverted: the card could not be charged",
			slog.String("lottery_entry_id", string(in.LotteryEntryID)),
			slog.String("order_id", string(plan.OrderID)))
		return IssueResult{OrderID: plan.OrderID}, nil
	}

	facets, err := Step(ctx, f, "read captured payment", func(ctx context.Context) (capturedFacets, error) {
		captured, err := uc.capturedPayment(ctx, plan.CardAuthorizationID)
		if err != nil {
			return capturedFacets{}, err
		}
		return capturedFacets{Provider: captured.Provider, CardBrand: captured.CardBrand, CardLast4: captured.CardLast4}, nil
	})
	if err != nil {
		return IssueResult{}, err
	}

	if _, err := Step(ctx, f, "issue", func(ctx context.Context) (struct{}, error) {
		order, err := uc.orderRepo.Get(ctx, plan.OrderID)
		if err != nil {
			return struct{}{}, err
		}
		if isPaid(order) {
			return struct{}{}, nil
		}
		captured := &entity.CapturedPayment{Provider: facets.Provider, CardBrand: facets.CardBrand, CardLast4: facets.CardLast4}
		_, err = uc.issueWith(ctx, order, plan.EventID, plan.OrganizerID, plan.PlatformFeeRateBps, plan.IdentityVerificationID, captured)
		return struct{}{}, err
	}); err != nil {
		return IssueResult{}, err
	}
	return IssueResult{OrderID: plan.OrderID, Paid: true}, nil
}

// planIssue reads the Won entry, its Pending Order, the Order's stock, the
// entry's sale, the event's Organizer and the IdentityVerification binding.
func (uc *orderUseCase) planIssue(ctx context.Context, entryID entity.LotteryEntryID) (issuePlan, error) {
	attrs := slog.String("lottery_entry_id", string(entryID))
	entry, err := uc.entryRepo.Get(ctx, entryID)
	if err != nil {
		return issuePlan{}, err
	}
	order, err := uc.orderRepo.Get(ctx, entry.OrderID)
	if err != nil {
		return issuePlan{}, err
	}
	if isPaid(order) {
		return issuePlan{OrderID: order.ID, Paid: true}, nil
	}
	if entry.State != entity.LotteryEntryStateWon {
		return issuePlan{}, apperr.New(codes.FailedPrecondition, "lottery entry is not Won; nothing is charged", attrs)
	}
	if order.Status != entity.OrderStatusPending {
		return issuePlan{}, apperr.New(codes.FailedPrecondition, "the entry's Order is not Pending; nothing is charged",
			attrs, slog.String("order_status", order.Status.String()))
	}
	stock, err := uc.stockRepo.Get(ctx, order.TicketStockID, uc.clock())
	if err != nil {
		return issuePlan{}, err
	}
	sale, err := uc.ticketSaleRepo.Get(ctx, entry.TicketSaleID)
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
