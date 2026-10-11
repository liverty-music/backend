package usecase

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"

	"github.com/liverty-music/backend/internal/entity"
)

// EventOrganizerRepository resolves the Organizer that owns an event, via its
// Series, so the issuance path can stamp the denormalized OrganizerID on the
// Held Settlement it creates alongside the Order (backend#468). A minimal
// read-only interface so IssuanceUseCase does not depend on the full
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
// before every run of the stalled-checkout job reports it to an operator.
const unissuedChargeReportAge = 10 * time.Minute

// LogKeyNeedsOperator is the log message of a purchase that needs an operator:
// money was taken without tickets. A log-based alert on it pages the operator.
// The alert in cloud-provisioning (monitoring.ts) matches this exact text, so
// it keeps its first-come-ticket-sales wording until the alert changes with it.
const LogKeyNeedsOperator = "reservation needs an operator"

// IssuanceUseCase is ⑤'s post-capture pipeline: it turns a captured payment —
// a won lottery entry's, or a first-come checkout's after committing and
// charging it — into an Order plus N account-bound covered tickets and a Held
// Settlement for the event's Organizer, together with the Order's ORDER.paid
// announcement. It owns issuance idempotency: one Order per source.
type IssuanceUseCase interface {
	// IssueFromCapturedWin creates the Order from ④'s captured winning payment,
	// issues the N account-bound covered tickets for the won lottery entry
	// (bound to the user's IdentityVerification when the sale requires verification) and
	// records a Held Settlement (one split paying the event's Organizer at its
	// platform fee rate).
	//
	// It is IDEMPOTENT: a replayed Won-captured signal (or a retry) creates the
	// Order once and issues tickets exactly once for the same entry — on a
	// replay it returns the already-created Order unchanged.
	//
	// # Possible errors
	//
	//  - FailedPrecondition: the entry is not Won, or its payment is not
	//    captured — no Order is created.
	//  - NotFound: no entry with the given ID exists, the event's Organizer
	//    cannot be resolved, or the sale requires verification and the user
	//    has no IdentityVerification — no Order is created.
	//  - Internal: persistence failure after capture.
	IssueFromCapturedWin(ctx context.Context, lotteryEntryID entity.LotteryEntryID) (*entity.Order, error)

	// IssueDueWins issues Orders + tickets for every Won lottery entry that
	// does not yet have an Order. A per-entry failure is logged and skipped so
	// one bad entry never blocks the rest of the sweep.
	//
	// # Possible errors
	//
	//  - Internal: the work-list query failed.
	IssueDueWins(ctx context.Context) error

	// IssueFromCheckout finishes a checkout: while its hold lasts it commits
	// the held tickets, charges the card once and issues the Order and its
	// tickets, bound to the user's IdentityVerification when the sale requires verification.
	// caller is the fan placing the order, or nil for the stalled-checkout job
	// and the payment webhook. Calls for the same Checkout run one after the
	// other, and a Checkout that already has an Order returns it.
	//
	// # Possible errors
	//
	//  - PermissionDenied: the Checkout does not exist or is another fan's
	//    (non-revealing; only with a caller).
	//  - FailedPrecondition: the hold lapsed, the concert is not published, the
	//    card was not authenticated, the commit found the Checkout not
	//    holding, or the card could no longer be charged (the commit is given
	//    back). Nothing is charged.
	//  - NotFound: the sale requires verification and the user has no IdentityVerification.
	//    Nothing is charged.
	//  - Unavailable: the charge's outcome is not known yet, or issuance failed
	//    after the charge; the stalled-checkout job finishes it.
	IssueFromCheckout(ctx context.Context, checkoutID entity.CheckoutID, caller *entity.UserID) (*entity.Order, error)

	// IssueDueCheckouts runs IssueFromCheckout for every Committed Checkout
	// committed more than 1 minute ago, and reports each charged one still
	// unissued 10 minutes after its charge as needing an operator.
	//
	// # Possible errors
	//
	//  - The listing's error; nothing is issued then.
	IssueDueCheckouts(ctx context.Context) error

	// FulfillPayment turns a charge the payment provider reports as completed
	// into an issued Order and announces it as ORDER.paid, with the Order id
	// as the message id. It finds the charge's source — a Checkout by its
	// card hold, else a LotteryEntry by its authorization — and runs the
	// idempotent issuance for it, so a repeated report finds the same Order. A
	// charge of no source succeeds without effect.
	//
	// # Possible errors
	//
	//  - The issuance's or the announcement's error, unchanged; the provider
	//    reports the charge again.
	FulfillPayment(ctx context.Context, paymentRef string) error
}

