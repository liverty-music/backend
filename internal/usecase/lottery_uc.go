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
	// its series' publish_state is PUBLISHED. Returns false — not an error — when
	// the event exists but is still DRAFT or CANCELLED.
	//
	// # Possible errors
	//
	//  - NotFound: no event with the given ID exists.
	//  - Internal: database query failure.
	IsEventPublished(ctx context.Context, eventID string) (bool, error)
}

// PaymentAuthorizationPort abstracts the payment provider (Stripe) for the
// authorization-hold payment model used by the lottery. Implementations live
// in internal/infrastructure/; the port is defined here (consumer package) per
// Clean Architecture conventions.
//
// Authorization hold flow:
//  1. [CreateAuthorization] — creates a manual-capture PaymentIntent
//     (capture_method=manual, JPY) and returns the client secret needed by the
//     frontend to complete 3DS. The hold is placed on the fan's card.
//  2. [VerifyAuthorization] — called at Apply to confirm the intent is in a
//     valid authorized state, for the expected JPY amount, on an accepted
//     card brand (Visa/Mastercard/JCB/Diners/Discover; not American Express).
//  3. [CancelAuthorization] — releases the hold (used on loss or withdrawal).
//  4. [CaptureAuthorization] — captures the held amount (used by the draw job
//     on win).
//
// TODO: replace with concrete Stripe implementation in internal/infrastructure/
// after the Stripe adapter is added.
type PaymentAuthorizationPort interface {
	// CreateAuthorization creates a Stripe PaymentIntent with
	// capture_method=manual in JPY for the given amount, placing a hold on the
	// fan's card.
	//
	// Returns the PaymentIntent ID (paymentIntentRef) and the client secret
	// (clientSecret) that the frontend passes to Stripe.js to complete 3DS
	// confirmation. The intent must be confirmed by the frontend before
	// [VerifyAuthorization] is called.
	//
	// # Possible errors
	//
	//  - Unavailable: the payment provider is unreachable.
	//  - InvalidArgument: amountJPY is non-positive.
	//  - Internal: unexpected provider error.
	CreateAuthorization(ctx context.Context, amountJPY int64) (paymentIntentRef string, clientSecret string, err error)

	// VerifyAuthorization confirms that the given PaymentIntent is a valid
	// authorization hold for exactly expectedAmountJPY on an accepted JPY card
	// (Visa/Mastercard/JCB/Diners/Discover). It rejects American Express cards
	// and non-JPY currency.
	//
	// Called at [Apply] time after the frontend has confirmed the intent.
	//
	// # Possible errors
	//
	//  - InvalidArgument: the intent is not in requires_capture state, the
	//    amount does not match, or the currency is not JPY.
	//  - FailedPrecondition: the card brand is American Express or otherwise
	//    unsupported (authorization cannot be held for the window duration).
	//  - NotFound: the payment intent does not exist.
	//  - Unavailable: the payment provider is unreachable.
	VerifyAuthorization(ctx context.Context, paymentIntentRef string, expectedAmountJPY int64) error

	// CancelAuthorization releases the authorization hold on the fan's card.
	// Used when the fan withdraws before the draw, or when the draw determines
	// the application is a loss.
	//
	// # Possible errors
	//
	//  - NotFound: the payment intent does not exist.
	//  - Unavailable: the payment provider is unreachable.
	//  - FailedPrecondition: the intent cannot be cancelled (already captured
	//    or fully cancelled).
	CancelAuthorization(ctx context.Context, paymentIntentRef string) error

	// CaptureAuthorization captures the held authorization, charging the fan's
	// card. Used by the draw job when the application wins.
	//
	// # Possible errors
	//
	//  - NotFound: the payment intent does not exist.
	//  - Unavailable: the payment provider is unreachable.
	//  - FailedPrecondition: the intent is not in a capturable state (e.g.
	//    already captured, cancelled, or the card has been closed).
	CaptureAuthorization(ctx context.Context, paymentIntentRef string) error
}

