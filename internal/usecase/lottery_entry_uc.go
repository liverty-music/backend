package usecase

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"log/slog"
	mrand "math/rand/v2"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/pkg/analyticslog"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
)

// Clock returns the current time. Injected so tests can use a deterministic
// fake; production code passes time.Now.
type Clock func() time.Time

// EventPublishStatePort reports whether a concert event has been published.
// Implementations query the events table via the concert repository; the port
// is defined here (consumer package) per Clean Architecture conventions.
type EventPublishStatePort interface {
	// IsEventPublished returns true when the event with the given ID exists and
	// its own publish state is PUBLISHED, whatever its other dates are.
	// Returns false — not an error — when the event exists but is DRAFT,
	// CANCELLED or discovered.
	//
	// # Possible errors
	//
	//  - NotFound: no event with the given ID exists.
	//  - Internal: database query failure.
	IsEventPublished(ctx context.Context, eventID string) (bool, error)

	// GetEventOrganizerID returns the ID of the organizer that owns the event's
	// series, via the series' OrganizerID. Used to enforce that an organizer can
	// only inspect the sales of events it owns. Returns "" (not an error) when
	// the event exists but its series is a discovered (non-first-party) series
	// with no owning organizer.
	//
	// # Possible errors
	//
	//  - NotFound: no event with the given ID exists.
	//  - Internal: database query failure.
	GetEventOrganizerID(ctx context.Context, eventID string) (string, error)
}

// LotteryEntryUseCase lets a fan enter a Lottery sale for a TicketStock of an
// event it names — open the card authorization for a ticket count, then
// confirm it into an entry and its Pending Order — shows
// the fan their entries, and draws every Lottery sale once its window has
// closed.
type LotteryEntryUseCase interface {
	// AuthorizeCard checks that the fan may enter the Lottery sale for count
	// tickets of the stock now, stores the fan's full name and phone number on
	// their User, and opens a card authorization for the lottery entry of the
	// fan in the sale, for the stock, the count and the code, for the stock's
	// SeatType amount for count. It returns the card authorization with its id
	// and its card authorization secret, which must not be stored. It stores
	// no Order and no entry; authorizing again opens a new card authorization.
	// The code is not checked against any list or the event's Series.
	//
	// # Possible errors
	//
	//  - NotFound: the sale or the TicketStock does not exist.
	//  - InvalidArgument: the count is not 1 to the event's per-account limit,
	//    or the name or phone number is missing or breaks the personal details
	//    rule.
	//  - FailedPrecondition: the sale is not a Lottery or does not name the
	//    stock's event, its window is not open, the fan has no Active
	//    IdentityVerification when the sale requires one, the application
	//    does not fit the event, or the fan already has an entry in the sale
	//    for the stock's event.
	//  - Unavailable: card payments cannot be reached.
	AuthorizeCard(ctx context.Context, saleID entity.TicketSaleID, stockID entity.TicketStockID, userID entity.UserID, count int, fullName, phoneNumber string, code *entity.ReferralCode) (*entity.CardAuthorization, error)

	// Confirm makes the Entered entry and its Pending Order from an
	// Authorized card authorization for a lottery entry and returns the entry
	// with its Order, or returns the entry the card authorization already
	// made. caller is the fan who confirms; nil when the payment provider
	// reports the card authorization Authorized, and then a Checkout's card
	// authorization is left as it is and Confirm returns no entry and no
	// error. An Authorized card authorization that Confirm rejects is
	// released before Confirm fails; one an Order holds is never released.
	// Nothing is captured.
	//
	// # Possible errors
	//
	//  - PermissionDenied: the card authorization does not exist, is not for a
	//    lottery entry, or is not the caller's.
	//  - FailedPrecondition: the card authorization is Pending, Captured or
	//    Released (nothing is released), or it was rejected and released: the
	//    TicketStock no longer exists, the card brand is not accepted, the
	//    application no longer fits the event, the fan already has another
	//    entry in the sale for the event, or the sale's end time has passed.
	//  - InvalidArgument: the amount differs from the stock's SeatType amount
	//    for the count; the card authorization was released.
	//  - Errors of reading the card authorization or the TicketStock, of
	//    storing the entry, or of releasing a rejected card authorization,
	//    unchanged.
	Confirm(ctx context.Context, cardAuthorizationID string, caller *entity.UserID) (*entity.LotteryEntry, error)

	// ListByTicketSale returns the fan's entries in the Lottery sale, at most
	// one for each event it names, each with its Order, in every state, newest
	// first; an empty slice when there is none. The name is kept until the
	// rename that follows remodel-ticket-sales (design D11).
	//
	// # Possible errors
	//
	//  - NotFound: the sale does not exist.
	ListByTicketSale(ctx context.Context, saleID entity.TicketSaleID, userID entity.UserID) ([]*entity.LotteryEntry, error)

	// DrawDueSales runs [LotteryEntryUseCase.RunDraw] for every Lottery sale due
	// now. A failed draw is logged and stays due for the next run.
	//
	// # Possible errors
	//
	//  - Errors of listing the due sales, unchanged; nothing is drawn.
	DrawDueSales(ctx context.Context) error

	// RunDraw draws one closed Lottery sale: for each TicketStock of its
	// events it draws the sale's Entered entries for the stock against the
	// stock's remaining tickets in an unpredictable order, captures each
	// winner's card authorization and releases every other, and records the
	// whole sale's outcome, the Lost entries' Orders Void. A winner whose
	// capture fails is released and recorded Lost. A sale with no Entered
	// entry is left as it is. It creates no Ticket.
	//
	// # Possible errors
	//
	//  - NotFound: the sale does not exist.
	//  - Errors of reading the entries or recording the outcome, unchanged.
	RunDraw(ctx context.Context, saleID entity.TicketSaleID) error
}

