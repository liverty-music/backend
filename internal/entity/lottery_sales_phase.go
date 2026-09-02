package entity

import (
	"context"
	"time"
)

// UserID is the opaque identifier for an authenticated fan account.
//
// TODO: swap to generated liverty_music.entity.v1.UserId after BSR gen.
type UserID string

// LotteryPhaseID is the opaque identifier for a lottery sales phase.
//
// TODO: swap to generated liverty_music.entity.v1.LotteryPhaseId after BSR gen.
type LotteryPhaseID string

// LotterySalesPhase is the configuration record for a single lottery-sales
// window attached to a concert event. It is separate from the discovered
// [SalesPhase] (which is read-only, Gemini-sourced) — this type is
// organizer-authored and carries full lottery parameters.
//
// TODO: swap to generated liverty_music.entity.v1.LotterySalesPhase after BSR gen.
type LotterySalesPhase struct {
	// ID is the surrogate primary key (UUIDv7).
	ID LotteryPhaseID

	// EventID is the concert event this phase is attached to.
	EventID string

	// OpenTime is when the application window opens. Must be before CloseTime.
	OpenTime time.Time

	// CloseTime is when the application window closes. Must be after OpenTime.
	// The window duration must be between 1 and 14 days inclusive.
	CloseTime time.Time

	// TicketCapacity is the total number of tickets available in this phase.
	// Must be positive.
	TicketCapacity int

	// MaxTicketsPerApplication is the maximum companion-group size a single fan
	// can request. Must be in [1, TicketCapacity].
	MaxTicketsPerApplication int

	// TicketPrice is the price per ticket in JPY (whole yen). Must be positive.
	// The authorization amount is TicketPrice × requested ticket count.
	TicketPrice int64

	// DrawnTime is when the draw ran for this phase. Zero value means the draw
	// has not yet run. Corresponds to drawn_at (TIMESTAMPTZ, nullable) in the
	// DB; set atomically by [TicketApplicationRepository.PersistDrawOutcome].
	DrawnTime time.Time
}

// ApplicantIdentity carries the full-name and phone number required for
// ticket collection at the venue. Collected at application time and not used
// in the draw algorithm.
//
// TODO: swap to generated liverty_music.entity.v1.ApplicantIdentity after BSR gen.
type ApplicantIdentity struct {
	// FullName is the applicant's legal name as it appears on their ID.
	FullName string

	// PhoneNumber is the contact phone number in E.164 format or any local format
	// accepted by the venue.
	PhoneNumber string
}

// PaymentAuthorization carries the Stripe PaymentIntent reference created when
// a fan authorizes (holds) the ticket amount at application time. No money is
// captured until the draw runs.
//
// TODO: swap to generated liverty_music.entity.v1.PaymentAuthorization after BSR gen.
type PaymentAuthorization struct {
	// PaymentIntentRef is the Stripe PaymentIntent ID (e.g. "pi_3Qx...").
	// The authorization is a manual-capture PaymentIntent (capture_method=manual).
	PaymentIntentRef string
}

// TicketApplicationState is the lifecycle state of a [TicketApplication].
//
// TODO: swap to generated liverty_music.entity.v1.TicketApplicationState after BSR gen.
type TicketApplicationState int8

const (
	// TicketApplicationStateApplied is the initial state: the fan has applied and
	// the authorization hold is in place. The draw has not yet run.
	TicketApplicationStateApplied TicketApplicationState = 1

	// TicketApplicationStateWon means the draw allocated tickets to this
	// application. The authorization has been captured.
	TicketApplicationStateWon TicketApplicationState = 2

	// TicketApplicationStateLost means the draw did not allocate tickets to this
	// application. The authorization has been released (cancelled).
	TicketApplicationStateLost TicketApplicationState = 3

	// TicketApplicationStateWithdrawn means the fan cancelled their own
	// application before the draw. The authorization has been released. Re-application
	// is permitted while the window is still open (a fresh [Apply] call creates a new row).
	TicketApplicationStateWithdrawn TicketApplicationState = 5
)