// ConfigureLotteryPhaseInput carries the organizer-supplied parameters for a
// new lottery phase.
//
// TODO: swap to generated proto request type after BSR gen.
type ConfigureLotteryPhaseInput struct {
	// EventID is the concert event this phase is attached to. The event must be
	// PUBLISHED before a lottery phase can be configured.
	EventID string

	// OpenTime is when the application window opens. Must be before CloseTime.
	OpenTime time.Time

	// CloseTime is when the application window closes. Must be after OpenTime.
	// The window duration must be between 1 and 14 days inclusive.
	CloseTime time.Time

	// TicketCapacity is the total number of tickets available. Must be positive.
	TicketCapacity int

	// MaxTicketsPerApplication is the maximum group size per application.
	// Must be in [1, TicketCapacity].
	MaxTicketsPerApplication int

	// TicketPrice is the per-ticket price in JPY (whole yen). Must be positive.
	// The authorization amount per application is TicketPrice × requested ticket count.
	TicketPrice int64

	// VerificationRequirement declares whether applicants must hold a verified
	// identity. Defaults to [entity.VerificationRequirementNone] when omitted.
	VerificationRequirement entity.VerificationRequirement
}

// SetVerificationRequirementInput carries the parameters for changing the
// identity-verification requirement on an existing lottery phase.
//
// TODO: swap to generated proto request type after BSR gen.
type SetVerificationRequirementInput struct {
	// PhaseID is the lottery phase to update.
	PhaseID entity.LotteryPhaseID

	// VerificationRequirement is the new requirement level.
	VerificationRequirement entity.VerificationRequirement
}

// CreateAuthorizationInput carries the parameters needed to create an
// authorization hold before a fan submits their application.
//
// TODO: swap to generated proto request type after BSR gen.
type CreateAuthorizationInput struct {
	// PhaseID identifies the lottery phase the fan intends to apply to.
	PhaseID entity.LotteryPhaseID

	// RequestedTicketCount is the number of tickets the fan will request.
	// Must be in [1, phase.MaxTicketsPerApplication].
	RequestedTicketCount int
}

// CreateAuthorizationResult carries the authorization credentials returned to
// the frontend to complete the 3DS card confirmation.
//
// TODO: swap to generated proto response type after BSR gen.
type CreateAuthorizationResult struct {
	// PaymentIntentRef is the Stripe PaymentIntent ID to pass back to [Apply].
	PaymentIntentRef string

	// ClientSecret is passed to the frontend to drive Stripe.js 3DS confirmation.
	// It is short-lived and MUST NOT be stored.
	ClientSecret string
}

// ApplyInput carries a fan's application parameters.
//
// TODO: swap to generated proto request type after BSR gen.
type ApplyInput struct {
	// PhaseID is the lottery phase the fan is applying to.
	PhaseID entity.LotteryPhaseID

	// ApplicantID is the fan's account identifier, derived from the auth token.
	ApplicantID entity.UserID

	// RequestedTicketCount is the companion-group size (1 to MaxTicketsPerApplication).
	RequestedTicketCount int

	// Identity carries the applicant's legal name and phone number (本人確認).
	Identity entity.ApplicantIdentity

	// PaymentIntentRef is the Stripe PaymentIntent ID returned by
	// [CreateAuthorization] after the frontend has completed 3DS confirmation.
	// [Apply] calls [PaymentAuthorizationPort.VerifyAuthorization] to confirm
	// the intent is valid before persisting the application.
	PaymentIntentRef string
}

