package usecase

import (
	"context"
	"errors"
	"log/slog"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"

	"github.com/liverty-music/backend/internal/entity"
)

// CapturedPayment is the read-back of ④'s captured winning payment from the
// provider: the authoritative amount/currency actually captured plus the
// display-only card facets. It NEVER carries raw card data (no PAN/CVC/expiry).
// Returned by [PaymentCapturePort.GetCapturedPayment].
type CapturedPayment struct {
	// Provider backs the captured payment (Stripe for the MVP).
	Provider entity.PaymentProvider
	// AmountJPY is the captured amount in whole yen.
	AmountJPY int64
	// Currency is the ISO 4217 code of the captured amount (JPY for the MVP).
	Currency string
	// CardBrand is a display-only brand facet (e.g. "visa"). May be empty.
	CardBrand string
	// CardLast4 is the display-only last four digits. May be empty.
	CardLast4 string
}

// PaymentCapturePort reads a captured payment's authoritative details from the
// provider. It is the ⑤-side read counterpart to ④'s
// [PaymentAuthorizationPort] (which owns authorize/capture/cancel). Defined here
// (consumer package) per Clean Architecture; the Stripe implementation lives in
// internal/infrastructure/payment/.
type PaymentCapturePort interface {
	// GetCapturedPayment retrieves the captured PaymentIntent referenced by
	// paymentIntentRef and returns its amount, currency, and display facets. ⑤
	// does NOT capture or charge — ④ already captured; this only reads.
	//
	// # Possible errors
	//
	//  - FailedPrecondition: the payment intent is not in a captured/succeeded
	//    state (④'s capture has not settled).
	//  - NotFound: the payment intent does not exist.
	//  - Unavailable: the payment provider is unreachable.
	GetCapturedPayment(ctx context.Context, paymentIntentRef string) (*CapturedPayment, error)
}

// IssuanceUseCase is ⑤'s post-capture pipeline: it turns ④'s Won-captured
// winning application into an Order plus N account-bound covered tickets, and
// sets the buyer's ticket-journey to PAID. It owns issuance idempotency.
type IssuanceUseCase interface {
	// IssueFromCapturedWin creates the Order from ④'s captured winning payment
	// and issues the N account-bound covered tickets for the winning application,
	// then sets the buyer's ticket-journey for the event to PAID.
	//
	// It is IDEMPOTENT: a replayed Won-captured signal (or a retry) creates the
	// Order once and issues tickets exactly once for the same application — on a
	// replay it returns the already-created Order unchanged.
	//
	// # Possible errors
	//
	//  - FailedPrecondition: the application is not Won-captured (lost, withdrawn,
	//    still applied, or its capture never succeeded) — no Order is created.
	//  - NotFound: no application with the given ID exists.
	//  - Internal: persistence failure after capture (the caller reconciles by
	//    refunding the captured payment; see the capture-succeeded/issuance-failed
	//    path in the design).
	IssueFromCapturedWin(ctx context.Context, applicationID entity.TicketApplicationID) (*entity.Order, error)
}

// issuanceUseCase implements [IssuanceUseCase].
type issuanceUseCase struct {
	issuanceRepo         entity.IssuanceRepository
	orderRepo            entity.OrderRepository
	appRepo              entity.TicketApplicationRepository
	phaseRepo            entity.LotteryPhaseRepository
	verifiedIdentityRepo entity.VerifiedIdentityRepository
	journeyRepo          entity.TicketJourneyRepository
	capturePort          PaymentCapturePort
	clock                Clock
	logger               *logging.Logger
}

// Compile-time interface compliance check.
var _ IssuanceUseCase = (*issuanceUseCase)(nil)

// NewIssuanceUseCase constructs an IssuanceUseCase with the given dependencies.
// All parameters are required and must not be nil.
func NewIssuanceUseCase(
	issuanceRepo entity.IssuanceRepository,
	orderRepo entity.OrderRepository,
	appRepo entity.TicketApplicationRepository,
	phaseRepo entity.LotteryPhaseRepository,
	verifiedIdentityRepo entity.VerifiedIdentityRepository,
	journeyRepo entity.TicketJourneyRepository,
	capturePort PaymentCapturePort,
	clock Clock,
	logger *logging.Logger,
) IssuanceUseCase {
	return &issuanceUseCase{
		issuanceRepo:         issuanceRepo,
		orderRepo:            orderRepo,
		appRepo:              appRepo,
		phaseRepo:            phaseRepo,
		verifiedIdentityRepo: verifiedIdentityRepo,
		journeyRepo:          journeyRepo,
		capturePort:          capturePort,
		clock:                clock,
		logger:               logger,
	}
}