// TicketApplication is a fan's entry into a lottery-sales phase. It records
// who applied, how many tickets they requested, the applicant's identity for
// 本人確認, and the authorization hold placed on the fan's card at apply time.
//
// TODO: swap to generated liverty_music.entity.v1.TicketApplication after BSR gen.
type TicketApplication struct {
	// ID is the surrogate primary key. Reuses [TicketApplicationID] from
	// lottery_draw.go so draw algorithms can reference this type directly.
	ID TicketApplicationID

	// PhaseID is the lottery phase this application belongs to.
	PhaseID LotteryPhaseID

	// ApplicantID is the fan's account identifier.
	ApplicantID UserID

	// RequestedTicketCount is the companion-group size (all-or-nothing allocation).
	// Must be in [1, phase.MaxTicketsPerApplication].
	RequestedTicketCount int

	// Identity carries the full name and phone number for ticket collection (本人確認).
	Identity ApplicantIdentity

	// Authorization holds the Stripe PaymentIntent reference for the authorization
	// placed on the fan's card at application time.
	Authorization PaymentAuthorization

	// State is the current lifecycle state of this application.
	State TicketApplicationState

	// DrawSequence is the application's zero-based position in the uniformly-
	// random shuffle that the draw ran. Non-zero only after the draw completes;
	// used to order the loser waitlist for ⑦ official-resale.
	DrawSequence int64
}

// IsWithdrawable reports whether the application can be withdrawn by the fan.
// Only Applied state is withdrawable; Won/Lost/Withdrawn are not.
func (a *TicketApplication) IsWithdrawable() bool {
	return a.State == TicketApplicationStateApplied
}

// LotteryPhaseStatus is a read-only aggregate view of a [LotterySalesPhase]
// combined with tallied statistics over its [TicketApplication] rows. It is
// assembled by the usecase layer (phaseRepo.Get + appRepo.GetPhaseStats) and
// returned by GetLotteryPhaseStatus.
type LotteryPhaseStatus struct {
	// Phase is the lottery sales phase configuration.
	Phase *LotterySalesPhase

	// DrawCompleted reports whether the draw has run. True when at least one
	// application has a non-null draw_sequence (state Won or Lost implies this).
	DrawCompleted bool

	// ApplicationCount is the number of non-withdrawn applications.
	ApplicationCount int

	// RequestedTicketCount is the sum of requested_ticket_count across all
	// non-withdrawn applications.
	RequestedTicketCount int

	// WinningApplicationCount is the number of applications in Won state.
	WinningApplicationCount int

	// WonTicketCount is the sum of requested_ticket_count for Won applications.
	WonTicketCount int

	// WaitlistedApplicationCount is the number of applications in Lost state.
	WaitlistedApplicationCount int
}

// DrawWinnerRow carries the fields needed to persist a winning draw outcome
// for a single application. Returned by [RunLotteryDraw] and consumed by
// [TicketApplicationRepository.PersistDrawOutcome].
type DrawWinnerRow struct {
	// ApplicationID is the surrogate key of the winning application.
	ApplicationID TicketApplicationID

	// PaymentIntentRef is the Stripe PaymentIntent reference for the authorization
	// hold, needed by the draw job to capture the payment.
	PaymentIntentRef string

	// DrawSequence is the application's zero-based position in the draw shuffle.
	DrawSequence int
}

// DrawLoserRow carries the fields needed to persist a losing draw outcome
// for a single application. Returned by [RunLotteryDraw] and consumed by
// [TicketApplicationRepository.PersistDrawOutcome].
type DrawLoserRow struct {
	// ApplicationID is the surrogate key of the losing application.
	ApplicationID TicketApplicationID

	// PaymentIntentRef is the Stripe PaymentIntent reference for the authorization
	// hold, needed by the draw job to release the hold via CancelAuthorization.
	PaymentIntentRef string

	// DrawSequence is the application's zero-based position in the draw shuffle.
	// Preserves the loser waitlist order for ⑦ official-resale.
	DrawSequence int
}

