package usecase

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"

	"github.com/liverty-music/backend/internal/entity"
)

// CheckoutUseCase runs a signed-in fan's first-come checkout on a TicketType
// of a FirstCome TicketSale: holding the tickets, reading the checkout, and
// storing the fan's 本人確認 (identity check) details and opening the card
// hold, plus the job that ends lapsed checkouts and gives back leftover card
// holds.
type CheckoutUseCase interface {
	// Start holds count tickets of the TicketType for the fan for 15 minutes,
	// or resumes the fan's holding Checkout with the same count. When the sale
	// requires verification the fan must hold an Active Kyc.
	//
	// # Possible errors
	//
	//  - NotFound: the TicketType does not exist.
	//  - FailedPrecondition: the sale's method is not FirstCome, the event is
	//    not published, the TicketType is not OnSale or AllHeld, the sale
	//    requires verification and the fan has no Active Kyc, or the
	//    per-account limit would be exceeded counting the fan's Issued Tickets
	//    for the event.
	//  - InvalidArgument: count is below 1 or above the TicketType's
	//    per-account limit.
	//  - ResourceExhausted: not enough tickets remain.
	Start(ctx context.Context, userID entity.UserID, ticketTypeID entity.TicketTypeID, count int) (*entity.Checkout, error)

	// Get returns the fan's own Checkout as of now: a Held Checkout whose hold
	// has lapsed is returned Expired.
	//
	// # Possible errors
	//
	//  - PermissionDenied: the Checkout does not exist or is another fan's.
	Get(ctx context.Context, userID entity.UserID, checkoutID entity.CheckoutID) (*entity.Checkout, error)

	// Authorize stores the full name and phone number on the fan's User, opens
	// the card hold for the fan's holding Checkout, records it on the Checkout
	// and returns the hold's confirmation secret (never stored).
	//
	// # Possible errors
	//
	//  - PermissionDenied: the Checkout does not exist or is another fan's.
	//  - FailedPrecondition: the Checkout is not holding.
	//  - InvalidArgument: the full name or phone number is missing or breaks
	//    the User's personal details rule.
	//  - Unavailable: card payments cannot be reached.
	Authorize(ctx context.Context, userID entity.UserID, checkoutID entity.CheckoutID, fullName, phoneNumber string) (string, error)

	// ReleaseExpired makes lapsed Held Checkouts Expired and gives back the
	// card hold of every ended Checkout, reporting a charged hold on an ended
	// Checkout as needing an operator. One Checkout's failure never stops
	// the others.
	//
	// # Possible errors
	//
	//  - The listing's error.
	ReleaseExpired(ctx context.Context) error
}

// checkoutUseCase implements [CheckoutUseCase].
type checkoutUseCase struct {
	checkoutRepo entity.CheckoutRepository
	saleRepo     entity.TicketSaleRepository
	userRepo     entity.UserRepository
	kycRepo      entity.KycRepository
	eventState   EventPublishStatePort
	auth         entity.CheckoutAuthorizationPort
	clock        Clock
	logger       *logging.Logger
}

// Compile-time interface compliance check.
var _ CheckoutUseCase = (*checkoutUseCase)(nil)

// CheckoutDeps are the dependencies of [NewCheckoutUseCase]. All are required.
type CheckoutDeps struct {
	CheckoutRepo entity.CheckoutRepository
	SaleRepo     entity.TicketSaleRepository
	UserRepo     entity.UserRepository
	KycRepo      entity.KycRepository
	EventState   EventPublishStatePort
	Auth         entity.CheckoutAuthorizationPort
	Clock        Clock
	Logger       *logging.Logger
}

// NewCheckoutUseCase constructs a CheckoutUseCase.
func NewCheckoutUseCase(d CheckoutDeps) CheckoutUseCase {
	return &checkoutUseCase{
		checkoutRepo: d.CheckoutRepo,
		saleRepo:     d.SaleRepo,
		userRepo:     d.UserRepo,
		kycRepo:      d.KycRepo,
		eventState:   d.EventState,
		auth:         d.Auth,
		clock:        d.Clock,
		logger:       d.Logger,
	}
}

// Start implements [CheckoutUseCase]. The per-account limit counting the
// fan's Issued Tickets for the event is checked by
// [entity.CheckoutRepository.GetOrCreateHeld] under the ticket type's row
// lock, so two concurrent starts cannot both pass it.
func (uc *checkoutUseCase) Start(ctx context.Context, userID entity.UserID, ticketTypeID entity.TicketTypeID, count int) (*entity.Checkout, error) {
	now := uc.clock()
	sale, ticketType, err := uc.saleRepo.GetTicketType(ctx, ticketTypeID, now)
	if err != nil {
		return nil, err
	}
	if sale.Method != entity.TicketSaleMethodFirstCome {
		return nil, apperr.New(codes.FailedPrecondition, "the ticket type is not sold first-come")
	}
	published, err := uc.eventState.IsEventPublished(ctx, ticketType.EventID)
	if err != nil {
		return nil, err
	}
	if !published {
		return nil, apperr.New(codes.FailedPrecondition, "the concert is not published")
	}
	if state := ticketType.StateAt(sale, now); state != entity.TicketTypeStateOnSale && state != entity.TicketTypeStateAllHeld {
		return nil, apperr.New(codes.FailedPrecondition, "the ticket type is not on sale")
	}
	if err := ticketType.ValidateTicketCount(count); err != nil {
		return nil, apperr.Wrap(err, codes.InvalidArgument, err.Error())
	}
	if err := uc.verificationGate(ctx, sale, userID); err != nil {
		return nil, err
	}

	return uc.checkoutRepo.GetOrCreateHeld(ctx, ticketTypeID, userID, count, now)
}

