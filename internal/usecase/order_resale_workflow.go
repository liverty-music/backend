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

// resaleIssuePlan is what the issue of a Won resale entry reads before the
// charge, as its step records it.
type resaleIssuePlan struct {
	issuePlan
	// StartTime is the event's start time, after which nothing is charged.
	StartTime time.Time `json:"start_time"`
}

// resaleCapture is the outcome of the charge of a Won resale entry, as its
// step records it.
type resaleCapture struct {
	// Charged is true when the card authorization was captured.
	Charged bool `json:"charged"`
	// TooLate is true when the event's start time came before a charge
	// succeeded; nothing more is tried.
	TooLate bool `json:"too_late"`
	// ConcertCancelled is true when the event was no longer PUBLISHED before
	// the charge, so nothing was charged.
	ConcertCancelled bool `json:"concert_cancelled,omitempty"`
}

// issueResaleEntry charges a Won resale entry's Order and issues it with its
// listings. A concert that is no longer PUBLISHED, or a card authorization
// that can no longer be charged, makes the entry Lost without a charge; at the
// event's start time it stops charging, releases the card authorization and
// makes the entry Lost only once the payment provider confirms nothing was
// charged, or issues the Order when an earlier attempt did charge it. When a
// listing allocated to the entry was cancelled after the charge, it refunds
// the whole charge and then makes the entry Lost. It never makes the entry
// Lost while a charge's outcome is unknown or a charge it made is not
// refunded.
func (uc *orderUseCase) issueResaleEntry(ctx context.Context, f Flow, entryID entity.ResaleEntryID) (IssueResult, error) {
	attrs := slog.String("resale_entry_id", string(entryID))
	plan, err := Step(ctx, f, "read", func(ctx context.Context) (resaleIssuePlan, error) {
		return uc.planResaleIssue(ctx, entryID)
	})
	if err != nil {
		return IssueResult{}, err
	}
	if plan.Paid {
		return IssueResult{OrderID: plan.OrderID, Paid: true}, nil
	}

	// The step is retried while the payment provider cannot be reached; each
	// try first checks the event and the clock, so the retries end at the
	// start time. The event is checked inside this step, so a resumed run
	// does not check it again after a capture that happened.
	outcome, err := Step(ctx, f, "capture", func(ctx context.Context) (resaleCapture, error) {
		published, err := uc.eventState.IsEventPublished(ctx, plan.EventID)
		if err != nil {
			return resaleCapture{}, err
		}
		if !published {
			return uc.resaleCaptureOfCancelledConcert(ctx, plan.CardAuthorizationID, published)
		}
		if !uc.clock().Before(plan.StartTime) {
			return resaleCapture{TooLate: true}, nil
		}
		err = uc.cardAuthorizations.Capture(ctx, plan.CardAuthorizationID)
		if errors.Is(err, apperr.ErrFailedPrecondition) {
			return resaleCapture{}, nil
		}
		return resaleCapture{Charged: err == nil}, err
	})
	if err != nil {
		return IssueResult{}, err
	}
	switch {
	case outcome.Charged:
		return uc.issueChargedResaleEntry(ctx, f, entryID, plan.issuePlan)
	case outcome.ConcertCancelled:
		if err := uc.revertResaleWin(ctx, f, entryID, entity.ResaleEntryLostReasonConcertCancelled); err != nil {
			return IssueResult{}, err
		}
		uc.logger.Info(ctx, "resale win reverted: the concert was cancelled before the charge", attrs,
			slog.String("order_id", string(plan.OrderID)))
		return IssueResult{OrderID: plan.OrderID}, nil
	case !outcome.TooLate:
		if err := uc.revertResaleWin(ctx, f, entryID, entity.ResaleEntryLostReasonCardNotCharged); err != nil {
			return IssueResult{}, err
		}
		uc.logger.Info(ctx, "resale win reverted: the card could not be charged", attrs,
			slog.String("order_id", string(plan.OrderID)))
		return IssueResult{OrderID: plan.OrderID}, nil
	}

	// The show has begun without a successful charge. Releasing tells whether
	// an attempt that ended unknown did charge the card; while the provider
	// cannot be reached the release is retried and the entry stays Won.
	released, err := Step(ctx, f, "release at the start time", func(ctx context.Context) (bool, error) {
		err := uc.cardAuthorizations.Release(ctx, plan.CardAuthorizationID)
		if errors.Is(err, apperr.ErrFailedPrecondition) {
			return false, nil
		}
		return err == nil, err
	})
	if err != nil {
		return IssueResult{}, err
	}
	if !released {
		// The card was charged after all: the fan gets the tickets they paid for.
		return uc.issueChargedResaleEntry(ctx, f, entryID, plan.issuePlan)
	}
	if err := uc.revertResaleWin(ctx, f, entryID, entity.ResaleEntryLostReasonPaymentNotCompletedBeforeShow); err != nil {
		return IssueResult{}, err
	}
	uc.logger.Info(ctx, "resale win reverted: the payment was not completed before the show", attrs,
		slog.String("order_id", string(plan.OrderID)))
	return IssueResult{OrderID: plan.OrderID}, nil
}

