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
const LogKeyNeedsOperator = "reservation needs an operator"

// IssuanceUseCase is ⑤'s post-capture pipeline: it turns a captured payment —
// a won lottery application's, or a first-come checkout's after committing and
// charging it — into an Order plus N account-bound covered tickets and a Held
// Settlement for the event's Organizer, together with the Order's ORDER.paid
// announcement. It owns issuance idempotency: one Order per source.
type IssuanceUseCase interface {
	// IssueFromCapturedWin creates the Order from ④'s captured winning payment,
	// issues the N account-bound covered tickets for the winning application
	// and records a Held Settlement (one split paying the event's Organizer at
	// its platform fee rate).
	//
	// It is IDEMPOTENT: a replayed Won-captured signal (or a retry) creates the
	// Order once and issues tickets exactly once for the same application — on a
	// replay it returns the already-created Order unchanged.
	//
	// # Possible errors
	//
	//  - FailedPrecondition: the application is not Won-captured — no Order is
	//    created.
	//  - NotFound: no application with the given ID exists, or the event's
	//    Organizer cannot be resolved — no Order is created.
	//  - Internal: persistence failure after capture.
	IssueFromCapturedWin(ctx context.Context, applicationID entity.TicketApplicationID) (*entity.Order, error)

	// IssueDueWins issues Orders + tickets for every Won-captured application
	// that does not yet have an Order. A per-application failure is logged and
	// skipped so one bad application never blocks the rest of the sweep.
	//
	// # Possible errors
	//
	//  - Internal: the work-list query failed.
	IssueDueWins(ctx context.Context) error

	// IssueFromReservation finishes a checkout: while its hold lasts it commits
	// the held tickets, charges the card once and issues the Order and its
	// tickets. caller is the fan placing the order, or nil for the
	// stalled-checkout job. Calls for the same Reservation run one after the
	// other, and a Reservation that already has an Order returns it.
	//
	// # Possible errors
	//
	//  - PermissionDenied: the Reservation does not exist or is another fan's
	//    (non-revealing; only with a caller).
	//  - FailedPrecondition: the hold lapsed, the concert is not published, the
	//    card was not authenticated, the commit found the Reservation not
	//    holding, or the card could no longer be charged (the commit is given
	//    back). Nothing is charged.
	//  - Unavailable: the charge's outcome is not known yet, or issuance failed
	//    after the charge; the stalled-checkout job finishes it.
	IssueFromReservation(ctx context.Context, reservationID entity.ReservationID, caller *entity.UserID) (*entity.Order, error)

	// IssueDueReservations runs IssueFromReservation for every Committed
	// Reservation committed more than 1 minute ago, and reports each charged
	// one still unissued 10 minutes after its charge as needing an operator.
	//
	// # Possible errors
	//
	//  - The listing's error; nothing is issued then.
	IssueDueReservations(ctx context.Context) error
}

// issuanceUseCase implements [IssuanceUseCase].
type issuanceUseCase struct {
	issuanceRepo         entity.IssuanceRepository
	orderRepo            entity.OrderRepository
	appRepo              entity.TicketApplicationRepository
	phaseRepo            entity.LotteryPhaseRepository
	eventOrganizerRepo   EventOrganizerRepository
	organizerRepo        entity.OrganizerRepository
	verifiedIdentityRepo entity.VerifiedIdentityRepository
	capturePort          entity.PaymentCapturePort
	reservationRepo      entity.ReservationRepository
	ticketSaleRepo       entity.TicketSaleRepository
	eventState           EventPublishStatePort
	reservationAuth      entity.ReservationAuthorizationPort
	clock                Clock
	logger               *logging.Logger
}

// Compile-time interface compliance check.
var _ IssuanceUseCase = (*issuanceUseCase)(nil)