// issuanceUseCase implements [IssuanceUseCase].
type issuanceUseCase struct {
	issuanceRepo             entity.IssuanceRepository
	orderRepo                entity.OrderRepository
	entryRepo                entity.LotteryEntryRepository
	eventOrganizerRepo       EventOrganizerRepository
	organizerRepo            entity.OrganizerRepository
	identityVerificationRepo entity.IdentityVerificationRepository
	capturePort              entity.PaymentCapturePort
	checkoutRepo             entity.CheckoutRepository
	ticketSaleRepo           entity.TicketSaleRepository
	eventState               EventPublishStatePort
	checkoutAuth             entity.CheckoutAuthorizationPort
	publisher                EventPublisher
	clock                    Clock
	logger                   *logging.Logger
}

// Compile-time interface compliance check.
var _ IssuanceUseCase = (*issuanceUseCase)(nil)

// IssuanceDeps are the dependencies of [NewIssuanceUseCase]. All are required.
type IssuanceDeps struct {
	IssuanceRepo             entity.IssuanceRepository
	OrderRepo                entity.OrderRepository
	EntryRepo                entity.LotteryEntryRepository
	EventOrganizerRepo       EventOrganizerRepository
	OrganizerRepo            entity.OrganizerRepository
	IdentityVerificationRepo entity.IdentityVerificationRepository
	CapturePort              entity.PaymentCapturePort
	CheckoutRepo             entity.CheckoutRepository
	TicketSaleRepo           entity.TicketSaleRepository
	EventState               EventPublishStatePort
	CheckoutAuth             entity.CheckoutAuthorizationPort
	Publisher                EventPublisher
	Clock                    Clock
	Logger                   *logging.Logger
}

// NewIssuanceUseCase constructs an IssuanceUseCase with the given dependencies.
func NewIssuanceUseCase(d IssuanceDeps) IssuanceUseCase {
	return &issuanceUseCase{
		issuanceRepo:             d.IssuanceRepo,
		orderRepo:                d.OrderRepo,
		entryRepo:                d.EntryRepo,
		eventOrganizerRepo:       d.EventOrganizerRepo,
		organizerRepo:            d.OrganizerRepo,
		identityVerificationRepo: d.IdentityVerificationRepo,
		capturePort:              d.CapturePort,
		checkoutRepo:             d.CheckoutRepo,
		ticketSaleRepo:           d.TicketSaleRepo,
		eventState:               d.EventState,
		checkoutAuth:             d.CheckoutAuth,
		publisher:                d.Publisher,
		clock:                    d.Clock,
		logger:                   d.Logger,
	}
}

// issuanceSource is everything the shared issuance core needs from a source:
// who bought what for which event, and the captured payment.
type issuanceSource struct {
	lotteryEntryID         entity.LotteryEntryID
	checkoutID             entity.CheckoutID
	userID                 entity.UserID
	eventID                string
	ticketCount            int
	identityVerificationID string
	amount                 int64
	currency               string
	payment                entity.Payment
}