// lotteryUseCase implements [LotteryEntryUseCase].
type lotteryEntryUseCase struct {
	saleRepo                 entity.TicketSaleRepository
	stockRepo                entity.TicketStockRepository
	entryRepo                entity.LotteryEntryRepository
	orderRepo                entity.OrderRepository
	eventRepo                entity.EventRepository
	identityVerificationRepo entity.IdentityVerificationRepository
	userRepo                 entity.UserRepository
	cardAuthorizations       entity.CardAuthorizationRepository
	publisher                EventPublisher
	clock                    Clock
	newRand                  func() *mrand.Rand
	logger                   *logging.Logger
}

// Compile-time interface compliance check.
var _ LotteryEntryUseCase = (*lotteryEntryUseCase)(nil)

// LotteryEntryDeps are the dependencies of [NewLotteryEntryUseCase]. All are
// required.
type LotteryEntryDeps struct {
	SaleRepo                 entity.TicketSaleRepository
	StockRepo                entity.TicketStockRepository
	EntryRepo                entity.LotteryEntryRepository
	OrderRepo                entity.OrderRepository
	EventRepo                entity.EventRepository
	IdentityVerificationRepo entity.IdentityVerificationRepository
	UserRepo                 entity.UserRepository
	CardAuthorizations       entity.CardAuthorizationRepository
	Publisher                EventPublisher
	Clock                    Clock
	Logger                   *logging.Logger
}

// NewLotteryEntryUseCase constructs a LotteryEntryUseCase with the given
// dependencies.
func NewLotteryEntryUseCase(d LotteryEntryDeps) LotteryEntryUseCase {
	return newLotteryUseCase(d, cryptoSeededRand)
}

// newLotteryUseCase constructs the use case with the draw's random source.
func newLotteryUseCase(d LotteryEntryDeps, newRand func() *mrand.Rand) *lotteryEntryUseCase {
	return &lotteryEntryUseCase{
		saleRepo:                 d.SaleRepo,
		stockRepo:                d.StockRepo,
		entryRepo:                d.EntryRepo,
		orderRepo:                d.OrderRepo,
		eventRepo:                d.EventRepo,
		identityVerificationRepo: d.IdentityVerificationRepo,
		userRepo:                 d.UserRepo,
		cardAuthorizations:       d.CardAuthorizations,
		publisher:                d.Publisher,
		clock:                    d.Clock,
		newRand:                  newRand,
		logger:                   d.Logger,
	}
}