// LotteryUseCase defines the business-logic surface for configuring lottery
// phases, accepting fan applications, and withdrawing applications.
//
// Connect-RPC handlers will delegate to this interface; they are NOT wired
// here because handler construction depends on generated request/response proto
// types.
//
// TODO: handler wiring after BSR gen — add a LotteryHandler in
// internal/adapter/rpc/ that maps proto ↔ entity and calls these methods.
type LotteryUseCase interface {
	// ConfigureLotteryPhase creates a new lottery sales phase for an event.
	//
	// # Possible errors
	//
	//  - InvalidArgument: validation failure (window ordering, duration out of
	//    [1,14] days, capacity, max-per-application, or ticket price).
	//  - FailedPrecondition: the event is not PUBLISHED.
	//  - NotFound: the event does not exist.
	ConfigureLotteryPhase(ctx context.Context, in ConfigureLotteryPhaseInput) (*entity.LotterySalesPhase, error)

	// SetPhaseVerificationRequirement changes the identity-verification
	// requirement on an existing lottery phase. The organizer may call this at
	// any time (before or after the draw). Returns the updated phase.
	//
	// # Possible errors
	//
	//  - InvalidArgument: phaseID is empty.
	//  - NotFound: no phase with the given ID exists.
	SetPhaseVerificationRequirement(ctx context.Context, in SetVerificationRequirementInput) (*entity.LotterySalesPhase, error)

	// CreateAuthorization creates a Stripe manual-capture PaymentIntent for the
	// given phase and ticket count, placing an authorization hold on the fan's
	// card. The frontend uses the returned client secret to complete 3DS. The
	// returned paymentIntentRef is passed to [Apply] to finalize the application.
	//
	// No TicketApplication is persisted by this call.
	//
	// # Possible errors
	//
	//  - InvalidArgument: requestedTicketCount out of range [1, max].
	//  - FailedPrecondition: the application window is not open.
	//  - NotFound: the phase does not exist.
	//  - Unavailable: the payment provider is unreachable.
	CreateAuthorization(ctx context.Context, in CreateAuthorizationInput) (CreateAuthorizationResult, error)

	// Apply submits a fan's application to a lottery phase. It verifies that the
	// given PaymentIntent is a valid authorization hold for the expected amount
	// (TicketPrice × count) on an accepted JPY card (Visa/Mastercard/JCB/
	// Diners/Discover; American Express and non-JPY are rejected).
	//
	// # Possible errors
	//
	//  - InvalidArgument: requestedTicketCount out of range, identity missing, or
	//    the PaymentIntent does not pass verification (wrong amount, wrong
	//    currency, or unaccepted brand).
	//  - FailedPrecondition: the application window is not open, or the fan
	//    already has an active application for this phase.
	//  - NotFound: the phase does not exist.
	//  - Unavailable: the payment provider is unreachable.
	Apply(ctx context.Context, in ApplyInput) (*entity.TicketApplication, error)

	// WithdrawApplication cancels the fan's own active application before the
	// draw. The authorization hold on the fan's card is released (cancelled).
	//
	// The fan may re-apply while the window is still open by calling [Apply]
	// again; that creates a fresh application row.
	//
	// # Possible errors
	//
	//  - NotFound: no application with the given ID exists.
	//  - FailedPrecondition: the application is not in a withdrawable state (e.g.
	//    the draw has already run or the application was already withdrawn).
	//  - PermissionDenied: the application does not belong to the calling fan.
	WithdrawApplication(ctx context.Context, applicationID entity.TicketApplicationID, applicantID entity.UserID) error

	// GetMyApplication returns the caller's most-recent active application for
	// the given phase. "Active" means the application has not been withdrawn.
	//
	// # Possible errors
	//
	//  - NotFound: no active application exists for the (phase, applicant) pair.
	GetMyApplication(ctx context.Context, phaseID entity.LotteryPhaseID, applicantID entity.UserID) (*entity.TicketApplication, error)

	// GetResult returns the caller's application result for the given phase.
	// The application's State field carries the result (Won, Lost, or Withdrawn).
	//
	// # Possible errors
	//
	//  - FailedPrecondition: the draw has not yet run for this phase.
	//  - NotFound: no active application exists for the (phase, applicant) pair.
	GetResult(ctx context.Context, phaseID entity.LotteryPhaseID, applicantID entity.UserID) (*entity.TicketApplication, error)

	// GetLotteryPhaseStatus loads the phase together with aggregate tallies over
	// all its ticket applications. The tallies reflect the post-draw outcome when
	// DrawCompleted is true, and are zeros otherwise.
	//
	// # Possible errors
	//
	//  - NotFound: no phase with the given ID exists.
	GetLotteryPhaseStatus(ctx context.Context, phaseID entity.LotteryPhaseID) (*entity.LotteryPhaseStatus, error)

	// DrawDuePhases finds all phases whose window has closed and whose draw has
	// not yet run, then executes the draw for each. Individual phase failures are
	// logged and skipped so one bad phase never blocks the rest of the sweep.
	// now is the reference clock value (time.Now() in production; injected for
	// deterministic tests).
	//
	// # Possible errors
	//
	//  - Internal: if listing due phases fails (returns immediately).
	DrawDuePhases(ctx context.Context, now time.Time) error

	// RunDraw executes the full lottery draw for a single phase:
	//  1. Loads all Applied applications.
	//  2. Runs entity.RunLotteryDraw with a crypto-seeded RNG.
	//  3. For each winner: calls CaptureAuthorization; on capture failure logs
	//     the error, calls CancelAuthorization, and treats the application as a
	//     loser (no automatic 繰上げ — the seat is left unfilled for the MVP).
	//  4. For each loser: calls CancelAuthorization.
	//  5. Calls PersistDrawOutcome to commit states and stamp drawn_at atomically.
	//
	// A Won application is the handoff signal to ⑤ Order/Ticket creation; no
	// event is emitted in the MVP — ⑤ will poll or be triggered separately.
	//
	// # Possible errors
	//
	//  - NotFound: no phase with the given phaseID exists.
	//  - Internal: database or payment provider failure.
	RunDraw(ctx context.Context, phaseID entity.LotteryPhaseID) error
}