// IssuanceDeps are the dependencies of [NewIssuanceUseCase]. All are required.
type IssuanceDeps struct {
	IssuanceRepo         entity.IssuanceRepository
	OrderRepo            entity.OrderRepository
	AppRepo              entity.TicketApplicationRepository
	PhaseRepo            entity.LotteryPhaseRepository
	EventOrganizerRepo   EventOrganizerRepository
	OrganizerRepo        entity.OrganizerRepository
	VerifiedIdentityRepo entity.VerifiedIdentityRepository
	CapturePort          entity.PaymentCapturePort
	ReservationRepo      entity.ReservationRepository
	TicketSaleRepo       entity.TicketSaleRepository
	EventState           EventPublishStatePort
	ReservationAuth      entity.ReservationAuthorizationPort
	Clock                Clock
	Logger               *logging.Logger
}

// NewIssuanceUseCase constructs an IssuanceUseCase with the given dependencies.
func NewIssuanceUseCase(d IssuanceDeps) IssuanceUseCase {
	return &issuanceUseCase{
		issuanceRepo:         d.IssuanceRepo,
		orderRepo:            d.OrderRepo,
		appRepo:              d.AppRepo,
		phaseRepo:            d.PhaseRepo,
		eventOrganizerRepo:   d.EventOrganizerRepo,
		organizerRepo:        d.OrganizerRepo,
		verifiedIdentityRepo: d.VerifiedIdentityRepo,
		capturePort:          d.CapturePort,
		reservationRepo:      d.ReservationRepo,
		ticketSaleRepo:       d.TicketSaleRepo,
		eventState:           d.EventState,
		reservationAuth:      d.ReservationAuth,
		clock:                d.Clock,
		logger:               d.Logger,
	}
}

// issuanceSource is everything the shared issuance core needs from a source:
// who bought what for which event, and the captured payment.
type issuanceSource struct {
	applicationID      entity.TicketApplicationID
	reservationID      entity.ReservationID
	buyerID            entity.UserID
	eventID            string
	ticketCount        int
	identity           entity.HolderIdentity
	verifiedIdentityID string
	amount             int64
	currency           string
	payment            entity.Payment
}

// issue is the shared issuance core: it resolves the event's Organizer and its
// fee rate, builds the Paid Order, the N Issued tickets and the Held
// Settlement, and stores them (with the ORDER.paid announcement) through
// IssuanceRepository.Issue. A duplicate re-reads and returns the Order of the
// same source.
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
		ID:            entity.OrderID(entity.NewID()),
		BuyerID:       src.buyerID,
		ApplicationID: src.applicationID,
		ReservationID: src.reservationID,
		Payment:       src.payment,
		Status:        entity.OrderStatusPaid,
		Amount:        src.amount,
		Currency:      src.currency,
		PaidTime:      now,
	}
	tickets := make([]*entity.Ticket, 0, src.ticketCount)
	for range src.ticketCount {
		tickets = append(tickets, &entity.Ticket{
			ID:                             entity.TicketID(entity.NewID()),
			OrderID:                        order.ID,
			HolderID:                       src.buyerID,
			EventID:                        src.eventID,
			HolderIdentity:                 src.identity,
			VerifiedIdentityID:             src.verifiedIdentityID,
			ResaleWithoutConsentProhibited: true,
			Status:                         entity.TicketStatusIssued,
			IssuedTime:                     now,
		})
	}
	settlement := entity.NewHeldSettlement(order, organizerID, src.eventID, organizer.PlatformFeeRateBps, now)

	if err := uc.issuanceRepo.Issue(ctx, order, tickets, settlement); err != nil {
		if !errors.Is(err, apperr.ErrAlreadyExists) {
			return nil, err
		}
		if src.reservationID != "" {
			return uc.orderRepo.GetByReservationID(ctx, src.reservationID)
		}
		return uc.orderRepo.GetByApplicationID(ctx, src.applicationID)
	}

	uc.logger.Info(ctx, "issued order and tickets",
		slog.String("order_id", string(order.ID)),
		slog.String("application_id", string(src.applicationID)),
		slog.String("reservation_id", string(src.reservationID)),
		slog.Int("ticket_count", src.ticketCount),
		slog.Int("platform_fee_rate_bps", organizer.PlatformFeeRateBps),
	)
	return order, nil
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
	//    VerifiedIdentity link on every ticket. --
	verifiedIdentityID := ""
	if phase.VerificationRequirement.RequiresVerification() {
		vi, err := uc.verifiedIdentityRepo.GetByUserID(ctx, string(app.ApplicantID))
		if err != nil {
			return nil, err // a required-verification phase must have a verified identity
		}
		verifiedIdentityID = vi.ID
	}

	return uc.issue(ctx, issuanceSource{
		applicationID:      app.ID,
		buyerID:            app.ApplicantID,
		eventID:            phase.EventID,
		ticketCount:        app.RequestedTicketCount,
		identity:           app.Identity,
		verifiedIdentityID: verifiedIdentityID,
		amount:             captured.AmountJPY,
		currency:           captured.Currency,
		payment: entity.Payment{
			Provider:         captured.Provider,
			PaymentIntentRef: app.Authorization.PaymentIntentRef,
			CardBrand:        captured.CardBrand,
			CardLast4:        captured.CardLast4,
		},
	})
}