// resaleCaptureOfCancelledConcert is the capture step's outcome for a Won
// entry whose event is no longer PUBLISHED: nothing is charged. An earlier try
// of the step may have captured the card authorization with its outcome
// unknown, so the payment provider is asked first; a captured one is issued
// as charged, and the cancellation refund of the fan's own Order covers it.
func (uc *orderUseCase) resaleCaptureOfCancelledConcert(ctx context.Context, cardAuthorizationID string, published bool) (resaleCapture, error) {
	authorization, err := uc.cardAuthorizations.Get(ctx, cardAuthorizationID)
	if err != nil {
		return resaleCapture{}, err
	}
	if authorization.CountsAsCharged(published) {
		return resaleCapture{Charged: true}, nil
	}
	return resaleCapture{ConcertCancelled: true}, nil
}

// issueChargedResaleEntry issues the charged Order of a Won resale entry.
// When a listing allocated to the entry was cancelled after the charge, it
// refunds the whole charge as the Order's own refund, keeping the entry Won
// until the refund succeeds, and then makes the entry Lost with the lost
// reason "listing cancelled". No card authorization is released for it.
func (uc *orderUseCase) issueChargedResaleEntry(ctx context.Context, f Flow, entryID entity.ResaleEntryID, plan issuePlan) (IssueResult, error) {
	result, listingCancelled, err := uc.issueCaptured(ctx, f, plan)
	if err != nil || !listingCancelled {
		return result, err
	}
	// Retried while the payment provider cannot be reached, up to the
	// workflow's deadline, after which an admin is told.
	if _, err := Step(ctx, f, "refund cancelled win", func(ctx context.Context) (struct{}, error) {
		return struct{}{}, uc.refundWholeCharge(ctx, plan.OrderID, plan.CardAuthorizationID)
	}); err != nil {
		return IssueResult{}, err
	}
	if err := uc.revertResaleWin(ctx, f, entryID, entity.ResaleEntryLostReasonListingCancelled); err != nil {
		return IssueResult{}, err
	}
	uc.logger.Info(ctx, "resale win reverted: a listing was cancelled after the charge, which was refunded",
		slog.String("resale_entry_id", string(entryID)), slog.String("order_id", string(plan.OrderID)))
	return IssueResult{OrderID: plan.OrderID}, nil
}