// lotteryUseCase implements [LotteryUseCase].
type lotteryUseCase struct {
	phaseRepo            entity.LotteryPhaseRepository
	appRepo              entity.TicketApplicationRepository
	eventState           EventPublishStatePort
	paymentPort          PaymentAuthorizationPort
	verifiedIdentityRepo entity.VerifiedIdentityRepository
	clock                Clock
	logger               *logging.Logger
}

// Compile-time interface compliance check.
var _ LotteryUseCase = (*lotteryUseCase)(nil)

// NewLotteryUseCase constructs a LotteryUseCase with the given dependencies.
// All parameters are required and must not be nil.
func NewLotteryUseCase(
	phaseRepo entity.LotteryPhaseRepository,
	appRepo entity.TicketApplicationRepository,
	eventState EventPublishStatePort,
	paymentPort PaymentAuthorizationPort,
	verifiedIdentityRepo entity.VerifiedIdentityRepository,
	clock Clock,
	logger *logging.Logger,
) LotteryUseCase {
	return &lotteryUseCase{
		phaseRepo:            phaseRepo,
		appRepo:              appRepo,
		eventState:           eventState,
		paymentPort:          paymentPort,
		verifiedIdentityRepo: verifiedIdentityRepo,
		clock:                clock,
		logger:               logger,
	}
}

const (
	// minWindowDuration is the minimum allowed application window duration (1 day).
	minWindowDuration = 24 * time.Hour

	// maxWindowDuration is the maximum allowed application window duration (14 days).
	maxWindowDuration = 14 * 24 * time.Hour
)