// IssueDueWins implements [IssuanceUseCase].
func (uc *issuanceUseCase) IssueDueWins(ctx context.Context) error {
	ids, err := uc.issuanceRepo.ListApplicationIDsAwaitingIssuance(ctx)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if _, err := uc.IssueFromCapturedWin(ctx, id); err != nil {
			// Per-application failure must not block the rest of the sweep. The
			// next tick retries (IssueFromCapturedWin is idempotent).
			uc.logger.Warn(ctx, "issuance sweep: failed to issue application; skipping",
				slog.String("application_id", string(id)),
				slog.Any("error", err),
			)
		}
	}
	return nil
}

// IssueFromReservation implements [IssuanceUseCase]. The whole call runs under
// the Reservation's lock, so a double tap or the stalled-checkout job waits
// for the first call and then takes the idempotent path.
func (uc *issuanceUseCase) IssueFromReservation(ctx context.Context, reservationID entity.ReservationID, caller *entity.UserID) (*entity.Order, error) {
	var order *entity.Order
	err := uc.reservationRepo.Serialize(ctx, reservationID, func(ctx context.Context) error {
		var err error
		order, err = uc.issueFromReservation(ctx, reservationID, caller)
		return err
	})
	if err != nil {
		return nil, err
	}
	return order, nil
}

