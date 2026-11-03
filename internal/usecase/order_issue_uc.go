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

// EventOrganizerRepository resolves the Organizer that owns an event, via its
// Series, so the issue workflow can stamp the denormalized OrganizerID on the
// Held Settlement it creates alongside the Order (backend#468). A minimal
// read-only interface so OrderUseCase does not depend on the full
// SeriesRepository. Interfaces are defined where consumed (AGENTS.md rule).
type EventOrganizerRepository interface {
	// GetOrganizerID resolves the Organizer that owns the event identified by
	// eventID, via event -> series -> organizer_id.
	//
	// # Possible errors
	//
	//  - NotFound: no event with the given ID exists, or its series is not
	//    organizer-authored (a discovery-pipeline series has no Organizer).
	//  - Internal: database query failure.
	GetOrganizerID(ctx context.Context, eventID string) (string, error)
}

// issueWith makes the Pending Order Paid with the captured payment, builds
// the N Issued tickets and the Held Settlement for the Organizer at its fee
// rate, and stores them through IssuanceRepository.Issue. When another run
// made the Order Paid first, it re-reads and returns that Order.
func (uc *orderUseCase) issueWith(ctx context.Context, order *entity.Order, eventID, organizerID string, platformFeeRateBps int, identityVerificationID string, captured *entity.CapturedPayment) (*entity.Order, error) {
	now := uc.clock()
	paid := *order
	paid.Payment = entity.Payment{
		Provider:          captured.Provider,
		ProviderPaymentID: order.CardAuthorizationID,
		CardBrand:         captured.CardBrand,
		CardLast4:         captured.CardLast4,
	}
	paid.Status = entity.OrderStatusPaid
	paid.PayTime = &now
	tickets := make([]*entity.Ticket, 0, order.TicketCount)
	for range order.TicketCount {
		tickets = append(tickets, &entity.Ticket{
			ID:                             entity.TicketID(entity.NewID()),
			OrderID:                        order.ID,
			UserID:                         order.UserID,
			EventID:                        eventID,
			IdentityVerificationID:         identityVerificationID,
			ResaleWithoutConsentProhibited: true,
			Status:                         entity.TicketStatusIssued,
			IssueTime:                      now,
		})
	}
	settlement := entity.NewHeldSettlement(&paid, organizerID, eventID, platformFeeRateBps, now)

	if err := uc.issuanceRepo.Issue(ctx, &paid, tickets, settlement); err != nil {
		if !errors.Is(err, apperr.ErrAlreadyExists) {
			return nil, err
		}
		return uc.orderRepo.Get(ctx, order.ID)
	}

	uc.logger.Info(ctx, "issued order and tickets",
		slog.String("order_id", string(order.ID)),
		slog.String("lottery_entry_id", string(order.LotteryEntryID)),
		slog.String("checkout_id", string(order.CheckoutID)),
		slog.Int("ticket_count", order.TicketCount),
		slog.Int("platform_fee_rate_bps", platformFeeRateBps),
	)
	return &paid, nil
}

// isPaid reports whether the Order was already made Paid, also when it was
// refunded since.
func isPaid(order *entity.Order) bool {
	return order.Status == entity.OrderStatusPaid || order.Status == entity.OrderStatusRefunded
}

// identityVerificationBinding returns the id of the IdentityVerification the Tickets of a sale are bound to:
// the user's IdentityVerification, whatever its status, when the sale requires verification;
// empty otherwise. The user's missing IdentityVerification is returned as its error.
func (uc *orderUseCase) identityVerificationBinding(ctx context.Context, sale *entity.TicketSale, userID entity.UserID) (string, error) {
	if !sale.VerificationRequirement.RequiresVerification() {
		return "", nil
	}
	identityVerification, err := uc.identityVerificationRepo.GetByUserID(ctx, string(userID))
	if err != nil {
		return "", err
	}
	return identityVerification.ID, nil
}

// placeWait is how long Place waits for the issue of a Checkout before it
// tells the fan the purchase is still being completed.
const placeWait = 30 * time.Second