// verificationGate applies the sale's verification gate: when the sale
// requires verification, the user must hold an Active Kyc, and
// FailedPrecondition is returned otherwise. A sale without a requirement needs
// no Kyc.
func (uc *checkoutUseCase) verificationGate(ctx context.Context, sale *entity.TicketSale, userID entity.UserID) error {
	if !sale.VerificationRequirement.RequiresVerification() {
		return nil
	}
	kyc, err := uc.kycRepo.GetByUserID(ctx, string(userID))
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return apperr.Wrap(err, codes.FailedPrecondition, "the sale requires verification and the fan has no kyc")
		}
		return err
	}
	if kyc.Status != entity.VerificationStatusActive {
		return apperr.New(codes.FailedPrecondition, "the sale requires verification and the fan's kyc is not active")
	}
	return nil
}

// ownCheckout reads the Checkout and fails with PermissionDenied, without
// revealing whether it exists, unless it is the fan's.
func (uc *checkoutUseCase) ownCheckout(ctx context.Context, userID entity.UserID, id entity.CheckoutID) (*entity.Checkout, error) {
	checkout, err := uc.checkoutRepo.Get(ctx, id)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return nil, apperr.New(codes.PermissionDenied, "the checkout is not the caller's")
		}
		return nil, err
	}
	if checkout.UserID != userID {
		return nil, apperr.New(codes.PermissionDenied, "the checkout is not the caller's")
	}
	return checkout, nil
}

// Get implements [CheckoutUseCase].
func (uc *checkoutUseCase) Get(ctx context.Context, userID entity.UserID, checkoutID entity.CheckoutID) (*entity.Checkout, error) {
	checkout, err := uc.ownCheckout(ctx, userID, checkoutID)
	if err != nil {
		return nil, err
	}
	asOf := *checkout
	asOf.Status = checkout.StatusAt(uc.clock())
	return &asOf, nil
}

// Authorize implements [CheckoutUseCase].
func (uc *checkoutUseCase) Authorize(ctx context.Context, userID entity.UserID, checkoutID entity.CheckoutID, fullName, phoneNumber string) (string, error) {
	checkout, err := uc.ownCheckout(ctx, userID, checkoutID)
	if err != nil {
		return "", err
	}
	now := uc.clock()
	if !checkout.IsHoldingAt(now) {
		return "", apperr.New(codes.FailedPrecondition, "the hold has lapsed; start again")
	}
	if fullName == "" || phoneNumber == "" {
		return "", apperr.New(codes.InvalidArgument, "the full name and phone number are required")
	}
	if err := entity.ValidatePersonalDetails(fullName, phoneNumber); err != nil {
		return "", apperr.Wrap(err, codes.InvalidArgument, err.Error())
	}
	_, ticketType, err := uc.saleRepo.GetTicketType(ctx, checkout.TicketTypeID, now)
	if err != nil {
		return "", err
	}

	if _, err := uc.userRepo.UpdatePersonalDetails(ctx, string(userID), fullName, phoneNumber); err != nil {
		return "", err
	}
	ref, secret, err := uc.auth.CreateAuthorization(ctx, checkout.Amount, entity.AuthorizationMetadata{
		CheckoutID:   checkout.ID,
		TicketTypeID: checkout.TicketTypeID,
		EventID:      ticketType.EventID,
	})
	if err != nil {
		return "", err
	}
	if err := uc.checkoutRepo.SetAuthorization(ctx, checkoutID, ref); err != nil {
		return "", err
	}
	return secret, nil
}

// ReleaseExpired implements [CheckoutUseCase].
func (uc *checkoutUseCase) ReleaseExpired(ctx context.Context) error {
	now := uc.clock()
	due, err := uc.checkoutRepo.ListDue(ctx, now)
	if err != nil {
		return err
	}
	for _, checkout := range due {
		switch checkout.Status {
		case entity.CheckoutStatusHeld:
			if _, err := uc.checkoutRepo.Release(ctx, checkout.ID, now); err != nil {
				uc.logger.Warn(ctx, "release sweep: failed to release checkout; retrying next run",
					slog.String("checkout_id", string(checkout.ID)), slog.Any("error", err))
			}
		case entity.CheckoutStatusExpired, entity.CheckoutStatusReleased:
			uc.giveBackCardHold(ctx, checkout, now)
		}
	}
	return nil
}

// giveBackCardHold releases an ended checkout's card hold and records it. A
// charged hold is reported as needing an operator.
func (uc *checkoutUseCase) giveBackCardHold(ctx context.Context, checkout *entity.Checkout, now time.Time) {
	if err := uc.auth.CancelAuthorization(ctx, checkout.AuthorizationRef); err != nil {
		if errors.Is(err, apperr.ErrFailedPrecondition) {
			uc.logger.Error(ctx, LogKeyNeedsOperator, errors.New("the card hold of an ended checkout was charged"),
				slog.String("checkout_id", string(checkout.ID)),
				slog.String("payment_ref", checkout.AuthorizationRef),
				slog.String("status", checkout.Status.String()),
			)
			return
		}
		uc.logger.Warn(ctx, "release sweep: failed to cancel card hold; retrying next run",
			slog.String("checkout_id", string(checkout.ID)), slog.Any("error", err))
		return
	}
	if err := uc.checkoutRepo.RecordAuthorizationRelease(ctx, checkout.ID, now); err != nil {
		uc.logger.Warn(ctx, "release sweep: failed to record card hold release; retrying next run",
			slog.String("checkout_id", string(checkout.ID)), slog.Any("error", err))
	}
}