// LotteryPhaseRepository defines the data access interface for [LotterySalesPhase].
// Implementations live in internal/infrastructure/database/rdb/.
//
// TODO: swap method signatures to use generated proto ID types after BSR gen.
type LotteryPhaseRepository interface {
	// Create persists a new LotterySalesPhase and returns the created row.
	//
	// # Possible errors
	//
	//  - InvalidArgument: the phase is structurally invalid (zero ID, etc.).
	//  - FailedPrecondition: the referenced event does not exist.
	Create(ctx context.Context, phase *LotterySalesPhase) (*LotterySalesPhase, error)

	// Get returns the phase by ID.
	//
	// # Possible errors
	//
	//  - NotFound: no phase with the given ID exists.
	Get(ctx context.Context, id LotteryPhaseID) (*LotterySalesPhase, error)

	// ListPhasesDueForDraw returns all phases whose application window has closed
	// (close_at <= now) and whose draw has not yet run (drawn_at IS NULL). The
	// caller (draw sweeper) iterates this list and runs [RunDraw] for each phase.
	//
	// # Possible errors
	//
	//  - Internal: database query failure.
	ListPhasesDueForDraw(ctx context.Context, now time.Time) ([]*LotterySalesPhase, error)
}

// TicketApplicationRepository defines the data access interface for
// [TicketApplication]. Implementations live in internal/infrastructure/database/rdb/.
//
// TODO: swap method signatures to use generated proto ID types after BSR gen.
type TicketApplicationRepository interface {
	// Create persists a new TicketApplication and returns the created row.
	//
	// # Possible errors
	//
	//  - InvalidArgument: the application is structurally invalid.
	//  - FailedPrecondition: the referenced phase does not exist.
	Create(ctx context.Context, app *TicketApplication) (*TicketApplication, error)

	// GetByPhaseAndApplicant returns the most-recent active application for the
	// given (phaseID, applicantID) pair. "Active" means state is NOT Withdrawn.
	// Returns NotFound when no active application exists.
	//
	// # Possible errors
	//
	//  - NotFound: no active application for the (phase, applicant) pair.
	GetByPhaseAndApplicant(ctx context.Context, phaseID LotteryPhaseID, applicantID UserID) (*TicketApplication, error)

	// Get returns the application by its own ID.
	//
	// # Possible errors
	//
	//  - NotFound: no application with the given ID exists.
	Get(ctx context.Context, id TicketApplicationID) (*TicketApplication, error)

	// UpdateState persists a state change on the application. It is the only
	// mutation operation available to usecases; field-by-field updates are not
	// supported to keep the repo surface minimal.
	//
	// # Possible errors
	//
	//  - NotFound: no application with the given ID exists.
	UpdateState(ctx context.Context, id TicketApplicationID, state TicketApplicationState) error

	// GetPhaseStats returns aggregate tallies for all ticket_applications rows
	// belonging to the given phase. Non-withdrawn applications are counted; Won
	// and Lost tallies reflect post-draw outcomes. draw_completed is true when at
	// least one application has a non-null draw_sequence. Returns a zero-valued
	// struct (no error) when no applications exist for the phase.
	//
	// # Possible errors
	//
	//  - InvalidArgument: phaseID is empty.
	//  - Internal: database query failure.
	GetPhaseStats(ctx context.Context, phaseID LotteryPhaseID) (LotteryPhaseStatus, error)

	// ListAppliedForPhase returns all applications in the Applied state (1) for the
	// given phase. Withdrawn, Won, and Lost applications are excluded because they
	// are not draw candidates. The order is unspecified; the draw algorithm shuffles
	// the returned slice internally.
	//
	// # Possible errors
	//
	//  - InvalidArgument: phaseID is empty.
	//  - Internal: database query failure.
	ListAppliedForPhase(ctx context.Context, phaseID LotteryPhaseID) ([]*TicketApplication, error)

	// PersistDrawOutcome atomically persists the draw results for one phase in a
	// single transaction:
	//  1. Sets each winner's state to Won and stamps its draw_sequence.
	//  2. Sets each loser's state to Lost and stamps its draw_sequence.
	//  3. Sets lottery_sales_phases.drawn_at = now() for the phase.
	//
	// Idempotency is externally guaranteed: the draw sweeper only calls this for
	// phases where drawn_at IS NULL (filtered by [LotteryPhaseRepository.ListPhasesDueForDraw]).
	//
	// # Possible errors
	//
	//  - InvalidArgument: phaseID is empty.
	//  - Internal: database transaction or query failure.
	PersistDrawOutcome(ctx context.Context, phaseID LotteryPhaseID, winners []DrawWinnerRow, losers []DrawLoserRow) error
}
