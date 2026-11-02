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
// Series, so the issuance path can stamp the denormalized OrganizerID on the
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

// unissuedChargeReportAge is how long a charged checkout may stay unissued
// before every run of the stalled-checkout job reports it to an admin.
const unissuedChargeReportAge = 10 * time.Minute

// LogKeyNeedsOperator is the log message of a purchase that needs an admin:
// money was taken without tickets. A log-based alert on it pages the admin;
// the alert in cloud-provisioning (monitoring.ts) matches this text and reads
// the checkout_id and card_authorization_id fields, so the constant and its
// text keep their names.
const LogKeyNeedsOperator = "checkout needs an operator"

// issue is the issuance core of a checkout: it resolves the event's Organizer
// and its fee rate and issues the Order with issueWith.
func (uc *orderUseCase) issue(ctx context.Context, order *entity.Order, eventID, identityVerificationID string, captured *entity.CapturedPayment) (*entity.Order, error) {
	organizerID, err := uc.eventOrganizerRepo.GetOrganizerID(ctx, eventID)
	if err != nil {
		return nil, err
	}
	organizer, err := uc.organizerRepo.Get(ctx, organizerID)
	if err != nil {
		return nil, err
	}
	return uc.issueWith(ctx, order, eventID, organizerID, organizer.PlatformFeeRateBps, identityVerificationID, captured)
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

// IssueFromCheckout implements [OrderUseCase]. The whole call runs under
// the Checkout's lock, so a double tap or the stalled-checkout job waits for
// the first call and then takes the idempotent path.
func (uc *orderUseCase) IssueFromCheckout(ctx context.Context, checkoutID entity.CheckoutID, caller *entity.UserID) (*entity.Order, error) {
	var order *entity.Order
	err := uc.checkoutRepo.Serialize(ctx, checkoutID, func(ctx context.Context) error {
		var err error
		order, err = uc.issueFromCheckout(ctx, checkoutID, caller)
		return err
	})
	if err != nil {
		return nil, err
	}
	return order, nil
}

func (uc *orderUseCase) issueFromCheckout(ctx context.Context, checkoutID entity.CheckoutID, caller *entity.UserID) (*entity.Order, error) {
	attrs := slog.String("checkout_id", string(checkoutID))

	// 1. Ownership first, without revealing whether the Checkout exists.
	checkout, err := uc.checkoutRepo.Get(ctx, checkoutID)
	if err != nil {
		if caller != nil && errors.Is(err, apperr.ErrNotFound) {
			return nil, apperr.New(codes.PermissionDenied, "checkout is not the caller's", attrs)
		}
		return nil, err
	}
	if caller != nil && checkout.Order.UserID != *caller {
		return nil, apperr.New(codes.PermissionDenied, "checkout is not the caller's", attrs)
	}

	// 2. A Paid Order ends the call.
	if isPaid(checkout.Order) {
		return checkout.Order, nil
	}

	now := uc.clock()
	sale, err := uc.ticketSaleRepo.Get(ctx, checkout.TicketSaleID)
	if err != nil {
		return nil, err
	}
	eventID, err := uc.orderRepo.GetEventID(ctx, checkout.OrderID)
	if err != nil {
		return nil, err
	}

	if checkout.Status == entity.CheckoutStatusHeld {
		// 3. Holding, published and authorized, checked before the card.
		if !checkout.IsHoldingAt(now) {
			return nil, apperr.New(codes.FailedPrecondition, "the hold has lapsed", attrs)
		}
		published, err := uc.eventState.IsEventPublished(ctx, eventID)
		if err != nil {
			return nil, err
		}
		if !published {
			return nil, apperr.New(codes.FailedPrecondition, "the concert is not published", attrs)
		}
		if checkout.Order.CardAuthorizationID == "" {
			return nil, apperr.New(codes.FailedPrecondition, "the card was not authorized", attrs)
		}
	}

	// The IdentityVerification binding is read before the commit and the charge, so a user
	// without a IdentityVerification is never charged.
	identityVerificationID, err := uc.identityVerificationBinding(ctx, sale, checkout.Order.UserID)
	if err != nil {
		return nil, err
	}

	if checkout.Status == entity.CheckoutStatusHeld {
		// 4. The card authorization must be authorized and for the Order's amount.
		authorization, err := uc.cardAuthorizations.Get(ctx, checkout.Order.CardAuthorizationID)
		if err != nil {
			return nil, err
		}
		if err := authorization.Confirm(entity.CardAuthorizationPurchase{Purpose: entity.CardAuthorizationPurposeCheckout, CheckoutID: checkout.ID}, checkout.Order.Amount); err != nil {
			return nil, err
		}
		// 5. Commit while holding.
		outcome, err := uc.checkoutRepo.Commit(ctx, checkoutID, now)
		if err != nil {
			return nil, err
		}
		if outcome == entity.CommitOutcomeNotHeld {
			return nil, apperr.New(codes.FailedPrecondition, "the checkout is no longer holding", attrs)
		}
		if checkout, err = uc.checkoutRepo.Get(ctx, checkoutID); err != nil {
			return nil, err
		}
	}

	switch checkout.Status {
	case entity.CheckoutStatusCommitted, entity.CheckoutStatusCompleted:
	default:
		return nil, apperr.New(codes.FailedPrecondition, "the checkout has ended", attrs,
			slog.String("status", checkout.Status.String()))
	}

	// Charge once: a Checkout with a capture time is never charged again.
	if !checkout.IsCharged() {
		if err := uc.cardAuthorizations.Capture(ctx, checkout.Order.CardAuthorizationID); err != nil {
			if errors.Is(err, apperr.ErrFailedPrecondition) {
				if revertErr := uc.checkoutRepo.RevertCommit(ctx, checkoutID); revertErr != nil {
					return nil, revertErr
				}
			}
			return nil, err
		}
		if err := uc.checkoutRepo.RecordCapture(ctx, checkoutID, uc.clock()); err != nil {
			return nil, err
		}
	}

	// The charge is the card authorization; its facets are read back from the provider,
	// as for a lottery win.
	captured, err := uc.capturedPayment(ctx, checkout.Order.CardAuthorizationID)
	if err != nil {
		return nil, err
	}
	return uc.issue(ctx, checkout.Order, eventID, identityVerificationID, captured)
}

// IssueDueCheckouts implements [OrderUseCase].
func (uc *orderUseCase) IssueDueCheckouts(ctx context.Context) error {
	now := uc.clock()
	due, err := uc.checkoutRepo.ListDue(ctx, now)
	if err != nil {
		return err
	}
	for _, checkout := range due {
		if checkout.Status != entity.CheckoutStatusCommitted {
			continue
		}
		if checkout.IsCharged() && now.Sub(*checkout.CaptureTime) > unissuedChargeReportAge {
			uc.logger.Error(ctx, LogKeyNeedsOperator,
				errors.New("a charged checkout is still not issued"),
				slog.String("checkout_id", string(checkout.ID)),
				slog.String("card_authorization_id", checkout.Order.CardAuthorizationID),
				slog.Time("capture_time", *checkout.CaptureTime),
			)
		}
		if _, err := uc.IssueFromCheckout(ctx, checkout.ID, nil); err != nil {
			// One failure never stops the others; the next run retries.
			uc.logger.Warn(ctx, "stalled-checkout sweep: failed to issue checkout; skipping",
				slog.String("checkout_id", string(checkout.ID)),
				slog.Any("error", err),
			)
		}
	}
	return nil
}

// FulfillPayment implements [OrderUseCase]. Order.Issue announces the
// issued Order as paid (ORDER.paid); a charge reported again finds the Order
// Paid and announces nothing.
func (uc *orderUseCase) FulfillPayment(ctx context.Context, cardAuthorizationID string) error {
	checkout, err := uc.checkoutRepo.GetByCardAuthorizationID(ctx, cardAuthorizationID)
	switch {
	case err == nil:
		_, err = uc.IssueFromCheckout(ctx, checkout.ID, nil)
		return err
	case !errors.Is(err, apperr.ErrNotFound):
		return err
	}
	entry, err := uc.entryRepo.GetByCardAuthorizationID(ctx, cardAuthorizationID)
	if errors.Is(err, apperr.ErrNotFound) {
		uc.logger.Info(ctx, "completed charge of no checkout or lottery entry; nothing to fulfill",
			slog.String("card_authorization_id", cardAuthorizationID))
		return nil
	}
	if err != nil {
		return err
	}
	// The issue workflow charged the entry's Order and made it Paid; the
	// notice of that charge changes nothing.
	uc.logger.Info(ctx, "completed charge of a lottery entry; issued by its issue workflow",
		slog.String("card_authorization_id", cardAuthorizationID),
		slog.String("lottery_entry_id", string(entry.ID)))
	return nil
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