// refundWholeCharge refunds the whole captured charge of the Order's card
// authorization as the Order's own refund, so a repeated call refunds once.
//
// # Possible errors
//
//   - FailedPrecondition: the card authorization is not captured, or the
//     charge was already refunded beyond what the refund needs.
//   - Unavailable: the payment provider cannot be reached.
//   - Errors of reading the card authorization, unchanged.
func (uc *orderUseCase) refundWholeCharge(ctx context.Context, orderID entity.OrderID, cardAuthorizationID string) error {
	captured, err := uc.capturedPayment(ctx, cardAuthorizationID)
	if err != nil {
		return err
	}
	chargeID, err := uc.orderRepo.GetProviderChargeID(ctx, captured.ProviderPaymentID)
	if err != nil {
		return err
	}
	_, err = uc.orderRepo.CreateRefund(ctx, entity.RefundParams{
		OrderID:          orderID,
		ProviderChargeID: chargeID,
		Amount:           captured.AmountJPY,
	})
	return err
}

// revertResaleWin makes the Won entry Lost with the reason in its own step.
func (uc *orderUseCase) revertResaleWin(ctx context.Context, f Flow, entryID entity.ResaleEntryID, reason entity.ResaleEntryLostReason) error {
	_, err := Step(ctx, f, "revert win", func(ctx context.Context) (struct{}, error) {
		return struct{}{}, uc.resaleEntryRepo.RevertWin(ctx, entryID, reason)
	})
	return err
}

// planResaleIssue reads the Won resale entry, its Pending Order, the Order's
// stock and event, the event's Organizer and the IdentityVerification the
// Tickets are bound to when any sale of the event requires verification.
func (uc *orderUseCase) planResaleIssue(ctx context.Context, entryID entity.ResaleEntryID) (resaleIssuePlan, error) {
	attrs := slog.String("resale_entry_id", string(entryID))
	entry, err := uc.resaleEntryRepo.Get(ctx, entryID)
	if err != nil {
		return resaleIssuePlan{}, err
	}
	order := entry.Order
	if order.IsPaid() {
		return resaleIssuePlan{OrderID: order.ID, Paid: true}, nil
	}
	if err := entry.CheckChargeable(); err != nil {
		return resaleIssuePlan{}, err
	}
	stock, err := uc.stockRepo.Get(ctx, order.TicketStockID, uc.clock())
	if err != nil {
		return resaleIssuePlan{}, err
	}
	event, err := uc.eventRepo.Get(ctx, stock.EventID)
	if err != nil {
		return resaleIssuePlan{}, err
	}
	if event.StartTime == nil {
		return resaleIssuePlan{}, apperr.New(codes.FailedPrecondition, "the event has no start time", attrs)
	}
	organizerID, err := uc.eventOrganizerRepo.GetOrganizerID(ctx, stock.EventID)
	if err != nil {
		return resaleIssuePlan{}, err
	}
	organizer, err := uc.organizerRepo.Get(ctx, organizerID)
	if err != nil {
		return resaleIssuePlan{}, err
	}
	identityVerificationID, err := uc.resaleIdentityVerificationBinding(ctx, stock.EventID, order.UserID)
	if err != nil {
		return resaleIssuePlan{}, err
	}
	return resaleIssuePlan{
		OrderID:                order.ID,
		CardAuthorizationID:    order.CardAuthorizationID,
		EventID:                stock.EventID,
		OrganizerID:            organizerID,
		PlatformFeeRateBps:     organizer.PlatformFeeRateBps,
		IdentityVerificationID: identityVerificationID,
		StartTime:              *event.StartTime,
	}, nil
}

// resaleIdentityVerificationBinding returns the id of the user's
// IdentityVerification when any sale of the event requires verification
// (design D9), empty otherwise.
func (uc *orderUseCase) resaleIdentityVerificationBinding(ctx context.Context, eventID string, userID entity.UserID) (string, error) {
	sales, err := uc.ticketSaleRepo.ListByEvent(ctx, eventID)
	if err != nil {
		return "", err
	}
	for _, sale := range sales {
		if sale.VerificationRequirement.RequiresVerification() {
			return uc.identityVerificationBinding(ctx, sale, userID)
		}
	}
	return "", nil
}
