package usecase

import (
	"context"
	"errors"
	"log/slog"
	mrand "math/rand/v2"
	"time"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"

	"github.com/liverty-music/backend/internal/entity"
)

// ResaleEntryView is one of a fan's resale entries as the fan sees it: the
// entry with its Order, its TicketStock with the stock's SeatType, and, while
// it is Entered, the time of its event's next draw.
type ResaleEntryView struct {
	// Entry is the entry with its Order.
	Entry *entity.ResaleEntry
	// Stock is the entry's TicketStock with its SeatType.
	Stock *entity.TicketStock
	// NextDrawTime is the next draw of the event; nil unless the entry is
	// Entered and a draw is still to come.
	NextDrawTime *time.Time
}

// ResaleEntryUseCase lets a fan apply for official resale (公式リセール)
// tickets of a sold-out TicketStock — open the card authorization for a ticket
// count, then confirm it into an Entered entry and its Pending Order — and
// shows the fan their entries.
type ResaleEntryUseCase interface {
	// AuthorizeCard checks that the fan may apply for count tickets of the
	// stock now, stores the fan's full name and phone number on their User,
	// and opens a card authorization for the resale entry, for the stock, the
	// count and the code, for the stock's SeatType amount for count. It
	// stores no Order and no entry.
	//
	// # Possible errors
	//
	//  - NotFound: the TicketStock does not exist.
	//  - InvalidArgument: the count is not 1 to the event's per-account limit,
	//    or the name or phone number is missing or breaks the personal details
	//    rule.
	//  - FailedPrecondition: the stock's resale is not open now, its event is
	//    not published (as for a 中止 concert), the application does not fit
	//    the event, or a sale of the event requires verification and the fan
	//    holds no Active IdentityVerification.
	//  - Unavailable: card payments cannot be reached.
	AuthorizeCard(ctx context.Context, stockID entity.TicketStockID, userID entity.UserID, count int, fullName, phoneNumber string, code *entity.ReferralCode) (*entity.CardAuthorization, error)

	// Confirm makes the Entered entry and its Pending Order from an
	// Authorized card authorization for a resale entry and returns it with its
	// stock and next draw time, or returns the entry the card authorization
	// already made. caller is the
	// fan who confirms; nil when the payment provider reports the card
	// authorization Authorized. An Authorized card authorization that
	// ResaleEntry.Create refuses is released before Confirm fails with the
	// same error. Nothing is captured.
	//
	// # Possible errors
	//
	//  - PermissionDenied: the card authorization does not exist, is not for a
	//    resale entry, or is not the caller's.
	//  - FailedPrecondition: the card authorization is not Authorized
	//    (nothing changes), or it was rejected and released: resale is no
	//    longer open, the application no longer fits the event, or the
	//    TicketStock no longer exists.
	//  - AlreadyExists: the fan already has another Entered resale entry for
	//    the event; the card authorization was released.
	//  - InvalidArgument: the amount differs from the stock's SeatType amount
	//    for the count; the card authorization was released.
	//  - Errors of reading the card authorization or the TicketStock, of
	//    storing the entry, or of releasing a rejected card authorization,
	//    unchanged.
	Confirm(ctx context.Context, cardAuthorizationID string, caller *entity.UserID) (*ResaleEntryView, error)

	// List returns the fan's resale entries, newest first, each with its
	// TicketStock and SeatType and, for an Entered one, its event's next draw
	// time; an empty slice when there is none.
	//
	// # Possible errors
	//
	//  - Errors of reading the entries, stocks or events, unchanged.
	List(ctx context.Context, userID entity.UserID) ([]*ResaleEntryView, error)

	// Withdraw withdraws the fan's own Entered entry now and returns it
	// Withdrawn with its Order Void. The release of its card authorization
	// runs from the withdrawal's announcement, so Withdraw does not wait for
	// the payment provider; no email or push is sent.
	//
	// # Possible errors
	//
	//  - NotFound: the entry does not exist or its Order is another user's.
	//  - FailedPrecondition: the entry is no longer Entered (a draw decided
	//    it, or it was withdrawn); nothing changes.
	//  - Errors of reading or withdrawing the entry, unchanged.
	Withdraw(ctx context.Context, userID entity.UserID, id entity.ResaleEntryID) (*entity.ResaleEntry, error)

	// DrawResale is the draw-resale workflow, run by its schedule every
	// minute with the time of the run: it lists the TicketStocks with Entered
	// entries whose event has a draw at that time and starts the draw of each
	// as its own workflow, named [DrawResaleStockWorkflowID], so a stock is
	// drawn once per time and a failing stock leaves the others drawn. It
	// returns the number of stocks it started.
	//
	// # Possible errors
	//
	//  - Errors of listing the stocks or of starting a draw, unchanged.
	DrawResale(ctx context.Context, f Flow, at time.Time) (int, error)

	// DrawStock is the draw of one stock at one time ([WorkflowDrawResaleStock]),
	// in one step: under the stock's row lock it takes the Entered entries in
	// a random order and the free listings oldest first and records
	// [entity.DrawResale]'s outcome, ending every entry as losing when the
	// stock's event is no longer published. It starts no other workflow: the
	// work that follows runs from each recorded entry's announcement. It
	// returns the number of entries it recorded.
	//
	// # Possible errors
	//
	//  - Errors of reading the stock or its event or of recording the draw,
	//    unchanged.
	DrawStock(ctx context.Context, f Flow, in ResaleStockDraw) (int, error)
}

