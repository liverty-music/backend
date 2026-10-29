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

// CheckoutUseCase runs a signed-in fan's first-come checkout on a TicketStock
// while a FirstCome TicketSale of its event is open: holding the tickets,
// reading the checkout, and storing the fan's 本人確認 (identity check)
// details and opening the card authorization, plus the job that ends lapsed
// checkouts and releases leftover card authorizations.
type CheckoutUseCase interface {
	// Start holds count tickets of the stock for the fan for 15 minutes with
	// a Pending Order, or resumes the fan's holding Checkout of the stock with
	// the same count. The Checkout belongs to the FirstCome sale of the stock's
	// event that is open now. When the sale requires verification the fan must
	// hold an Active IdentityVerification. A code is kept on a new Order; a
	// resumed Checkout keeps its Order's code.
	//
	// # Possible errors
	//
	//  - NotFound: the TicketStock does not exist.
	//  - FailedPrecondition: the event is not published, no FirstCome sale of
	//    the event is open, a closed Lottery sale of the event still has
	//    Entered entries for the stock, the sale requires verification and the
	//    fan has no Active IdentityVerification, or the application does not
	//    fit the event (the per-account limit or another seat type).
	//  - InvalidArgument: count is below 1 or above the event's per-account
	//    limit.
	//  - ResourceExhausted: not enough tickets remain.
	Start(ctx context.Context, userID entity.UserID, stockID entity.TicketStockID, count int, code *entity.ReferralCode) (*entity.Checkout, error)

	// Get returns the fan's own Checkout with its Order as of now: a Held
	// Checkout whose hold has lapsed is returned Expired.
	//
	// # Possible errors
	//
	//  - PermissionDenied: the Checkout does not exist or is another fan's.
	Get(ctx context.Context, userID entity.UserID, checkoutID entity.CheckoutID) (*entity.Checkout, error)

	// AuthorizeCard stores the full name and phone number on the fan's User,
	// opens the card authorization for the Order of the fan's holding
	// Checkout, records it on the Order and returns its card authorization
	// secret (never stored).
	//
	// # Possible errors
	//
	//  - PermissionDenied: the Checkout does not exist or is another fan's.
	//  - FailedPrecondition: the Checkout is not holding.
	//  - InvalidArgument: the full name or phone number is missing or breaks
	//    the User's personal details rule.
	//  - Unavailable: card payments cannot be reached.
	AuthorizeCard(ctx context.Context, userID entity.UserID, checkoutID entity.CheckoutID, fullName, phoneNumber string) (string, error)

	// ReleaseExpired makes lapsed Held Checkouts Expired with their Orders
	// Void and gives back the card authorization of every ended Checkout's
	// Order, reporting a captured one on an ended Checkout as needing an
	// admin. One Checkout's failure never stops the others.
	//
	// # Possible errors
	//
	//  - The listing's error.
	ReleaseExpired(ctx context.Context) error
}

// checkoutUseCase implements [CheckoutUseCase].
type checkoutUseCase struct {
	checkoutRepo             entity.CheckoutRepository
	saleRepo                 entity.TicketSaleRepository
	stockRepo                entity.TicketStockRepository
	entryRepo                entity.LotteryEntryRepository
	orderRepo                entity.OrderRepository
	userRepo                 entity.UserRepository
	identityVerificationRepo entity.IdentityVerificationRepository
	eventState               EventPublishStatePort
	eventRepo                entity.EventRepository
	cardAuthorizations       entity.CardAuthorizationRepository
	publisher                EventPublisher
	clock                    Clock
	logger                   *logging.Logger
}

// Compile-time interface compliance check.
var _ CheckoutUseCase = (*checkoutUseCase)(nil)

// CheckoutDeps are the dependencies of [NewCheckoutUseCase]. All are required.
type CheckoutDeps struct {
	CheckoutRepo             entity.CheckoutRepository
	SaleRepo                 entity.TicketSaleRepository
	StockRepo                entity.TicketStockRepository
	EntryRepo                entity.LotteryEntryRepository
	OrderRepo                entity.OrderRepository
	UserRepo                 entity.UserRepository
	IdentityVerificationRepo entity.IdentityVerificationRepository
	EventState               EventPublishStatePort
	EventRepo                entity.EventRepository
	CardAuthorizations       entity.CardAuthorizationRepository
	Publisher                EventPublisher
	Clock                    Clock
	Logger                   *logging.Logger
}