// ConfigureLotteryPhase implements [LotteryUseCase].
func (uc *lotteryUseCase) ConfigureLotteryPhase(ctx context.Context, in ConfigureLotteryPhaseInput) (*entity.LotterySalesPhase, error) {
	// -- validation --

	if in.EventID == "" {
		return nil, apperr.New(codes.InvalidArgument, "event_id is required")
	}
	if in.OpenTime.IsZero() {
		return nil, apperr.New(codes.InvalidArgument, "open_time is required")
	}
	if in.CloseTime.IsZero() {
		return nil, apperr.New(codes.InvalidArgument, "close_time is required")
	}
	if !in.CloseTime.After(in.OpenTime) {
		return nil, apperr.New(codes.InvalidArgument, "close_time must be after open_time")
	}
	duration := in.CloseTime.Sub(in.OpenTime)
	if duration < minWindowDuration {
		return nil, apperr.New(codes.InvalidArgument, "application window must be at least 1 day")
	}
	if duration > maxWindowDuration {
		return nil, apperr.New(codes.InvalidArgument, "application window must not exceed 14 days")
	}
	if in.TicketCapacity <= 0 {
		return nil, apperr.New(codes.InvalidArgument, "ticket_capacity must be positive")
	}
	if in.MaxTicketsPerApplication <= 0 {
		return nil, apperr.New(codes.InvalidArgument, "max_tickets_per_application must be positive")
	}
	if in.MaxTicketsPerApplication > in.TicketCapacity {
		return nil, apperr.New(codes.InvalidArgument, "max_tickets_per_application must not exceed ticket_capacity")
	}
	if in.TicketPrice <= 0 {
		return nil, apperr.New(codes.InvalidArgument, "ticket_price must be positive")
	}

	// -- precondition: event must be PUBLISHED --

	published, err := uc.eventState.IsEventPublished(ctx, in.EventID)
	if err != nil {
		return nil, err // propagates NotFound or Internal from the port
	}
	if !published {
		return nil, apperr.New(codes.FailedPrecondition, "event is not published; configure a lottery phase only for published events")
	}

	// -- persist --

	phase := &entity.LotterySalesPhase{
		ID:                       entity.LotteryPhaseID(entity.NewID()),
		EventID:                  in.EventID,
		OpenTime:                 in.OpenTime,
		CloseTime:                in.CloseTime,
		TicketCapacity:           in.TicketCapacity,
		MaxTicketsPerApplication: in.MaxTicketsPerApplication,
		TicketPrice:              in.TicketPrice,
		VerificationRequirement:  in.VerificationRequirement,
	}
	created, err := uc.phaseRepo.Create(ctx, phase)
	if err != nil {
		return nil, err
	}
	return created, nil
}

// CreateAuthorization implements [LotteryUseCase].
func (uc *lotteryUseCase) CreateAuthorization(ctx context.Context, in CreateAuthorizationInput) (CreateAuthorizationResult, error) {
	// -- load phase --

	phase, err := uc.phaseRepo.Get(ctx, in.PhaseID)
	if err != nil {
		return CreateAuthorizationResult{}, err // propagates NotFound
	}

	// -- validate count --

	if in.RequestedTicketCount <= 0 {
		return CreateAuthorizationResult{}, apperr.New(codes.InvalidArgument, "requested_ticket_count must be positive")
	}
	if in.RequestedTicketCount > phase.MaxTicketsPerApplication {
		return CreateAuthorizationResult{}, apperr.New(codes.InvalidArgument, "requested_ticket_count exceeds the phase limit")
	}

	// -- window check --

	now := uc.clock()
	if now.Before(phase.OpenTime) {
		return CreateAuthorizationResult{}, apperr.New(codes.FailedPrecondition, "application window is not yet open")
	}
	if !now.Before(phase.CloseTime) {
		return CreateAuthorizationResult{}, apperr.New(codes.FailedPrecondition, "application window has closed")
	}

	// -- create authorization hold --

	amountJPY := phase.TicketPrice * int64(in.RequestedTicketCount)
	piRef, clientSecret, err := uc.paymentPort.CreateAuthorization(ctx, amountJPY)
	if err != nil {
		return CreateAuthorizationResult{}, err // propagates Unavailable / InvalidArgument / Internal
	}

	return CreateAuthorizationResult{
		PaymentIntentRef: piRef,
		ClientSecret:     clientSecret,
	}, nil
}