// IssueFromCapturedWin implements [IssuanceUseCase].
func (uc *issuanceUseCase) IssueFromCapturedWin(ctx context.Context, applicationID entity.TicketApplicationID) (*entity.Order, error) {
	// -- idempotency: an Order already created for this application means issuance
	//    already ran; return it without re-issuing. --
	if existing, err := uc.orderRepo.GetByApplicationID(ctx, applicationID); err == nil {
		return existing, nil
	} else if !errors.Is(err, apperr.ErrNotFound) {
		return nil, err
	}

	// -- load the application; it must be Won-captured to issue. --
	app, err := uc.appRepo.Get(ctx, applicationID)
	if err != nil {
		return nil, err // propagates NotFound
	}
	if app.State != entity.TicketApplicationStateWon {
		return nil, apperr.New(codes.FailedPrecondition,
			"application is not Won-captured; no Order is created and no ticket is issued")
	}

	// -- load the phase for the event reference and verification requirement. --
	phase, err := uc.phaseRepo.Get(ctx, app.PhaseID)
	if err != nil {
		return nil, err
	}

	// -- read ④'s captured payment for the authoritative amount/currency + facets. --
	captured, err := uc.capturePort.GetCapturedPayment(ctx, app.Authorization.PaymentIntentRef)
	if err != nil {
		return nil, err
	}

	// -- resolve the covered-ticket 本人確認 binding. Where the phase required
	//    identity verification, the verified identity is authoritative: record the
	//    VerifiedIdentity link on every ticket. The displayed name stays ④'s
	//    self-declared value (the backend VerifiedIdentity intentionally stores no
	//    基本4情報 name); ④ already gated the application on the verified person, so
	//    there is no conflicting self-declared name to reconcile here. --
	verifiedIdentityID := ""
	if phase.VerificationRequirement.RequiresVerification() {
		vi, err := uc.verifiedIdentityRepo.GetByUserID(ctx, string(app.ApplicantID))
		if err != nil {
			return nil, err // a required-verification phase must have a verified identity
		}
		verifiedIdentityID = vi.ID
	}

	// -- build the Order (Paid on creation) and the N covered tickets. --
	now := uc.clock()
	order := &entity.Order{
		ID:            entity.OrderID(entity.NewID()),
		BuyerID:       app.ApplicantID,
		ApplicationID: app.ID,
		Payment: entity.Payment{
			Provider:         captured.Provider,
			PaymentIntentRef: app.Authorization.PaymentIntentRef,
			CardBrand:        captured.CardBrand,
			CardLast4:        captured.CardLast4,
		},
		Status:   entity.OrderStatusPaid,
		Amount:   captured.AmountJPY,
		Currency: captured.Currency,
		PaidTime: now,
	}

	tickets := make([]*entity.Ticket, 0, app.RequestedTicketCount)
	for range app.RequestedTicketCount {
		tickets = append(tickets, &entity.Ticket{
			ID:                             entity.TicketID(entity.NewID()),
			OrderID:                        order.ID,
			HolderID:                       app.ApplicantID,
			EventID:                        phase.EventID,
			HolderIdentity:                 app.Identity,
			VerifiedIdentityID:             verifiedIdentityID,
			ResaleWithoutConsentProhibited: true,
			Status:                         entity.TicketStatusIssued,
			IssuedTime:                     now,
		})
	}

	// -- atomic write: Order + N tickets in one transaction. A concurrent issuance
	//    that won the race surfaces as AlreadyExists; re-read and return (idempotent). --
	if err := uc.issuanceRepo.Issue(ctx, order, tickets); err != nil {
		if errors.Is(err, apperr.ErrAlreadyExists) {
			return uc.orderRepo.GetByApplicationID(ctx, applicationID)
		}
		return nil, err
	}

	// -- first-party authoritative side effect: set ticket-journey to PAID. This
	//    supersedes any scraped/self-reported value. Non-fatal: the tickets are
	//    already issued, so a journey-write failure is logged, not rolled back. --
	if err := uc.journeyRepo.Upsert(ctx, &entity.TicketJourney{
		UserID:  string(app.ApplicantID),
		EventID: phase.EventID,
		Status:  entity.TicketJourneyStatusPaid,
	}); err != nil {
		uc.logger.Error(ctx, "failed to set ticket-journey to PAID after issuance", err,
			slog.String("order_id", string(order.ID)),
			slog.String("user_id", string(app.ApplicantID)),
			slog.String("event_id", phase.EventID),
		)
	}

	uc.logger.Info(ctx, "issued order and tickets from captured win",
		slog.String("order_id", string(order.ID)),
		slog.String("application_id", string(app.ID)),
		slog.Int("ticket_count", app.RequestedTicketCount),
	)
	return order, nil
}