// AuthorizeCard implements [LotteryEntryUseCase].
func (uc *lotteryEntryUseCase) AuthorizeCard(ctx context.Context, saleID entity.TicketSaleID, stockID entity.TicketStockID, userID entity.UserID, count int, fullName, phoneNumber string, code *entity.ReferralCode) (*entity.CardAuthorization, error) {
	now := uc.clock()
	sale, err := uc.saleRepo.Get(ctx, saleID)
	if err != nil {
		return nil, err
	}
	stock, err := uc.stockRepo.Get(ctx, stockID, now)
	if err != nil {
		return nil, err
	}
	if err := entity.ValidateLotteryEntrySale(sale, stock); err != nil {
		return nil, apperr.New(codes.FailedPrecondition, err.Error())
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
	if err := entity.ValidatePersonalDetails(fullName, phoneNumber); err != nil {
		return nil, apperr.New(codes.InvalidArgument, err.Error())
	}
	if !sale.IsOpenAt(now) {
		return nil, apperr.New(codes.FailedPrecondition, "the sale's window is not open")
	}
	if err := uc.checkVerification(ctx, sale, userID); err != nil {
		return nil, err
	}
	held, err := uc.orderRepo.GetHeldForEvent(ctx, userID, stock.EventID, nil, now)
	if err != nil {
		return nil, err
	}
	if !event.Fits(held, stock.SeatTypeID, count) {
		return nil, apperr.New(codes.FailedPrecondition, "the application does not fit the event's per-account limit or seat type")
	}
	// Advisory: LotteryEntry.Create is the guard, but refusing here spares the
	// fan a card authentication that Confirm would release.
	entered, err := uc.hasEntryForEvent(ctx, sale.ID, userID, stock.EventID, now)
	if err != nil {
		return nil, err
	}
	if entered {
		return nil, apperr.New(codes.FailedPrecondition, "the fan has already entered this sale for the event")
	}
	if _, err := uc.userRepo.UpdatePersonalDetails(ctx, string(userID), fullName, phoneNumber); err != nil {
		return nil, err
	}
	return uc.cardAuthorizations.Create(ctx, entity.CardAuthorizationPurchase{
		Purpose:       entity.CardAuthorizationPurposeLotteryEntry,
		UserID:        userID,
		TicketSaleID:  sale.ID,
		TicketStockID: stock.ID,
		EventID:       stock.EventID,
		TicketCount:   count,
		ReferralCode:  code,
	}, stock.SeatType.AmountFor(count))
}

// hasEntryForEvent reports whether the fan has an entry, in any state, in the
// sale for a TicketStock of the event.
func (uc *lotteryEntryUseCase) hasEntryForEvent(ctx context.Context, saleID entity.TicketSaleID, userID entity.UserID, eventID string, now time.Time) (bool, error) {
	entries, err := uc.entryRepo.ListByTicketSaleAndUser(ctx, saleID, userID)
	if err != nil || len(entries) == 0 {
		return false, err
	}
	stocks, err := uc.stockRepo.ListByEvent(ctx, eventID, now)
	if err != nil {
		return false, err
	}
	ofEvent := make(map[entity.TicketStockID]struct{}, len(stocks))
	for _, s := range stocks {
		ofEvent[s.ID] = struct{}{}
	}
	for _, e := range entries {
		if _, ok := ofEvent[e.Order.TicketStockID]; ok {
			return true, nil
		}
	}
	return false, nil
}

// checkVerification fails with FailedPrecondition when the sale requires
// verification and the fan holds no Active IdentityVerification.
func (uc *lotteryEntryUseCase) checkVerification(ctx context.Context, sale *entity.TicketSale, userID entity.UserID) error {
	if !sale.VerificationRequirement.RequiresVerification() {
		return nil
	}
	identityVerification, err := uc.identityVerificationRepo.GetByUserID(ctx, string(userID))
	if err != nil {
		if isNotFound(err) {
			return apperr.New(codes.FailedPrecondition, "the sale requires 本人確認; verify your identity first")
		}
		return err
	}
	if identityVerification.Status != entity.VerificationStatusActive {
		return apperr.New(codes.FailedPrecondition, "the sale requires an active 本人確認; please re-verify")
	}
	return nil
}

// Confirm implements [LotteryEntryUseCase].
func (uc *lotteryEntryUseCase) Confirm(ctx context.Context, cardAuthorizationID string, caller *entity.UserID) (*entity.LotteryEntry, error) {
	notCallers := func() error {
		return apperr.New(codes.PermissionDenied, "the card authorization is not the caller's lottery entry",
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
	if !purchase.IsLotteryEntry() {
		if caller == nil && purchase.Purpose == entity.CardAuthorizationPurposeCheckout {
			// The notice of a Checkout's card authorization: its own
			// Confirm settles it.
			return nil, nil
		}
		return nil, notCallers()
	}
	if caller != nil && purchase.UserID != *caller {
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
	event, err := uc.eventRepo.Get(ctx, stock.EventID)
	if err != nil {
		return nil, err
	}
	held, err := uc.orderRepo.GetHeldForEvent(ctx, purchase.UserID, stock.EventID, nil, now)
	if err != nil {
		return nil, err
	}
	if !event.Fits(held, stock.SeatTypeID, purchase.TicketCount) {
		return nil, uc.reject(ctx, cardAuthorizationID,
			apperr.New(codes.FailedPrecondition, "the application no longer fits the event's per-account limit or seat type"))
	}

	order := entity.NewPendingOrder(purchase.UserID, stock, purchase.TicketCount, purchase.ReferralCode, entity.OrderPaymentProvider)
	order.CardAuthorizationID = cardAuthorizationID
	entry, err = uc.entryRepo.Create(ctx, entity.NewLotteryEntry(order, purchase.TicketSaleID))
	if err != nil {
		switch {
		case errors.Is(err, apperr.ErrAlreadyExists):
			return nil, uc.reject(ctx, cardAuthorizationID,
				apperr.Wrap(err, codes.FailedPrecondition, "the fan has already entered this sale for the event"))
		case errors.Is(err, apperr.ErrFailedPrecondition):
			return nil, uc.reject(ctx, cardAuthorizationID, err)
		}
		return nil, err
	}
	return entry, nil
}

// reject releases an Authorized card authorization that Confirm refused and
// returns the refusal, or the release's failure so the rejection is tried
// again.
func (uc *lotteryEntryUseCase) reject(ctx context.Context, cardAuthorizationID string, refusal error) error {
	if err := uc.cardAuthorizations.Release(ctx, cardAuthorizationID); err != nil {
		uc.logger.Warn(ctx, "confirm: releasing a rejected card authorization failed",
			slog.String("card_authorization_id", cardAuthorizationID),
			slog.Any("refusal", refusal),
			slog.Any("error", err),
		)
		return err
	}
	uc.logger.Info(ctx, "confirm: rejected card authorization released",
		slog.String("card_authorization_id", cardAuthorizationID),
		slog.Any("refusal", refusal),
	)
	return refusal
}

// ListByTicketSale implements [LotteryEntryUseCase].
func (uc *lotteryEntryUseCase) ListByTicketSale(ctx context.Context, saleID entity.TicketSaleID, userID entity.UserID) ([]*entity.LotteryEntry, error) {
	if _, err := uc.saleRepo.Get(ctx, saleID); err != nil {
		return nil, err
	}
	return uc.entryRepo.ListByTicketSaleAndUser(ctx, saleID, userID)
}

// DrawDueSales implements [LotteryEntryUseCase].
func (uc *lotteryEntryUseCase) DrawDueSales(ctx context.Context) error {
	sales, err := uc.saleRepo.ListDueForDraw(ctx, uc.clock())
	if err != nil {
		return err
	}
	for _, sale := range sales {
		if err := uc.RunDraw(ctx, sale.ID); err != nil {
			// One failed sale must not stop the others; it stays due.
			uc.logger.Warn(ctx, "draw failed for sale; retrying next run",
				slog.String("ticket_sale_id", string(sale.ID)),
				slog.Any("error", err),
			)
		}
	}
	return nil
}

// RunDraw implements [LotteryEntryUseCase].
func (uc *lotteryEntryUseCase) RunDraw(ctx context.Context, saleID entity.TicketSaleID) error {
	sale, err := uc.saleRepo.Get(ctx, saleID)
	if err != nil {
		return err
	}

	var (
		winners []entity.DrawWinnerRow
		losers  []entity.DrawLoserRow
	)
	drawTime := uc.clock()
	for _, eventID := range sale.EventIDs {
		stocks, err := uc.stockRepo.ListByEvent(ctx, eventID, drawTime)
		if err != nil {
			return err
		}
		for _, listed := range stocks {
			entries, err := uc.entryRepo.ListEnteredByTicketSaleAndTicketStock(ctx, saleID, listed.ID)
			if err != nil {
				return err
			}
			if len(entries) == 0 {
				continue
			}
			// The remaining count at the draw excludes the tickets sold or won
			// in earlier sales of the event and the tickets held now.
			stock, err := uc.stockRepo.Get(ctx, listed.ID, uc.clock())
			if err != nil {
				return err
			}
			refs := make(map[entity.LotteryEntryID]string, len(entries))
			entered := make([]entity.DrawEntry, 0, len(entries))
			for _, e := range entries {
				refs[e.ID] = e.Order.CardAuthorizationID
				entered = append(entered, entity.DrawEntry{ID: e.ID, TicketCount: e.Order.TicketCount})
			}

			result := entity.RunLotteryDraw(entered, stock.Remaining(), uc.newRand())
			for _, w := range result.Winners {
				ref := refs[w.Entry.ID]
				if err := uc.cardAuthorizations.Capture(ctx, ref); err != nil {
					// No promotion of a loser (繰上げ): the tickets stay
					// unallocated.
					uc.logger.Warn(ctx, "capture failed for winner; recording it lost",
						slog.String("ticket_sale_id", string(saleID)),
						slog.String("lottery_entry_id", string(w.Entry.ID)),
						slog.Any("error", err),
					)
					uc.release(ctx, saleID, w.Entry.ID, ref)
					losers = append(losers, entity.DrawLoserRow{LotteryEntryID: w.Entry.ID, CardAuthorizationID: ref, DrawPosition: w.DrawPosition, LostReason: entity.LotteryEntryLostReasonCardNotCharged})
					continue
				}
				winners = append(winners, entity.DrawWinnerRow{LotteryEntryID: w.Entry.ID, CardAuthorizationID: ref, DrawPosition: w.DrawPosition})
			}
			for _, l := range result.Losers {
				ref := refs[l.Entry.ID]
				uc.release(ctx, saleID, l.Entry.ID, ref)
				losers = append(losers, entity.DrawLoserRow{LotteryEntryID: l.Entry.ID, CardAuthorizationID: ref, DrawPosition: l.DrawPosition, LostReason: entity.LotteryEntryLostReasonNotDrawn})
			}
		}
	}
	if len(winners) == 0 && len(losers) == 0 {
		return nil
	}

	if err := uc.entryRepo.PersistDrawOutcome(ctx, saleID, winners, losers); err != nil {
		return err
	}
	uc.logger.Info(ctx, "draw completed", analyticslog.Attrs(analyticslog.TopicPayment,
		slog.String("ticket_sale_id", string(saleID)),
		slog.Int("winner_count", len(winners)),
		slog.Int("loser_count", len(losers)),
	)...)

	return nil
}

// release releases a card authorization during the draw; a failure is logged and does not
// stop the draw.
func (uc *lotteryEntryUseCase) release(ctx context.Context, saleID entity.TicketSaleID, entryID entity.LotteryEntryID, cardAuthorizationID string) {
	if err := uc.cardAuthorizations.Release(ctx, cardAuthorizationID); err != nil {
		uc.logger.Warn(ctx, "draw: releasing a card authorization failed; continuing",
			slog.String("ticket_sale_id", string(saleID)),
			slog.String("lottery_entry_id", string(entryID)),
			slog.Any("error", err),
		)
	}
}

// cryptoSeededRand returns a new *mrand.Rand seeded from crypto/rand so the
// draw shuffle is unpredictable in production. Two uint64 values seed the PCG
// source (state + sequence).
func cryptoSeededRand() *mrand.Rand {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failure is extremely rare (OS-level entropy pool error).
		// Panic here: a non-random seed would silently bias the draw, which is
		// a correctness violation more serious than a crash.
		panic("lottery: crypto/rand unavailable: " + err.Error())
	}
	state := binary.LittleEndian.Uint64(b[:8])
	seq := binary.LittleEndian.Uint64(b[8:])
	return mrand.New(mrand.NewPCG(state, seq))
}

// isNotFound reports whether err wraps an apperr.ErrNotFound sentinel.
func isNotFound(err error) bool {
	return errors.Is(err, apperr.ErrNotFound)
}