func (uc *issuanceUseCase) issueFromReservation(ctx context.Context, reservationID entity.ReservationID, caller *entity.UserID) (*entity.Order, error) {
	attrs := slog.String("reservation_id", string(reservationID))

	// 1. Ownership first, without revealing whether the Reservation exists.
	res, err := uc.reservationRepo.Get(ctx, reservationID)
	if err != nil {
		if caller != nil && errors.Is(err, apperr.ErrNotFound) {
			return nil, apperr.New(codes.PermissionDenied, "reservation is not the caller's", attrs)
		}
		return nil, err
	}
	if caller != nil && res.UserID != *caller {
		return nil, apperr.New(codes.PermissionDenied, "reservation is not the caller's", attrs)
	}

	// 2. An existing Order ends the call.
	if existing, err := uc.orderRepo.GetByReservationID(ctx, reservationID); err == nil {
		return existing, nil
	} else if !errors.Is(err, apperr.ErrNotFound) {
		return nil, err
	}

	now := uc.clock()
	sale, err := uc.ticketSaleRepo.Get(ctx, res.TicketSaleID, now)
	if err != nil {
		return nil, err
	}

	if res.Status == entity.ReservationStatusHeld {
		// 3. Holding, published and authorized, checked before the card.
		if !res.IsHoldingAt(now) {
			return nil, apperr.New(codes.FailedPrecondition, "the hold has lapsed", attrs)
		}
		published, err := uc.eventState.IsEventPublished(ctx, sale.EventID)
		if err != nil {
			return nil, err
		}
		if !published {
			return nil, apperr.New(codes.FailedPrecondition, "the concert is not published", attrs)
		}
		if res.AuthorizationRef == "" {
			return nil, apperr.New(codes.FailedPrecondition, "the card was not authorized", attrs)
		}
		// 4. The hold must be authenticated and for the Reservation's amount.
		if err := uc.reservationAuth.VerifyAuthorization(ctx, res.AuthorizationRef, res.Amount); err != nil {
			return nil, err
		}
		// 5. Commit while holding.
		outcome, err := uc.reservationRepo.Commit(ctx, reservationID, now)
		if err != nil {
			return nil, err
		}
		if outcome == entity.CommitOutcomeNotHeld {
			return nil, apperr.New(codes.FailedPrecondition, "the reservation is no longer holding", attrs)
		}
		if res, err = uc.reservationRepo.Get(ctx, reservationID); err != nil {
			return nil, err
		}
	}

	switch res.Status {
	case entity.ReservationStatusCommitted, entity.ReservationStatusCompleted:
	default:
		return nil, apperr.New(codes.FailedPrecondition, "the reservation has ended", attrs,
			slog.String("status", res.Status.String()))
	}

	// Charge once: a Reservation with a capture time is never charged again.
	if !res.IsCharged() {
		payment, err := uc.reservationAuth.CaptureAuthorization(ctx, res.AuthorizationRef)
		if err != nil {
			if errors.Is(err, apperr.ErrFailedPrecondition) {
				if revertErr := uc.reservationRepo.RevertCommit(ctx, reservationID); revertErr != nil {
					return nil, revertErr
				}
				return nil, err
			}
			return nil, err
		}
		if err := uc.reservationRepo.RecordCapture(ctx, reservationID, uc.clock(), payment); err != nil {
			return nil, err
		}
		if res, err = uc.reservationRepo.Get(ctx, reservationID); err != nil {
			return nil, err
		}
	}

	if res.HolderIdentity == nil {
		return nil, apperr.New(codes.Internal, "a charged reservation has no holder identity", attrs)
	}
	return uc.issue(ctx, issuanceSource{
		reservationID: res.ID,
		buyerID:       res.UserID,
		eventID:       sale.EventID,
		ticketCount:   res.TicketCount,
		identity:      *res.HolderIdentity,
		amount:        res.Amount,
		currency:      "JPY",
		payment: entity.Payment{
			Provider:         entity.PaymentProviderStripe,
			PaymentIntentRef: res.PaymentRef,
			CardBrand:        res.CardBrand,
			CardLast4:        res.CardLast4,
		},
	})
}

// IssueDueReservations implements [IssuanceUseCase].
func (uc *issuanceUseCase) IssueDueReservations(ctx context.Context) error {
	now := uc.clock()
	due, err := uc.reservationRepo.ListDue(ctx, now)
	if err != nil {
		return err
	}
	for _, res := range due {
		if res.Status != entity.ReservationStatusCommitted {
			continue
		}
		if res.IsCharged() && now.Sub(*res.CaptureTime) > unissuedChargeReportAge {
			uc.logger.Error(ctx, LogKeyNeedsOperator,
				errors.New("a charged checkout is still not issued"),
				slog.String("reservation_id", string(res.ID)),
				slog.String("payment_ref", res.PaymentRef),
				slog.Time("capture_time", *res.CaptureTime),
			)
		}
		if _, err := uc.IssueFromReservation(ctx, res.ID, nil); err != nil {
			// One failure never stops the others; the next run retries.
			uc.logger.Warn(ctx, "stalled-checkout sweep: failed to issue reservation; skipping",
				slog.String("reservation_id", string(res.ID)),
				slog.Any("error", err),
			)
		}
	}
	return nil
}