// Apply implements [LotteryUseCase].
func (uc *lotteryUseCase) Apply(ctx context.Context, in ApplyInput) (*entity.TicketApplication, error) {
	// -- load phase --

	phase, err := uc.phaseRepo.Get(ctx, in.PhaseID)
	if err != nil {
		return nil, err // propagates NotFound
	}

	// -- validate requested count --

	if in.RequestedTicketCount <= 0 {
		return nil, apperr.New(codes.InvalidArgument, "requested_ticket_count must be positive")
	}
	if in.RequestedTicketCount > phase.MaxTicketsPerApplication {
		return nil, apperr.New(codes.InvalidArgument, "requested_ticket_count exceeds the phase limit")
	}

	// -- validate identity --

	if in.Identity.FullName == "" {
		return nil, apperr.New(codes.InvalidArgument, "applicant full name is required")
	}
	if in.Identity.PhoneNumber == "" {
		return nil, apperr.New(codes.InvalidArgument, "applicant phone number is required")
	}

	// -- window check --

	now := uc.clock()
	if now.Before(phase.OpenTime) {
		return nil, apperr.New(codes.FailedPrecondition, "application window is not yet open")
	}
	if !now.Before(phase.CloseTime) {
		return nil, apperr.New(codes.FailedPrecondition, "application window has closed")
	}

	// -- identity-verification gate (task 6.3) --
	//
	// When the phase requires verification, the applicant must hold an ACTIVE
	// VerifiedIdentity. In the MVP, both VERIFIED_ANY and JPKI_ONLY map to the
	// same check: an ACTIVE JPKI-backed VerifiedIdentity must exist. The
	// driver's-licence fallback is POST-MVP and not evaluated here.
	//
	// Per-person limit note: when verification is required, the eKYC dedupe
	// (≤1 active verified account per pocket_sign_user_id, enforced by
	// uq_active_pocket_sign_user_id) means the existing "1 application per
	// account per phase" rule IS effectively a per-person limit — no separate
	// cross-account scan is needed (task 6.1).
	if phase.VerificationRequirement.RequiresVerification() {
		vi, err := uc.verifiedIdentityRepo.GetByUserID(ctx, string(in.ApplicantID))
		if err != nil {
			if isNotFound(err) {
				return nil, apperr.New(codes.FailedPrecondition,
					"identity verification is required to apply to this event; please verify your identity first")
			}
			return nil, err
		}
		if vi.Status != entity.VerificationStatusActive {
			return nil, apperr.New(codes.FailedPrecondition,
				"identity verification is required to apply to this event; your verification is not active — please re-verify")
		}
	}

	// -- 1-per-account duplicate check --

	existing, err := uc.appRepo.GetByPhaseAndApplicant(ctx, in.PhaseID, in.ApplicantID)
	if err != nil && !isNotFound(err) {
		return nil, err
	}
	if existing != nil {
		return nil, apperr.New(codes.FailedPrecondition, "an active application already exists for this phase; withdraw it before re-applying")
	}

	// -- verify the authorization hold --
	//
	// Confirms the PaymentIntent is in requires_capture state (3DS completed),
	// for the exact amount (price × count), on a JPY Visa/Mastercard/JCB/
	// Diners/Discover card. Rejects American Express and non-JPY.

	amountJPY := phase.TicketPrice * int64(in.RequestedTicketCount)
	if err := uc.paymentPort.VerifyAuthorization(ctx, in.PaymentIntentRef, amountJPY); err != nil {
		return nil, err // propagates InvalidArgument / FailedPrecondition / NotFound / Unavailable
	}

	// -- persist application --

	app := &entity.TicketApplication{
		ID:                   entity.TicketApplicationID(entity.NewID()),
		PhaseID:              in.PhaseID,
		ApplicantID:          in.ApplicantID,
		RequestedTicketCount: in.RequestedTicketCount,
		Identity:             in.Identity,
		Authorization: entity.PaymentAuthorization{
			PaymentIntentRef: in.PaymentIntentRef,
		},
		State: entity.TicketApplicationStateApplied,
	}
	created, err := uc.appRepo.Create(ctx, app)
	if err != nil {
		return nil, err
	}
	return created, nil
}