// Place implements [OrderUseCase]. Every check a fan can fix runs here, before
// the workflow starts: a workflow runs once per id, so an outcome it records
// would answer the fan's next try too.
func (uc *orderUseCase) Place(ctx context.Context, checkoutID entity.CheckoutID, userID entity.UserID, now time.Time) (*entity.Order, error) {
	attrs := slog.String("checkout_id", string(checkoutID))

	// Ownership first, without revealing whether the Checkout exists, so a fan
	// never joins another fan's workflow.
	checkout, err := uc.checkoutRepo.Get(ctx, checkoutID)
	if errors.Is(err, apperr.ErrNotFound) {
		return nil, apperr.New(codes.PermissionDenied, "checkout is not the caller's", attrs)
	}
	if err != nil {
		return nil, err
	}
	order, err := uc.orderRepo.Get(ctx, checkout.OrderID)
	if err != nil {
		return nil, err
	}
	if order.UserID != userID {
		return nil, apperr.New(codes.PermissionDenied, "checkout is not the caller's", attrs)
	}
	if isPaid(order) {
		return order, nil
	}

	switch checkout.Status {
	case entity.CheckoutStatusHeld:
		if err := uc.checkPlaceable(ctx, checkout, order, now); err != nil {
			return nil, err
		}
	case entity.CheckoutStatusCommitted:
		// Placed before: wait for the same run.
	default:
		return nil, apperr.New(codes.FailedPrecondition, "the checkout has ended", attrs,
			slog.String("status", checkout.Status.String()))
	}

	id := IssueWorkflowID(order.ID)
	if err := uc.workflows.Start(ctx, WorkflowIssue, id, IssueRequest{CheckoutID: checkout.ID},
		WithQueue(CheckoutQueue), WithDeadline(now.Add(paymentWorkflowDeadline))); err != nil {
		return nil, err
	}
	result, err := Await[IssueResult](ctx, uc.workflows, id, placeWait)
	if errors.Is(err, apperr.ErrDeadlineExceeded) {
		return nil, apperr.Wrap(err, codes.Unavailable, "the purchase is still being completed", attrs)
	}
	if err != nil {
		return nil, err
	}
	if !result.Paid {
		return nil, apperr.New(codes.FailedPrecondition, "the order could not be placed: the hold was over or the card could not be charged", attrs)
	}
	return uc.orderRepo.Get(ctx, order.ID)
}

// checkPlaceable checks a Held Checkout and its Order before anything is
// committed: holding at now, the concert published, the card authorization
// Authorized for the Order's amount, and the user's IdentityVerification when
// the sale requires verification.
func (uc *orderUseCase) checkPlaceable(ctx context.Context, checkout *entity.Checkout, order *entity.Order, now time.Time) error {
	attrs := slog.String("checkout_id", string(checkout.ID))
	if !checkout.IsHoldingAt(now) {
		return apperr.New(codes.FailedPrecondition, "the hold has lapsed", attrs)
	}
	stock, err := uc.stockRepo.Get(ctx, order.TicketStockID, now)
	if err != nil {
		return err
	}
	published, err := uc.eventState.IsEventPublished(ctx, stock.EventID)
	if err != nil {
		return err
	}
	if !published {
		return apperr.New(codes.FailedPrecondition, "the concert is not published", attrs)
	}
	if order.CardAuthorizationID == "" {
		return apperr.New(codes.FailedPrecondition, "the card was not authorized", attrs)
	}
	authorization, err := uc.cardAuthorizations.Get(ctx, order.CardAuthorizationID)
	if err != nil {
		return err
	}
	if err := authorization.Confirm(entity.CardAuthorizationPurchase{Purpose: entity.CardAuthorizationPurposeCheckout, CheckoutID: checkout.ID}, order.Amount); err != nil {
		return err
	}
	sale, err := uc.ticketSaleRepo.Get(ctx, checkout.TicketSaleID)
	if err != nil {
		return err
	}
	_, err = uc.identityVerificationBinding(ctx, sale, order.UserID)
	return err
}

// checkoutCommit is what the commit step of a Checkout's issue records.
type checkoutCommit struct {
	// Committed is false when the Checkout was not holding at the commit.
	Committed bool `json:"committed"`
	// Charged is true when the Checkout already has a capture time.
	Charged bool `json:"charged"`
}