// NewCheckoutUseCase constructs a CheckoutUseCase.
func NewCheckoutUseCase(d CheckoutDeps) CheckoutUseCase {
	return &checkoutUseCase{
		checkoutRepo:             d.CheckoutRepo,
		saleRepo:                 d.SaleRepo,
		stockRepo:                d.StockRepo,
		entryRepo:                d.EntryRepo,
		orderRepo:                d.OrderRepo,
		userRepo:                 d.UserRepo,
		identityVerificationRepo: d.IdentityVerificationRepo,
		eventState:               d.EventState,
		eventRepo:                d.EventRepo,
		cardAuthorizations:       d.CardAuthorizations,
		publisher:                d.Publisher,
		clock:                    d.Clock,
		logger:                   d.Logger,
	}
}

// Start implements [CheckoutUseCase]. The fit check here is advisory: it
// spares the fan a hold that would be refused.
// [entity.CheckoutRepository.GetOrCreateHeld] checks it again under the
// fan's users row lock, so two concurrent starts cannot both pass it.
func (uc *checkoutUseCase) Start(ctx context.Context, userID entity.UserID, stockID entity.TicketStockID, count int, code *entity.ReferralCode) (*entity.Checkout, error) {
	now := uc.clock()
	stock, err := uc.stockRepo.Get(ctx, stockID, now)
	if err != nil {
		return nil, err
	}
	published, err := uc.eventState.IsEventPublished(ctx, stock.EventID)
	if err != nil {
		return nil, err
	}
	if !published {
		return nil, apperr.New(codes.FailedPrecondition, "the concert is not published")
	}
	sales, err := uc.saleRepo.ListByEvent(ctx, stock.EventID)
	if err != nil {
		return nil, err
	}
	sale := openFirstComeSale(sales, now)
	if sale == nil {
		return nil, apperr.New(codes.FailedPrecondition, "no first-come sale of the event is open")
	}
	if err := uc.checkLotteriesDrawn(ctx, sales, stock, now); err != nil {
		return nil, err
	}
	event, err := uc.eventRepo.Get(ctx, stock.EventID)
	if err != nil {
		return nil, err
	}
	if err := entity.ValidateTicketCount(count, event); err != nil {
		return nil, apperr.Wrap(err, codes.InvalidArgument, err.Error())
	}
	if err := uc.verificationGate(ctx, sale, userID); err != nil {
		return nil, err
	}
	holding, err := uc.holdingOrderID(ctx, userID, stockID, now)
	if err != nil {
		return nil, err
	}
	held, err := uc.orderRepo.GetHeldForEvent(ctx, userID, stock.EventID, holding, now)
	if err != nil {
		return nil, err
	}
	if !event.Fits(held, stock.SeatTypeID, count) {
		return nil, apperr.New(codes.FailedPrecondition, "the application does not fit the event's per-account limit or seat type")
	}
	return uc.checkoutRepo.GetOrCreateHeld(ctx, stockID, sale.ID, userID, count, code, now)
}

// openFirstComeSale returns the FirstCome sale whose window is open at now,
// or nil when there is none. Sales of one event never overlap, so there is at
// most one.
func openFirstComeSale(sales []*entity.TicketSale, now time.Time) *entity.TicketSale {
	for _, sale := range sales {
		if sale.Method == entity.TicketSaleMethodFirstCome && sale.IsOpenAt(now) {
			return sale
		}
	}
	return nil
}

// checkLotteriesDrawn fails with FailedPrecondition while a Lottery sale of
// the event has closed and still has Entered entries for the stock: that draw
// has not yet taken its winners' tickets from the stock (design D6).
func (uc *checkoutUseCase) checkLotteriesDrawn(ctx context.Context, sales []*entity.TicketSale, stock *entity.TicketStock, now time.Time) error {
	for _, sale := range sales {
		if sale.Method != entity.TicketSaleMethodLottery || sale.IsNotYetOverAt(now) {
			continue
		}
		undrawn, err := uc.entryRepo.ListUndrawnForTicketType(ctx, sale.ID, stock.ID)
		if err != nil {
			return err
		}
		if len(undrawn) > 0 {
			return apperr.New(codes.FailedPrecondition, "a closed lottery of the event is not drawn yet")
		}
	}
	return nil
}

// holdingOrderID returns the id of the Order of the fan's holding Checkout of
// the stock at now, which a new start resumes or replaces, or nil when there
// is none.
func (uc *checkoutUseCase) holdingOrderID(ctx context.Context, userID entity.UserID, stockID entity.TicketStockID, now time.Time) (*entity.OrderID, error) {
	checkouts, err := uc.checkoutRepo.ListByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	for _, c := range checkouts {
		if c.Order.TicketStockID == stockID && c.IsHoldingAt(now) {
			id := c.OrderID
			return &id, nil
		}
	}
	return nil, nil
}