// WithdrawApplication implements [LotteryUseCase].
func (uc *lotteryUseCase) WithdrawApplication(ctx context.Context, applicationID entity.TicketApplicationID, applicantID entity.UserID) error {
	app, err := uc.appRepo.Get(ctx, applicationID)
	if err != nil {
		return err // propagates NotFound
	}

	// Non-revealing ownership check: the application must belong to the caller.
	// Return PermissionDenied rather than NotFound so the caller cannot enumerate
	// other fans' application IDs via a timing oracle.
	if app.ApplicantID != applicantID {
		return apperr.New(codes.PermissionDenied, "permission denied")
	}

	if !app.IsWithdrawable() {
		return apperr.New(codes.FailedPrecondition, "application is not in a withdrawable state")
	}

	// Release the authorization hold before marking as withdrawn.
	if err := uc.paymentPort.CancelAuthorization(ctx, string(app.Authorization.PaymentIntentRef)); err != nil {
		return err // propagates FailedPrecondition / NotFound / Unavailable
	}

	return uc.appRepo.UpdateState(ctx, applicationID, entity.TicketApplicationStateWithdrawn)
}

// GetMyApplication implements [LotteryUseCase].
func (uc *lotteryUseCase) GetMyApplication(ctx context.Context, phaseID entity.LotteryPhaseID, applicantID entity.UserID) (*entity.TicketApplication, error) {
	return uc.appRepo.GetByPhaseAndApplicant(ctx, phaseID, applicantID)
}

// GetResult implements [LotteryUseCase].
//
// Draw-not-run detection: an application remains in Applied state until the
// draw sets it to Won or Lost and stamps draw_sequence. GetResult returns
// FailedPrecondition when the only active application is still in Applied
// state, because that means the draw has not run yet and there is no result
// to report. Won and Lost states indicate the draw completed.
func (uc *lotteryUseCase) GetResult(ctx context.Context, phaseID entity.LotteryPhaseID, applicantID entity.UserID) (*entity.TicketApplication, error) {
	app, err := uc.appRepo.GetByPhaseAndApplicant(ctx, phaseID, applicantID)
	if err != nil {
		return nil, err // propagates NotFound
	}

	if app.State == entity.TicketApplicationStateApplied {
		return nil, apperr.New(codes.FailedPrecondition, "draw has not run yet; result is not available")
	}

	return app, nil
}

// GetLotteryPhaseStatus implements [LotteryUseCase].
func (uc *lotteryUseCase) GetLotteryPhaseStatus(ctx context.Context, phaseID entity.LotteryPhaseID) (*entity.LotteryPhaseStatus, error) {
	phase, err := uc.phaseRepo.Get(ctx, phaseID)
	if err != nil {
		return nil, err // propagates NotFound
	}

	stats, err := uc.appRepo.GetPhaseStats(ctx, phaseID)
	if err != nil {
		return nil, err
	}

	stats.Phase = phase
	return &stats, nil
}

// SetPhaseVerificationRequirement implements [LotteryUseCase].
func (uc *lotteryUseCase) SetPhaseVerificationRequirement(ctx context.Context, in SetVerificationRequirementInput) (*entity.LotterySalesPhase, error) {
	if in.PhaseID == "" {
		return nil, apperr.New(codes.InvalidArgument, "phase_id is required")
	}
	return uc.phaseRepo.UpdateVerificationRequirement(ctx, in.PhaseID, in.VerificationRequirement)
}

// DrawDuePhases implements [LotteryUseCase].
func (uc *lotteryUseCase) DrawDuePhases(ctx context.Context, now time.Time) error {
	phases, err := uc.phaseRepo.ListPhasesDueForDraw(ctx, now)
	if err != nil {
		return err // propagates Internal from the repo
	}

	for _, phase := range phases {
		if err := uc.RunDraw(ctx, phase.ID); err != nil {
			// Log and continue: one bad phase must not stop the sweep.
			uc.logger.Warn(ctx, "draw failed for phase; skipping",
				slog.String("phase_id", string(phase.ID)),
				slog.Any("error", err),
			)
		}
	}
	return nil
}