// issueCheckout commits a placed Checkout's tickets, charges its Order once
// and issues it.
func (uc *orderUseCase) issueCheckout(ctx context.Context, f Flow, checkoutID entity.CheckoutID) (IssueResult, error) {
	attrs := slog.String("checkout_id", string(checkoutID))
	// Every read that can fail for a data reason runs before the commit and
	// the charge.
	plan, err := Step(ctx, f, "read", func(ctx context.Context) (issuePlan, error) {
		return uc.planCheckoutIssue(ctx, checkoutID)
	})
	if err != nil {
		return IssueResult{}, err
	}
	if plan.Paid {
		return IssueResult{OrderID: plan.OrderID, Paid: true}, nil
	}

	// The commit judges "holding" at the time the step runs, never the
	// placement time: a hold that lapsed while Issue waited in the queue may
	// have had its tickets sold to another fan.
	commit, err := Step(ctx, f, "commit", func(ctx context.Context) (checkoutCommit, error) {
		outcome, err := uc.checkoutRepo.Commit(ctx, checkoutID, uc.clock())
		if err != nil || outcome == entity.CommitOutcomeNotHeld {
			return checkoutCommit{}, err
		}
		checkout, err := uc.checkoutRepo.Get(ctx, checkoutID)
		if err != nil {
			return checkoutCommit{}, err
		}
		return checkoutCommit{Committed: true, Charged: checkout.IsCharged()}, nil
	})
	if err != nil {
		return IssueResult{}, err
	}
	if !commit.Committed {
		uc.logger.Info(ctx, "checkout not issued: the hold was over at the commit", attrs,
			slog.String("order_id", string(plan.OrderID)))
		return IssueResult{OrderID: plan.OrderID}, nil
	}

	// Charge once: a Checkout with a capture time is never charged again.
	if !commit.Charged {
		charged, err := uc.capture(ctx, f, plan)
		if err != nil {
			return IssueResult{}, err
		}
		if !charged {
			if _, err := Step(ctx, f, "revert commit", func(ctx context.Context) (struct{}, error) {
				return struct{}{}, uc.checkoutRepo.RevertCommit(ctx, checkoutID)
			}); err != nil {
				return IssueResult{}, err
			}
			uc.logger.Info(ctx, "checkout commit reverted: the card could not be charged", attrs,
				slog.String("order_id", string(plan.OrderID)))
			return IssueResult{OrderID: plan.OrderID}, nil
		}
		if _, err := Step(ctx, f, "record capture", func(ctx context.Context) (struct{}, error) {
			return struct{}{}, uc.checkoutRepo.RecordCapture(ctx, checkoutID, uc.clock())
		}); err != nil {
			return IssueResult{}, err
		}
	}

	return uc.issueCaptured(ctx, f, plan)
}

// planCheckoutIssue reads the Checkout, its Order, the Order's stock, the
// Checkout's sale, the event's Organizer and the IdentityVerification binding.
func (uc *orderUseCase) planCheckoutIssue(ctx context.Context, checkoutID entity.CheckoutID) (issuePlan, error) {
	checkout, err := uc.checkoutRepo.Get(ctx, checkoutID)
	if err != nil {
		return issuePlan{}, err
	}
	order, err := uc.orderRepo.Get(ctx, checkout.OrderID)
	if err != nil {
		return issuePlan{}, err
	}
	if isPaid(order) {
		return issuePlan{OrderID: order.ID, Paid: true}, nil
	}
	return uc.planOrder(ctx, order, checkout.TicketSaleID)
}

// capture charges the plan's card authorization as the step "capture" and
// reports whether it was charged; false means it can no longer be charged.
func (uc *orderUseCase) capture(ctx context.Context, f Flow, plan issuePlan) (bool, error) {
	return Step(ctx, f, "capture", func(ctx context.Context) (bool, error) {
		err := uc.cardAuthorizations.Capture(ctx, plan.CardAuthorizationID)
		if errors.Is(err, apperr.ErrFailedPrecondition) {
			// The card authorization can no longer be charged: a result.
			return false, nil
		}
		return err == nil, err
	})
}

// capturedPayment reads the payment a Captured CardAuthorization took, for the
// Order to record.
//
// # Possible errors
//
//   - FailedPrecondition: the card authorization is not captured.
//   - Errors of CardAuthorizationRepository.Get, unchanged.
func (uc *orderUseCase) capturedPayment(ctx context.Context, cardAuthorizationID string) (*entity.CapturedPayment, error) {
	authorization, err := uc.cardAuthorizations.Get(ctx, cardAuthorizationID)
	if err != nil {
		return nil, err
	}
	if authorization.Status != entity.CardAuthorizationStatusCaptured || authorization.CapturedPayment == nil {
		return nil, apperr.New(codes.FailedPrecondition, "the card authorization is not captured")
	}
	return authorization.CapturedPayment, nil
}