// verificationGate applies the sale's verification gate: when the sale
// requires verification, the user must hold an Active IdentityVerification, and
// FailedPrecondition is returned otherwise. A sale without a requirement needs
// no IdentityVerification.
func (uc *checkoutUseCase) verificationGate(ctx context.Context, sale *entity.TicketSale, userID entity.UserID) error {
	if !sale.VerificationRequirement.RequiresVerification() {
		return nil
	}
	identityVerification, err := uc.identityVerificationRepo.GetByUserID(ctx, string(userID))
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return apperr.Wrap(err, codes.FailedPrecondition, "the sale requires verification and the fan has no identity verification")
		}
		return err
	}
	if identityVerification.Status != entity.VerificationStatusActive {
		return apperr.New(codes.FailedPrecondition, "the sale requires verification and the fan's identity verification is not active")
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
	if checkout.Order.UserID != userID {
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

// AuthorizeCard implements [CheckoutUseCase].
func (uc *checkoutUseCase) AuthorizeCard(ctx context.Context, userID entity.UserID, checkoutID entity.CheckoutID, fullName, phoneNumber string) (string, error) {
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
	stock, err := uc.stockRepo.Get(ctx, checkout.Order.TicketStockID, now)
	if err != nil {
		return "", err
	}

	if _, err := uc.userRepo.UpdatePersonalDetails(ctx, string(userID), fullName, phoneNumber); err != nil {
		return "", err
	}
	authorization, err := uc.cardAuthorizations.Create(ctx, entity.CardAuthorizationPurchase{
		Purpose:       entity.CardAuthorizationPurposeCheckout,
		CheckoutID:    checkout.ID,
		UserID:        checkout.Order.UserID,
		TicketStockID: checkout.Order.TicketStockID,
		EventID:       stock.EventID,
		TicketCount:   checkout.Order.TicketCount,
	}, checkout.Order.Amount)
	if err != nil {
		return "", err
	}
	if err := uc.checkoutRepo.SetCardAuthorization(ctx, checkoutID, authorization.ID); err != nil {
		return "", err
	}
	return authorization.Secret, nil
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
			released, err := uc.checkoutRepo.Release(ctx, checkout.ID, now)
			if err != nil {
				uc.logger.Warn(ctx, "release sweep: failed to release checkout; retrying next run",
					slog.String("checkout_id", string(checkout.ID)), slog.Any("error", err))
				continue
			}
			if released {
				uc.reportExpired(ctx, checkout)
			}
		case entity.CheckoutStatusExpired, entity.CheckoutStatusReleased:
			uc.releaseCardAuthorization(ctx, checkout, now)
		}
	}
	return nil
}

// releaseCardAuthorization releases the card authorization of an ended
// checkout's Order and records it. A captured card authorization is reported
// as needing an admin.
func (uc *checkoutUseCase) releaseCardAuthorization(ctx context.Context, checkout *entity.Checkout, now time.Time) {
	if err := uc.cardAuthorizations.Release(ctx, checkout.Order.CardAuthorizationID); err != nil {
		if errors.Is(err, apperr.ErrFailedPrecondition) {
			uc.logger.Error(ctx, LogKeyNeedsOperator, errors.New("the card authorization of an ended checkout was charged"),
				slog.String("checkout_id", string(checkout.ID)),
				slog.String("card_authorization_id", checkout.Order.CardAuthorizationID),
				slog.String("status", checkout.Status.String()),
			)
			return
		}
		uc.logger.Warn(ctx, "release sweep: failed to cancel card authorization; retrying next run",
			slog.String("checkout_id", string(checkout.ID)), slog.Any("error", err))
		return
	}
	if err := uc.checkoutRepo.RecordCardAuthorizationRelease(ctx, checkout.ID, now); err != nil {
		uc.logger.Warn(ctx, "release sweep: failed to record card authorization release; retrying next run",
			slog.String("checkout_id", string(checkout.ID)), slog.Any("error", err))
	}
}

// reportExpired announces, for product analytics, that the Checkout's hold
// lapsed. A failed publish is logged and never changes the release.
func (uc *checkoutUseCase) reportExpired(ctx context.Context, checkout *entity.Checkout) {
	if err := uc.publisher.PublishEvent(ctx, entity.SubjectCheckoutExpired, entity.CheckoutExpiredData{
		CheckoutID:     string(checkout.ID),
		UserID:         string(checkout.Order.UserID),
		TicketStockID:  string(checkout.Order.TicketStockID),
		TicketCount:    checkout.Order.TicketCount,
		HoldExpireTime: checkout.HoldExpireTime,
	}); err != nil {
		uc.logger.Error(ctx, "failed to publish ORDER.checkout_expired event", err,
			slog.String("checkout_id", string(checkout.ID)))
	}
}