// RunDraw implements [LotteryUseCase].
func (uc *lotteryUseCase) RunDraw(ctx context.Context, phaseID entity.LotteryPhaseID) error {
	phase, err := uc.phaseRepo.Get(ctx, phaseID)
	if err != nil {
		return err // propagates NotFound
	}

	apps, err := uc.appRepo.ListAppliedForPhase(ctx, phaseID)
	if err != nil {
		return err
	}

	// Zero applications: nothing to draw; still stamp drawn_at so the phase is
	// not retried on the next sweep tick.
	if len(apps) == 0 {
		uc.logger.Info(ctx, "draw: no applied applications; marking phase as drawn",
			slog.String("phase_id", string(phaseID)),
		)
		return uc.appRepo.PersistDrawOutcome(ctx, phaseID, nil, nil)
	}

	// Build the input slice for the draw algorithm.
	drawApps := make([]entity.DrawApplication, len(apps))
	for i, a := range apps {
		drawApps[i] = entity.DrawApplication{
			ID:                   a.ID,
			RequestedTicketCount: a.RequestedTicketCount,
		}
	}

	result := entity.RunLotteryDraw(drawApps, phase.TicketCapacity, cryptoSeededRand())

	// Build a lookup from ApplicationID to PaymentIntentRef for winners and
	// losers, so capture/cancel can be called without a per-application DB hit.
	piByID := make(map[entity.TicketApplicationID]string, len(apps))
	for _, a := range apps {
		piByID[a.ID] = a.Authorization.PaymentIntentRef
	}

	// Process winners: capture the authorization. On capture failure, demote the
	// winner to a loser (cancel the hold, leave the seat unfilled — no 繰上げ).
	winners := make([]entity.DrawWinnerRow, 0, len(result.Winners))
	losers := make([]entity.DrawLoserRow, 0, len(result.Waitlist))

	for _, w := range result.Winners {
		pi := piByID[w.Application.ID]
		if err := uc.paymentPort.CaptureAuthorization(ctx, pi); err != nil {
			uc.logger.Warn(ctx, "capture failed for winner; demoting to loser",
				slog.String("phase_id", string(phaseID)),
				slog.String("application_id", string(w.Application.ID)),
				slog.Any("error", err),
			)
			// Release the hold and treat this application as a loser.
			if cancelErr := uc.paymentPort.CancelAuthorization(ctx, pi); cancelErr != nil {
				uc.logger.Warn(ctx, "cancel after failed capture also failed",
					slog.String("phase_id", string(phaseID)),
					slog.String("application_id", string(w.Application.ID)),
					slog.Any("error", cancelErr),
				)
			}
			losers = append(losers, entity.DrawLoserRow{
				ApplicationID:    w.Application.ID,
				PaymentIntentRef: pi,
				DrawSequence:     w.DrawSequence,
			})
			continue
		}
		winners = append(winners, entity.DrawWinnerRow{
			ApplicationID:    w.Application.ID,
			PaymentIntentRef: pi,
			DrawSequence:     w.DrawSequence,
		})
	}

	// Release holds for all waitlisted losers.
	for _, l := range result.Waitlist {
		pi := piByID[l.Application.ID]
		if err := uc.paymentPort.CancelAuthorization(ctx, pi); err != nil {
			uc.logger.Warn(ctx, "cancel failed for loser; continuing",
				slog.String("phase_id", string(phaseID)),
				slog.String("application_id", string(l.Application.ID)),
				slog.Any("error", err),
			)
		}
		losers = append(losers, entity.DrawLoserRow{
			ApplicationID:    l.Application.ID,
			PaymentIntentRef: pi,
			DrawSequence:     l.DrawSequence,
		})
	}

	// Atomically persist winner/loser states + drawn_at stamp.
	// Won state is the handoff signal to ⑤ (Order + Ticket creation); no event
	// is emitted in the MVP — ⑤ will poll Won rows or be triggered separately.
	if err := uc.appRepo.PersistDrawOutcome(ctx, phaseID, winners, losers); err != nil {
		return err
	}

	uc.logger.Info(ctx, "draw completed",
		slog.String("phase_id", string(phaseID)),
		slog.Int("winner_count", len(winners)),
		slog.Int("loser_count", len(losers)),
	)
	return nil
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