// resaleEntryUseCase implements [ResaleEntryUseCase].
type resaleEntryUseCase struct {
	entryRepo                entity.ResaleEntryRepository
	stockRepo                entity.TicketStockRepository
	saleRepo                 entity.TicketSaleRepository
	orderRepo                entity.OrderRepository
	eventRepo                entity.EventRepository
	eventState               EventPublishStatePort
	identityVerificationRepo entity.IdentityVerificationRepository
	userRepo                 entity.UserRepository
	cardAuthorizations       entity.CardAuthorizationRepository
	clock                    Clock
	newRand                  func() *mrand.Rand
	logger                   *logging.Logger
}

// Compile-time interface compliance check.
var _ ResaleEntryUseCase = (*resaleEntryUseCase)(nil)

// ResaleEntryDeps are the dependencies of [NewResaleEntryUseCase]. All are
// required.
type ResaleEntryDeps struct {
	EntryRepo                entity.ResaleEntryRepository
	StockRepo                entity.TicketStockRepository
	SaleRepo                 entity.TicketSaleRepository
	OrderRepo                entity.OrderRepository
	EventRepo                entity.EventRepository
	EventState               EventPublishStatePort
	IdentityVerificationRepo entity.IdentityVerificationRepository
	UserRepo                 entity.UserRepository
	CardAuthorizations       entity.CardAuthorizationRepository
	Clock                    Clock
	Logger                   *logging.Logger
}

// NewResaleEntryUseCase constructs a ResaleEntryUseCase whose draws take
// entries in an order from a cryptographic random source.
func NewResaleEntryUseCase(d ResaleEntryDeps) ResaleEntryUseCase {
	return newResaleEntryUseCase(d, cryptoSeededRand)
}

// newResaleEntryUseCase constructs the use case with the draw's random source.
func newResaleEntryUseCase(d ResaleEntryDeps, newRand func() *mrand.Rand) *resaleEntryUseCase {
	return &resaleEntryUseCase{
		newRand:                  newRand,
		entryRepo:                d.EntryRepo,
		stockRepo:                d.StockRepo,
		saleRepo:                 d.SaleRepo,
		orderRepo:                d.OrderRepo,
		eventRepo:                d.EventRepo,
		eventState:               d.EventState,
		identityVerificationRepo: d.IdentityVerificationRepo,
		userRepo:                 d.UserRepo,
		cardAuthorizations:       d.CardAuthorizations,
		clock:                    d.Clock,
		logger:                   d.Logger,
	}
}