// issue is the shared issuance core: it resolves the event's Organizer and its
// fee rate, builds the Paid Order, the N Issued tickets and the Held
// Settlement, and stores them through IssuanceRepository.Issue. A duplicate
// re-reads and returns the Order of the same source.
func (uc *issuanceUseCase) issue(ctx context.Context, src issuanceSource) (*entity.Order, error) {
	organizerID, err := uc.eventOrganizerRepo.GetOrganizerID(ctx, src.eventID)
	if err != nil {
		return nil, err
	}
	organizer, err := uc.organizerRepo.Get(ctx, organizerID)
	if err != nil {
		return nil, err
	}

	now := uc.clock()
	order := &entity.Order{
		ID:             entity.OrderID(entity.NewID()),
		UserID:         src.userID,
		LotteryEntryID: src.lotteryEntryID,
		CheckoutID:     src.checkoutID,
		Payment:        src.payment,
		Status:         entity.OrderStatusPaid,
		Amount:         src.amount,
		Currency:       src.currency,
		PayTime:        now,
	}
	tickets := make([]*entity.Ticket, 0, src.ticketCount)
	for range src.ticketCount {
		tickets = append(tickets, &entity.Ticket{
			ID:                             entity.TicketID(entity.NewID()),
			OrderID:                        order.ID,
			UserID:                         src.userID,
			EventID:                        src.eventID,
			IdentityVerificationID:         src.identityVerificationID,
			ResaleWithoutConsentProhibited: true,
			Status:                         entity.TicketStatusIssued,
			IssueTime:                      now,
		})
	}
	settlement := entity.NewHeldSettlement(order, organizerID, src.eventID, organizer.PlatformFeeRateBps, now)

	if err := uc.issuanceRepo.Issue(ctx, order, tickets, settlement); err != nil {
		if !errors.Is(err, apperr.ErrAlreadyExists) {
			return nil, err
		}
		if src.checkoutID != "" {
			return uc.orderRepo.GetByCheckoutID(ctx, src.checkoutID)
		}
		return uc.orderRepo.GetByLotteryEntryID(ctx, src.lotteryEntryID)
	}

	uc.logger.Info(ctx, "issued order and tickets",
		slog.String("order_id", string(order.ID)),
		slog.String("lottery_entry_id", string(src.lotteryEntryID)),
		slog.String("checkout_id", string(src.checkoutID)),
		slog.Int("ticket_count", src.ticketCount),
		slog.Int("platform_fee_rate_bps", organizer.PlatformFeeRateBps),
	)
	return order, nil
}

// kycBinding returns the id of the IdentityVerification the Tickets of a sale are bound to:
// the user's IdentityVerification, whatever its status, when the sale requires verification;
// empty otherwise. The user's missing IdentityVerification is returned as its error.
func (uc *issuanceUseCase) identityVerificationBinding(ctx context.Context, sale *entity.TicketSale, userID entity.UserID) (string, error) {
	if !sale.VerificationRequirement.RequiresVerification() {
		return "", nil
	}
	identityVerification, err := uc.identityVerificationRepo.GetByUserID(ctx, string(userID))
	if err != nil {
		return "", err
	}
	return identityVerification.ID, nil
}

// IssueFromCapturedWin implements [IssuanceUseCase].
func (uc *issuanceUseCase) IssueFromCapturedWin(ctx context.Context, lotteryEntryID entity.LotteryEntryID) (*entity.Order, error) {
	// -- idempotency: an Order already created for this entry means issuance
	//    already ran; return it without re-issuing. --
	if existing, err := uc.orderRepo.GetByLotteryEntryID(ctx, lotteryEntryID); err == nil {
		return existing, nil
	} else if !errors.Is(err, apperr.ErrNotFound) {
		return nil, err
	}

	// -- load the entry; it must be Won to issue. --
	entry, err := uc.entryRepo.Get(ctx, lotteryEntryID)
	if err != nil {
		return nil, err // propagates NotFound
	}
	if entry.State != entity.LotteryEntryStateWon {
		return nil, apperr.New(codes.FailedPrecondition,
			"lottery entry is not Won; no Order is created and no ticket is issued",
			slog.String("lottery_entry_id", string(lotteryEntryID)))
	}

	// -- load the ticket type and its sale for the event and the verification
	//    requirement. --
	sale, ticketType, err := uc.ticketSaleRepo.GetTicketType(ctx, entry.TicketTypeID, uc.clock())
	if err != nil {
		return nil, err
	}

	// -- read ④'s captured payment for the authoritative amount/currency + facets. --
	captured, err := uc.capturePort.GetCapturedPayment(ctx, entry.CardAuthorizationID)
	if err != nil {
		return nil, err
	}

	// -- where the sale requires verification, bind every ticket to the
	//    user's IdentityVerification. --
	identityVerificationID, err := uc.identityVerificationBinding(ctx, sale, entry.UserID)
	if err != nil {
		return nil, err
	}

	return uc.issue(ctx, issuanceSource{
		lotteryEntryID:         entry.ID,
		userID:                 entry.UserID,
		eventID:                ticketType.EventID,
		ticketCount:            entry.TicketCount,
		identityVerificationID: identityVerificationID,
		amount:                 captured.AmountJPY,
		currency:               captured.Currency,
		payment: entity.Payment{
			Provider:          captured.Provider,
			ProviderPaymentID: entry.CardAuthorizationID,
			CardBrand:         captured.CardBrand,
			CardLast4:         captured.CardLast4,
		},
	})
}

