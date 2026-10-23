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

// LotteryEntryUseCase lets a fan enter the lottery of a TicketType — start a Draft,
// open the card authorization, confirm — and withdraw, shows the fan their entries, and
// draws every Lottery sale once its window has closed.
type LotteryEntryUseCase interface {
	// Start begins the fan's Draft entry for a TicketType of an open Lottery
	// sale, or resumes it when the count is the same, after checking the
	// count, the sale's verification requirement and the per-account limit
	// against the fan's Issued tickets for the event. Nothing is held on the
	// card.
	//
	// # Possible errors
	//
	//  - NotFound: the TicketType does not exist.
	//  - InvalidArgument: the count is not 1 to the per-account limit.
	//  - FailedPrecondition: the sale is not a Lottery, its window is not
	//    open, the fan has no Active IdentityVerification when the sale requires one, the count
	//    does not fit the per-account limit with the tickets the fan holds, or
	//    the fan already has an Entered, Won or Lost entry.
	//  - The error of [entity.ReferralRepository.Record], unchanged; the
	//    Draft stays, so starting again resumes it and records the code again.
	//
	// When code is not nil, the returned Draft's Referral is recorded with
	// it; a nil code records nothing and keeps an existing Referral. The code
	// is not checked against any list or the event's Series.
	Start(ctx context.Context, ticketTypeID entity.TicketTypeID, userID entity.UserID, count int, code *entity.ReferralCode) (*entity.LotteryEntry, error)

	// AuthorizeCard stores the fan's full name and phone number on their User and
	// opens the card authorization for their own Draft, or reuses it, and returns
	// its card authorization secret, which must not be stored. Nothing is
	// captured.
	//
	// # Possible errors
	//
	//  - PermissionDenied: the entry does not exist or is another fan's.
	//  - FailedPrecondition: the entry is not a Draft, or the sale's window is
	//    not open.
	//  - InvalidArgument: the name or phone number is missing or breaks the
	//    personal details rule.
	//  - Unavailable: card payments cannot be reached.
	AuthorizeCard(ctx context.Context, entryID entity.LotteryEntryID, userID entity.UserID, fullName, phoneNumber string) (string, error)

	// Confirm makes the fan's own authorized Draft Entered after verifying its
	// card authorization for the amount, and returns it. An Entered entry is returned
	// unchanged.
	//
	// # Possible errors
	//
	//  - PermissionDenied: the entry does not exist or is another fan's.
	//  - FailedPrecondition: the entry is not an authorized Draft, or the
	//    sale's window is not open.
	//  - Errors of reading the card authorization, unchanged.
	Confirm(ctx context.Context, entryID entity.LotteryEntryID, userID entity.UserID) (*entity.LotteryEntry, error)

	// Withdraw releases the card authorization of the fan's own Draft or Entered entry,
	// when it has one, and then makes it Withdrawn. It works until the draw
	// runs, also after the window has closed.
	//
	// # Possible errors
	//
	//  - NotFound: the entry does not exist.
	//  - PermissionDenied: the entry is another fan's.
	//  - FailedPrecondition: the entry is Won, Lost or Withdrawn.
	//  - Errors of releasing the card authorization, unchanged; the entry keeps its state.
	Withdraw(ctx context.Context, entryID entity.LotteryEntryID, userID entity.UserID) error

	// ListByTicketType returns the fan's entries for the TicketType, in
	// every state, newest first; an empty slice when there is none.
	//
	// # Possible errors
	//
	//  - NotFound: the TicketType does not exist.
	ListByTicketType(ctx context.Context, ticketTypeID entity.TicketTypeID, userID entity.UserID) ([]*entity.LotteryEntry, error)

	// DrawDueSales runs [LotteryEntryUseCase.RunDraw] for every Lottery sale due
	// now. A failed draw is logged and stays due for the next run.
	//
	// # Possible errors
	//
	//  - Errors of listing the due sales, unchanged; nothing is drawn.
	DrawDueSales(ctx context.Context) error

	// RunDraw draws one closed Lottery sale: for each TicketType it releases
	// the card authorizations of its Drafts, draws the Entered entries against the
	// TicketType's quantity in an unpredictable order, captures each winner's
	// card authorization and releases every other, and records the whole sale's outcome
	// with the sale drawn. A winner whose capture fails is released and
	// recorded Lost. It creates no Order or Ticket.
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
	entryRepo                entity.LotteryEntryRepository
	referralRepo             entity.ReferralRepository
	ticketRepo               entity.TicketRepository
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

// NewLotteryEntryUseCase constructs a LotteryEntryUseCase with the given dependencies.
// All parameters are required and must not be nil.
func NewLotteryEntryUseCase(
	saleRepo entity.TicketSaleRepository,
	entryRepo entity.LotteryEntryRepository,
	referralRepo entity.ReferralRepository,
	ticketRepo entity.TicketRepository,
	identityVerificationRepo entity.IdentityVerificationRepository,
	userRepo entity.UserRepository,
	cardAuthorizations entity.CardAuthorizationRepository,
	publisher EventPublisher,
	clock Clock,
	logger *logging.Logger,
) LotteryEntryUseCase {
	return newLotteryUseCase(saleRepo, entryRepo, referralRepo, ticketRepo, identityVerificationRepo, userRepo, cardAuthorizations, publisher, clock, cryptoSeededRand, logger)
}

// newLotteryUseCase constructs the use case with the draw's random source.
func newLotteryUseCase(
	saleRepo entity.TicketSaleRepository,
	entryRepo entity.LotteryEntryRepository,
	referralRepo entity.ReferralRepository,
	ticketRepo entity.TicketRepository,
	identityVerificationRepo entity.IdentityVerificationRepository,
	userRepo entity.UserRepository,
	cardAuthorizations entity.CardAuthorizationRepository,
	publisher EventPublisher,
	clock Clock,
	newRand func() *mrand.Rand,
	logger *logging.Logger,
) *lotteryEntryUseCase {
	return &lotteryEntryUseCase{
		saleRepo:                 saleRepo,
		entryRepo:                entryRepo,
		referralRepo:             referralRepo,
		ticketRepo:               ticketRepo,
		identityVerificationRepo: identityVerificationRepo,
		userRepo:                 userRepo,
		cardAuthorizations:       cardAuthorizations,
		publisher:                publisher,
		clock:                    clock,
		newRand:                  newRand,
		logger:                   logger,
	}
}

// Start implements [LotteryEntryUseCase].
func (uc *lotteryEntryUseCase) Start(ctx context.Context, ticketTypeID entity.TicketTypeID, userID entity.UserID, count int, code *entity.ReferralCode) (*entity.LotteryEntry, error) {
	now := uc.clock()
	sale, tt, err := uc.saleRepo.GetTicketType(ctx, ticketTypeID, now)
	if err != nil {
		return nil, err
	}
	if sale.Method != entity.TicketSaleMethodLottery {
		return nil, apperr.New(codes.FailedPrecondition, "the ticket type is not sold by lottery")
	}
	if err := tt.ValidateTicketCount(count); err != nil {
		return nil, apperr.New(codes.InvalidArgument, err.Error())
	}
	if !sale.IsOpenAt(now) {
		return nil, apperr.New(codes.FailedPrecondition, "the sale's window is not open")
	}
	if err := uc.checkVerification(ctx, sale, userID); err != nil {
		return nil, err
	}
	tickets, err := uc.ticketRepo.ListByUserAndEvent(ctx, userID, tt.EventID)
	if err != nil {
		return nil, err
	}
	held := 0
	for _, t := range tickets {
		if t.Status == entity.TicketStatusIssued {
			held++
		}
	}
	if !tt.FitsPerAccountLimit(held, count) {
		return nil, apperr.New(codes.FailedPrecondition, "the tickets would exceed the per-account limit for the event")
	}
	entry, err := uc.entryRepo.GetOrCreateDraft(ctx, ticketTypeID, userID, count)
	if err != nil {
		if errors.Is(err, apperr.ErrAlreadyExists) {
			return nil, apperr.New(codes.FailedPrecondition, "the fan has already entered this ticket type")
		}
		return nil, err
	}
	if code != nil {
		if err := uc.referralRepo.Record(ctx, entity.ReferralSource{LotteryEntryID: entry.ID}, *code, now); err != nil {
			return nil, err
		}
	}
	return entry, nil
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

// ownEntry reads the entry and fails with PermissionDenied, without revealing
// whether it exists, unless it is the fan's.
func (uc *lotteryEntryUseCase) ownEntry(ctx context.Context, entryID entity.LotteryEntryID, userID entity.UserID) (*entity.LotteryEntry, error) {
	entry, err := uc.entryRepo.Get(ctx, entryID)
	if err != nil {
		if isNotFound(err) {
			return nil, apperr.New(codes.PermissionDenied, "the entry is not the caller's")
		}
		return nil, err
	}
	if entry.UserID != userID {
		return nil, apperr.New(codes.PermissionDenied, "the entry is not the caller's")
	}
	return entry, nil
}

// openTicketType reads the entry's TicketType and its sale and fails with
// FailedPrecondition when the sale's window is not open at now.
func (uc *lotteryEntryUseCase) openTicketType(ctx context.Context, entry *entity.LotteryEntry, now time.Time) (*entity.TicketType, error) {
	sale, tt, err := uc.saleRepo.GetTicketType(ctx, entry.TicketTypeID, now)
	if err != nil {
		return nil, err
	}
	if !sale.IsOpenAt(now) {
		return nil, apperr.New(codes.FailedPrecondition, "the sale's window is not open")
	}
	return tt, nil
}

// AuthorizeCard implements [LotteryEntryUseCase].
func (uc *lotteryEntryUseCase) AuthorizeCard(ctx context.Context, entryID entity.LotteryEntryID, userID entity.UserID, fullName, phoneNumber string) (string, error) {
	entry, err := uc.ownEntry(ctx, entryID, userID)
	if err != nil {
		return "", err
	}
	if entry.State != entity.LotteryEntryStateDraft {
		return "", apperr.New(codes.FailedPrecondition, "only a draft entry can be authorized")
	}
	now := uc.clock()
	tt, err := uc.openTicketType(ctx, entry, now)
	if err != nil {
		return "", err
	}
	if fullName == "" || phoneNumber == "" {
		return "", apperr.New(codes.InvalidArgument, "full name and phone number are required")
	}
	if err := entity.ValidatePersonalDetails(fullName, phoneNumber); err != nil {
		return "", apperr.New(codes.InvalidArgument, err.Error())
	}
	if _, err := uc.userRepo.UpdatePersonalDetails(ctx, string(userID), fullName, phoneNumber); err != nil {
		return "", err
	}
	if entry.HasCardAuthorization() {
		// A declined card retries the same card authorization.
		authorization, err := uc.cardAuthorizations.Get(ctx, entry.CardAuthorizationID)
		if err != nil {
			return "", err
		}
		return authorization.Secret, nil
	}
	authorization, err := uc.cardAuthorizations.Create(ctx, entity.CardAuthorizationPurchase{
		LotteryEntryID: entry.ID,
		TicketTypeID:   entry.TicketTypeID,
		EventID:        tt.EventID,
	}, tt.AmountFor(entry.TicketCount))
	if err != nil {
		return "", err
	}
	if err := uc.entryRepo.SetCardAuthorization(ctx, entry.ID, authorization.ID); err != nil {
		return "", err
	}
	return authorization.Secret, nil
}

// Confirm implements [LotteryEntryUseCase].
func (uc *lotteryEntryUseCase) Confirm(ctx context.Context, entryID entity.LotteryEntryID, userID entity.UserID) (*entity.LotteryEntry, error) {
	entry, err := uc.ownEntry(ctx, entryID, userID)
	if err != nil {
		return nil, err
	}
	if entry.State == entity.LotteryEntryStateEntered {
		return entry, nil
	}
	if !entry.IsConfirmable() {
		return nil, apperr.New(codes.FailedPrecondition, "only an authorized draft entry can be confirmed")
	}
	tt, err := uc.openTicketType(ctx, entry, uc.clock())
	if err != nil {
		return nil, err
	}
	authorization, err := uc.cardAuthorizations.Get(ctx, entry.CardAuthorizationID)
	if err != nil {
		return nil, err
	}
	if err := authorization.Confirm(entity.CardAuthorizationPurchase{LotteryEntryID: entry.ID}, tt.AmountFor(entry.TicketCount)); err != nil {
		return nil, err
	}
	if err := uc.entryRepo.UpdateState(ctx, entry.ID, entity.LotteryEntryStateEntered); err != nil {
		return nil, err
	}
	entry.State = entity.LotteryEntryStateEntered
	return entry, nil
}

// Withdraw implements [LotteryEntryUseCase].
func (uc *lotteryEntryUseCase) Withdraw(ctx context.Context, entryID entity.LotteryEntryID, userID entity.UserID) error {
	entry, err := uc.entryRepo.Get(ctx, entryID)
	if err != nil {
		return err
	}
	if entry.UserID != userID {
		return apperr.New(codes.PermissionDenied, "the entry is not the caller's")
	}
	if !entry.IsWithdrawable() {
		return apperr.New(codes.FailedPrecondition, "the entry can no longer be withdrawn")
	}
	if entry.HasCardAuthorization() {
		if err := uc.cardAuthorizations.Release(ctx, entry.CardAuthorizationID); err != nil {
			return err
		}
	}
	return uc.entryRepo.UpdateState(ctx, entryID, entity.LotteryEntryStateWithdrawn)
}

// ListByTicketType implements [LotteryEntryUseCase].
func (uc *lotteryEntryUseCase) ListByTicketType(ctx context.Context, ticketTypeID entity.TicketTypeID, userID entity.UserID) ([]*entity.LotteryEntry, error) {
	if _, _, err := uc.saleRepo.GetTicketType(ctx, ticketTypeID, uc.clock()); err != nil {
		return nil, err
	}
	return uc.entryRepo.ListByTicketTypeAndUser(ctx, ticketTypeID, userID)
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
	sale, err := uc.saleRepo.Get(ctx, saleID, uc.clock())
	if err != nil {
		return err
	}

	var (
		winners []entity.DrawWinnerRow
		losers  []entity.DrawLoserRow
		drafts  []entity.LotteryEntryID
		results []entity.LotteryEntryDrawnData
	)
	drawTime := uc.clock()
	for _, tt := range sale.TicketTypes {
		entries, err := uc.entryRepo.ListUndrawnForTicketType(ctx, tt.ID)
		if err != nil {
			return err
		}
		refs := make(map[entity.LotteryEntryID]string, len(entries))
		users := make(map[entity.LotteryEntryID]entity.UserID, len(entries))
		var entered []entity.DrawEntry
		for _, e := range entries {
			switch e.State {
			case entity.LotteryEntryStateDraft:
				drafts = append(drafts, e.ID)
				if e.HasCardAuthorization() {
					uc.release(ctx, saleID, e.ID, e.CardAuthorizationID)
				}
			case entity.LotteryEntryStateEntered:
				refs[e.ID] = e.CardAuthorizationID
				users[e.ID] = e.UserID
				entered = append(entered, entity.DrawEntry{ID: e.ID, TicketCount: e.TicketCount})
			}
		}

		drawn := func(id entity.LotteryEntryID, state entity.LotteryEntryState, captureFailed bool) {
			results = append(results, entity.LotteryEntryDrawnData{
				LotteryEntryID: string(id),
				UserID:         string(users[id]),
				TicketSaleID:   string(saleID),
				TicketTypeID:   string(tt.ID),
				Result:         state.String(),
				CaptureFailed:  captureFailed,
				DrawTime:       drawTime,
			})
		}

		result := entity.RunLotteryDraw(entered, tt.Quantity, uc.newRand())
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
				losers = append(losers, entity.DrawLoserRow{LotteryEntryID: w.Entry.ID, CardAuthorizationID: ref, DrawPosition: w.DrawPosition})
				drawn(w.Entry.ID, entity.LotteryEntryStateLost, true)
				continue
			}
			winners = append(winners, entity.DrawWinnerRow{LotteryEntryID: w.Entry.ID, CardAuthorizationID: ref, DrawPosition: w.DrawPosition})
			drawn(w.Entry.ID, entity.LotteryEntryStateWon, false)
		}
		for _, l := range result.Losers {
			ref := refs[l.Entry.ID]
			uc.release(ctx, saleID, l.Entry.ID, ref)
			losers = append(losers, entity.DrawLoserRow{LotteryEntryID: l.Entry.ID, CardAuthorizationID: ref, DrawPosition: l.DrawPosition})
			drawn(l.Entry.ID, entity.LotteryEntryStateLost, false)
		}
	}

	if err := uc.entryRepo.PersistDrawOutcome(ctx, saleID, winners, losers, drafts); err != nil {
		return err
	}
	uc.logger.Info(ctx, "draw completed", analyticslog.Attrs(analyticslog.TopicPayment,
		slog.String("ticket_sale_id", string(saleID)),
		slog.Int("winner_count", len(winners)),
		slog.Int("loser_count", len(losers)),
		slog.Int("withdrawn_draft_count", len(drafts)),
	)...)

	// Report each entry's result for product analytics. A failed publish is
	// logged and never changes the recorded draw.
	for _, r := range results {
		if err := uc.publisher.PublishEventWithID(ctx, entity.SubjectLotteryEntryDrawn, r.LotteryEntryID, r); err != nil {
			uc.logger.Error(ctx, "failed to publish ORDER.lottery_entry_drawn event", err,
				slog.String("ticket_sale_id", string(saleID)),
				slog.String("lottery_entry_id", r.LotteryEntryID),
			)
		}
	}
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