// AuthorizeCard implements [ResaleEntryUseCase].
func (uc *resaleEntryUseCase) AuthorizeCard(ctx context.Context, stockID entity.TicketStockID, userID entity.UserID, count int, fullName, phoneNumber string, code *entity.ReferralCode) (*entity.CardAuthorization, error) {
	now := uc.clock()
	stock, err := uc.stockRepo.Get(ctx, stockID, now)
	if err != nil {
		return nil, err
	}
	event, err := uc.eventRepo.Get(ctx, stock.EventID)
	if err != nil {
		return nil, err
	}
	if err := entity.ValidateTicketCount(count, event); err != nil {
		return nil, apperr.New(codes.InvalidArgument, err.Error())
	}
	if fullName == "" || phoneNumber == "" {
		return nil, apperr.New(codes.InvalidArgument, "full name and phone number are required")
	}
	if err := entity.ValidatePersonalDetails(&fullName, &phoneNumber); err != nil {
		return nil, apperr.New(codes.InvalidArgument, err.Error())
	}
	deadline, ok := entity.ResaleDeadlineOf(event)
	if !ok || !stock.IsResaleOpenAt(now, deadline) {
		return nil, apperr.New(codes.FailedPrecondition, "official resale of the stock is not open")
	}
	published, err := uc.eventState.IsEventPublished(ctx, stock.EventID)
	if err != nil {
		return nil, err
	}
	if !published {
		return nil, apperr.New(codes.FailedPrecondition, "the concert is not published")
	}
	held, err := uc.orderRepo.GetHeldForEvent(ctx, userID, stock.EventID, nil, now)
	if err != nil {
		return nil, err
	}
	if !event.Fits(held, stock.SeatTypeID, count) {
		return nil, apperr.New(codes.FailedPrecondition, "the application does not fit the event's per-account limit or seat type")
	}
	if err := uc.checkVerification(ctx, stock.EventID, userID); err != nil {
		return nil, err
	}
	if _, err := uc.userRepo.UpdatePersonalDetails(ctx, string(userID), &fullName, &phoneNumber); err != nil {
		return nil, err
	}
	return uc.cardAuthorizations.Create(ctx, entity.CardAuthorizationPurchase{
		Purpose:       entity.CardAuthorizationPurposeResaleEntry,
		UserID:        userID,
		TicketStockID: stock.ID,
		EventID:       stock.EventID,
		TicketCount:   count,
		ReferralCode:  code,
	}, stock.SeatType.AmountFor(count))
}

// checkVerification fails with FailedPrecondition when any sale of the event
// requires verification and the fan holds no Active IdentityVerification
// (design D9).
func (uc *resaleEntryUseCase) checkVerification(ctx context.Context, eventID string, userID entity.UserID) error {
	sales, err := uc.saleRepo.ListByEvent(ctx, eventID)
	if err != nil {
		return err
	}
	required := false
	for _, sale := range sales {
		if sale.VerificationRequirement.RequiresVerification() {
			required = true
			break
		}
	}
	if !required {
		return nil
	}
	identityVerification, err := uc.identityVerificationRepo.GetByUserID(ctx, string(userID))
	if err != nil {
		if isNotFound(err) {
			return apperr.New(codes.FailedPrecondition, "the event requires 本人確認; verify your identity first")
		}
		return err
	}
	if identityVerification.Status != entity.VerificationStatusActive {
		return apperr.New(codes.FailedPrecondition, "the event requires an active 本人確認; please re-verify")
	}
	return nil
}

// Confirm implements [ResaleEntryUseCase].
func (uc *resaleEntryUseCase) Confirm(ctx context.Context, cardAuthorizationID string, caller *entity.UserID) (*ResaleEntryView, error) {
	entry, err := uc.confirm(ctx, cardAuthorizationID, caller)
	if err != nil {
		return nil, err
	}
	views, err := uc.views(ctx, []*entity.ResaleEntry{entry})
	if err != nil {
		return nil, err
	}
	return views[0], nil
}