// IssueDueWins implements [IssuanceUseCase].
func (uc *issuanceUseCase) IssueDueWins(ctx context.Context) error {
	ids, err := uc.issuanceRepo.ListLotteryEntryIDsAwaitingIssuance(ctx)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if _, err := uc.IssueFromCapturedWin(ctx, id); err != nil {
			// Per-entry failure must not block the rest of the sweep. The next
			// tick retries (IssueFromCapturedWin is idempotent).
			uc.logger.Warn(ctx, "issuance sweep: failed to issue lottery entry; skipping",
				slog.String("lottery_entry_id", string(id)),
				slog.Any("error", err),
			)
		}
	}
	return nil
}

// IssueFromCheckout implements [IssuanceUseCase]. The whole call runs under
// the Checkout's lock, so a double tap or the stalled-checkout job waits for
// the first call and then takes the idempotent path.
func (uc *issuanceUseCase) IssueFromCheckout(ctx context.Context, checkoutID entity.CheckoutID, caller *entity.UserID) (*entity.Order, error) {
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

func (uc *issuanceUseCase) issueFromCheckout(ctx context.Context, checkoutID entity.CheckoutID, caller *entity.UserID) (*entity.Order, error) {
	attrs := slog.String("checkout_id", string(checkoutID))

	// 1. Ownership first, without revealing whether the Checkout exists.
	checkout, err := uc.checkoutRepo.Get(ctx, checkoutID)
	if err != nil {
		if caller != nil && errors.Is(err, apperr.ErrNotFound) {
			return nil, apperr.New(codes.PermissionDenied, "checkout is not the caller's", attrs)
		}
		return nil, err
	}
	if caller != nil && checkout.UserID != *caller {
		return nil, apperr.New(codes.PermissionDenied, "checkout is not the caller's", attrs)
	}

	// 2. An existing Order ends the call.
	if existing, err := uc.orderRepo.GetByCheckoutID(ctx, checkoutID); err == nil {
		return existing, nil
	} else if !errors.Is(err, apperr.ErrNotFound) {
		return nil, err
	}

	now := uc.clock()
	sale, ticketType, err := uc.ticketSaleRepo.GetTicketType(ctx, checkout.TicketTypeID, now)
	if err != nil {
		return nil, err
	}

	if checkout.Status == entity.CheckoutStatusHeld {
		// 3. Holding, published and authorized, checked before the card.
		if !checkout.IsHoldingAt(now) {
			return nil, apperr.New(codes.FailedPrecondition, "the hold has lapsed", attrs)
		}
		published, err := uc.eventState.IsEventPublished(ctx, ticketType.EventID)
		if err != nil {
			return nil, err
		}
		if !published {
			return nil, apperr.New(codes.FailedPrecondition, "the concert is not published", attrs)
		}
		if checkout.CardAuthorizationID == "" {
			return nil, apperr.New(codes.FailedPrecondition, "the card was not authorized", attrs)
		}
	}

	// The IdentityVerification binding is read before the commit and the charge, so a user
	// without a IdentityVerification is never charged.
	identityVerificationID, err := uc.identityVerificationBinding(ctx, sale, checkout.UserID)
	if err != nil {
		return nil, err
	}

	if checkout.Status == entity.CheckoutStatusHeld {
		// 4. The hold must be authenticated and for the Checkout's amount.
		if err := uc.checkoutAuth.VerifyAuthorization(ctx, checkout.CardAuthorizationID, checkout.Amount); err != nil {
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
		if _, err := uc.checkoutAuth.CaptureAuthorization(ctx, checkout.CardAuthorizationID); err != nil {
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

	// The charge is the card hold; its facets are read back from the provider,
	// as for a lottery win.
	captured, err := uc.capturePort.GetCapturedPayment(ctx, checkout.CardAuthorizationID)
	if err != nil {
		return nil, err
	}
	return uc.issue(ctx, issuanceSource{
		checkoutID:             checkout.ID,
		userID:                 checkout.UserID,
		eventID:                ticketType.EventID,
		ticketCount:            checkout.TicketCount,
		identityVerificationID: identityVerificationID,
		amount:                 checkout.Amount,
		currency:               "JPY",
		payment: entity.Payment{
			Provider:          captured.Provider,
			ProviderPaymentID: checkout.CardAuthorizationID,
			CardBrand:         captured.CardBrand,
			CardLast4:         captured.CardLast4,
		},
	})
}

// IssueDueCheckouts implements [IssuanceUseCase].
func (uc *issuanceUseCase) IssueDueCheckouts(ctx context.Context) error {
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
				slog.String("payment_ref", checkout.CardAuthorizationID),
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

// FulfillPayment implements [IssuanceUseCase].
func (uc *issuanceUseCase) FulfillPayment(ctx context.Context, paymentRef string) error {
	paid, err := uc.fulfill(ctx, paymentRef)
	if err != nil || paid == nil {
		return err
	}
	if err := uc.publisher.PublishEventWithID(ctx, entity.SubjectOrderPaid, paid.OrderID, paid); err != nil {
		return fmt.Errorf("announce order %s as paid: %w", paid.OrderID, err)
	}
	uc.logger.Info(ctx, "order announced as paid",
		slog.String("order_id", paid.OrderID),
		slog.String("payment_ref", paymentRef),
	)
	return nil
}

// fulfill issues, or finds, the Order of the charge's source and returns its
// announcement; nil when the charge has no source here.
func (uc *issuanceUseCase) fulfill(ctx context.Context, paymentRef string) (*entity.OrderPaidData, error) {
	checkout, err := uc.checkoutRepo.GetByCardAuthorizationID(ctx, paymentRef)
	switch {
	case err == nil:
		order, err := uc.IssueFromCheckout(ctx, checkout.ID, nil)
		if err != nil {
			return nil, err
		}
		_, ticketType, err := uc.ticketSaleRepo.GetTicketType(ctx, checkout.TicketTypeID, uc.clock())
		if err != nil {
			return nil, err
		}
		return orderPaid(order, ticketType.EventID, checkout.TicketCount), nil
	case !errors.Is(err, apperr.ErrNotFound):
		return nil, err
	}

	entry, err := uc.entryRepo.GetByCardAuthorizationID(ctx, paymentRef)
	if errors.Is(err, apperr.ErrNotFound) {
		uc.logger.Info(ctx, "completed charge of no checkout or lottery entry; nothing to fulfill",
			slog.String("payment_ref", paymentRef))
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	order, err := uc.IssueFromCapturedWin(ctx, entry.ID)
	if err != nil {
		return nil, err
	}
	_, ticketType, err := uc.ticketSaleRepo.GetTicketType(ctx, entry.TicketTypeID, uc.clock())
	if err != nil {
		return nil, err
	}
	return orderPaid(order, ticketType.EventID, entry.TicketCount), nil
}

// orderPaid builds the ORDER.paid payload of an issued Order.
func orderPaid(order *entity.Order, eventID string, ticketCount int) *entity.OrderPaidData {
	return &entity.OrderPaidData{
		OrderID:        string(order.ID),
		UserID:         string(order.UserID),
		EventID:        eventID,
		TicketCount:    ticketCount,
		Amount:         order.Amount,
		Currency:       order.Currency,
		LotteryEntryID: string(order.LotteryEntryID),
		CheckoutID:     string(order.CheckoutID),
	}
}