// confirm makes or returns the entry of the card authorization.
func (uc *resaleEntryUseCase) confirm(ctx context.Context, cardAuthorizationID string, caller *entity.UserID) (*entity.ResaleEntry, error) {
	notCallers := func() error {
		return apperr.New(codes.PermissionDenied, "the card authorization is not the caller's resale entry",
			slog.String("card_authorization_id", cardAuthorizationID))
	}
	authorization, err := uc.cardAuthorizations.Get(ctx, cardAuthorizationID)
	if err != nil {
		if isNotFound(err) {
			return nil, notCallers()
		}
		return nil, err
	}
	purchase := authorization.Purchase
	if !purchase.IsResaleEntry() || (caller != nil && purchase.UserID != *caller) {
		return nil, notCallers()
	}
	entry, err := uc.entryRepo.GetByCardAuthorizationID(ctx, cardAuthorizationID)
	if err == nil {
		return entry, nil
	}
	if !isNotFound(err) {
		return nil, err
	}
	if authorization.Status != entity.CardAuthorizationStatusAuthorized {
		return nil, apperr.New(codes.FailedPrecondition,
			"the card authorization is not authorized; card authentication must complete first",
			slog.String("card_authorization_id", cardAuthorizationID))
	}

	now := uc.clock()
	stock, err := uc.stockRepo.Get(ctx, purchase.TicketStockID, now)
	if err != nil {
		if isNotFound(err) {
			return nil, uc.reject(ctx, cardAuthorizationID,
				apperr.New(codes.FailedPrecondition, "the ticket stock no longer exists"))
		}
		return nil, err
	}
	if err := authorization.Confirm(purchase, stock.SeatType.AmountFor(purchase.TicketCount)); err != nil {
		return nil, uc.reject(ctx, cardAuthorizationID, err)
	}
	order := entity.NewPendingOrder(purchase.UserID, stock, purchase.TicketCount, purchase.ReferralCode, entity.OrderPaymentProvider)
	order.CardAuthorizationID = cardAuthorizationID
	newEntry := entity.NewResaleEntry(order, now)
	newEntry.CardAuthorizationExpireTime = authorization.ExpireTime
	entry, err = uc.entryRepo.Create(ctx, newEntry)
	if err != nil {
		if errors.Is(err, apperr.ErrAlreadyExists) || errors.Is(err, apperr.ErrFailedPrecondition) || errors.Is(err, apperr.ErrNotFound) {
			return nil, uc.reject(ctx, cardAuthorizationID, err)
		}
		return nil, err
	}
	return entry, nil
}

// reject releases an Authorized card authorization that Confirm refused and
// returns the refusal, or the release's failure so the rejection is tried
// again.
func (uc *resaleEntryUseCase) reject(ctx context.Context, cardAuthorizationID string, refusal error) error {
	if err := uc.cardAuthorizations.Release(ctx, cardAuthorizationID); err != nil {
		uc.logger.Warn(ctx, "resale confirm: releasing a rejected card authorization failed",
			slog.String("card_authorization_id", cardAuthorizationID),
			slog.Any("refusal", refusal),
			slog.Any("error", err),
		)
		return err
	}
	uc.logger.Info(ctx, "resale confirm: rejected card authorization released",
		slog.String("card_authorization_id", cardAuthorizationID),
		slog.Any("refusal", refusal),
	)
	return refusal
}

// Withdraw implements [ResaleEntryUseCase].
func (uc *resaleEntryUseCase) Withdraw(ctx context.Context, userID entity.UserID, id entity.ResaleEntryID) (*entity.ResaleEntry, error) {
	entry, err := uc.entryRepo.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if entry.Order == nil || entry.Order.UserID != userID {
		return nil, apperr.New(codes.NotFound, "no resale entry of the fan has the id", slog.String("resale_entry_id", string(id)))
	}
	return uc.entryRepo.Withdraw(ctx, id, uc.clock())
}

// List implements [ResaleEntryUseCase].
func (uc *resaleEntryUseCase) List(ctx context.Context, userID entity.UserID) ([]*ResaleEntryView, error) {
	entries, err := uc.entryRepo.ListByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	return uc.views(ctx, entries)
}

// views reads each entry's stock and, for an Entered one, its event's next
// draw time.
func (uc *resaleEntryUseCase) views(ctx context.Context, entries []*entity.ResaleEntry) ([]*ResaleEntryView, error) {
	now := uc.clock()
	stocks := map[entity.TicketStockID]*entity.TicketStock{}
	starts := map[string]*time.Time{}
	views := make([]*ResaleEntryView, 0, len(entries))
	for _, e := range entries {
		stock, ok := stocks[e.Order.TicketStockID]
		if !ok {
			var err error
			if stock, err = uc.stockRepo.Get(ctx, e.Order.TicketStockID, now); err != nil {
				return nil, err
			}
			stocks[stock.ID] = stock
		}
		view := &ResaleEntryView{Entry: e, Stock: stock}
		if e.State == entity.ResaleEntryStateEntered {
			start, ok := starts[stock.EventID]
			if !ok {
				event, err := uc.eventRepo.Get(ctx, stock.EventID)
				if err != nil {
					return nil, err
				}
				start = event.StartTime
				starts[stock.EventID] = start
			}
			if start != nil {
				if next, ok := entity.NextResaleDrawTime(now, *start); ok {
					view.NextDrawTime = &next
				}
			}
		}
		views = append(views, view)
	}
	return views, nil
}
