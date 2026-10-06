package entity

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// SalesMethod classifies the ticket acquisition mechanism for a sales phase. It
// is the only classification of a phase.
//
// Values MUST exactly match the proto enum liverty_music.entity.v1.SalesMethod
// so an RPC adapter can cast Go int16 ↔ proto enum by value.
type SalesMethod int16

const (
	// SalesMethodUnspecified is the zero value. A phase never carries it.
	SalesMethodUnspecified SalesMethod = 0
	// SalesMethodLottery is a ballot-based allocation: fans apply during the
	// window and a random draw determines winners. Mirrors LOTTERY = 1.
	SalesMethodLottery SalesMethod = 1
	// SalesMethodFirstCome is a sequential on-sale: tickets sell in order of
	// purchase until exhausted. Mirrors FIRST_COME = 2.
	SalesMethodFirstCome SalesMethod = 2
)

// IsValid reports whether m is LOTTERY or FIRST_COME.
func (m SalesMethod) IsValid() bool {
	return m == SalesMethodLottery || m == SalesMethodFirstCome
}

// ReminderStage identifies which point in the sales lifecycle a reminder targets.
//
// Which stages apply depends on the phase's method: a lottery gets all three, a
// first-come sale only APPLY_OPEN. Value 3 was APPLY_CLOSE_1H and is left
// unused. The sales_phase_reminders.stage CHECK (1..10) leaves room for future
// stages.
type ReminderStage int16

const (
	// ReminderStageApplyOpen fires at apply_start_time for a lottery and 30
	// minutes before it for a first-come sale.
	ReminderStageApplyOpen ReminderStage = 1
	// ReminderStageApplyClose24H fires 24 hours before apply_end_time of a
	// lottery.
	ReminderStageApplyClose24H ReminderStage = 2
	// ReminderStageResultDay fires at 09:00 in the user's timezone on the
	// calendar day of lottery_result_time.
	ReminderStageResultDay ReminderStage = 4
)

// IsValid reports whether s is one of the defined stages.
func (s ReminderStage) IsValid() bool {
	switch s {
	case ReminderStageApplyOpen, ReminderStageApplyClose24H, ReminderStageResultDay:
		return true
	default:
		return false
	}
}

// String returns the uppercase name of the stage for use in analytics event
// properties (e.g. "APPLY_OPEN"). The zero value and any unrecognised int16
// return "UNSPECIFIED".
func (s ReminderStage) String() string {
	switch s {
	case ReminderStageApplyOpen:
		return "APPLY_OPEN"
	case ReminderStageApplyClose24H:
		return "APPLY_CLOSE_24H"
	case ReminderStageResultDay:
		return "RESULT_DAY"
	default:
		return "UNSPECIFIED"
	}
}

// SalesPhase represents a single ticket-sales window for a series: one
// application period under one method. A phase belongs to a series and applies
// to it as a whole — there is no per-event coverage subset.
//
// Re-discovered phases converge on (series_id, method, apply start date in
// Japan time); the surrogate ID is the stable handle reminders reference.
type SalesPhase struct {
	// ID is the unique identifier for this phase (UUIDv7).
	ID string
	// SeriesID is the parent series that owns this sales phase.
	SeriesID string
	// Method is the ticket acquisition mechanism (lottery or first come).
	Method SalesMethod
	// ApplyStartTime is the start of the application or on-sale window. Required.
	ApplyStartTime time.Time
	// ApplyEndTime is the end of the application window. Always set on a
	// lottery; zero on a first-come sale that ends when tickets run out.
	ApplyEndTime time.Time
	// LotteryResultTime is when lottery results are announced. Zero when not
	// announced, and always zero on a first-come sale.
	LotteryResultTime time.Time
	// DiscoveredTime is the timestamp when this phase row was first inserted. It is
	// set by the database DEFAULT and never overwritten on update. The reminder
	// scan uses it as the first-sight guard: stages whose natural trigger is
	// before DiscoveredTime are not fired (the phase was discovered after that
	// milestone had already passed).
	DiscoveredTime time.Time
}

// HasApplicationEnded reports whether the phase's application has ended at now.
// It ends at the apply end time; a first-come sale without one ends once it
// opens.
func (p *SalesPhase) HasApplicationEnded(now time.Time) bool {
	end := p.ApplyEndTime
	if end.IsZero() {
		end = p.ApplyStartTime
	}
	return !now.Before(end)
}

// SalesPhaseCandidate carries the data for a single phase extracted by the
// Gemini searcher before it is matched against the database.
type SalesPhaseCandidate struct {
	// SeriesID is the series this candidate belongs to.
	SeriesID string
	// Method is the ticket acquisition mechanism.
	Method SalesMethod
	// ApplyStartTime is the start of the application window. Required.
	ApplyStartTime time.Time
	// ApplyEndTime is the end of the application window. Required for a lottery.
	ApplyEndTime time.Time
	// LotteryResultTime is when lottery results are announced. Lottery only.
	LotteryResultTime time.Time
}

// Validate checks the method and milestone rules of a sales phase: the method
// is LOTTERY or FIRST_COME, the apply start time is known, a lottery has an
// apply end time, the apply end time is after the apply start time, and a
// lottery result time appears only on a lottery and is not before the apply
// end time. It returns a stdlib error describing the first failure; callers
// wrap it with the appropriate apperr code.
func (c *SalesPhaseCandidate) Validate() error {
	if !c.Method.IsValid() {
		return fmt.Errorf("method must be LOTTERY or FIRST_COME, got %d", c.Method)
	}
	if c.ApplyStartTime.IsZero() {
		return errors.New("apply start time is required")
	}
	if c.Method == SalesMethodLottery && c.ApplyEndTime.IsZero() {
		return errors.New("a lottery requires an apply end time")
	}
	if !c.ApplyEndTime.IsZero() && !c.ApplyEndTime.After(c.ApplyStartTime) {
		return errors.New("apply end time must be after apply start time")
	}
	if !c.LotteryResultTime.IsZero() {
		if c.Method != SalesMethodLottery {
			return errors.New("only a lottery may have a lottery result time")
		}
		if c.LotteryResultTime.Before(c.ApplyEndTime) {
			return errors.New("lottery result time must not be before apply end time")
		}
	}
	return nil
}

// UpsertOutcome signals whether Upsert inserted a new phase or updated an
// existing one. The discovery use case uses this to decide whether to publish
// a SALES_PHASE.discovered announcement event (insert only — re-discovery of
// an existing phase must not re-announce).
type UpsertOutcome int8

const (
	// UpsertOutcomeSkipped means the candidate was dropped by the persistence
	// guard (zero apply_start_time). No row was written.
	UpsertOutcomeSkipped UpsertOutcome = 0
	// UpsertOutcomeInserted means a new sales_phases row was created.
	UpsertOutcomeInserted UpsertOutcome = 1
	// UpsertOutcomeUpdated means an existing row (matched on series, method
	// and apply start date in Japan time) was updated in place.
	UpsertOutcomeUpdated UpsertOutcome = 2
)

// SalesSeriesRef identifies one of an artist's known upcoming series, supplied
// to the searcher so it can attribute a discovered sale phase to the correct
// series_id. The event period helps the model disambiguate when an artist has
// multiple concurrent tours and bounds the plausible sale dates.
type SalesSeriesRef struct {
	// SeriesID is the stable identifier of the series.
	SeriesID string
	// Title is the display title of the series (tour).
	Title string
	// EventDates are the known local dates of the series' upcoming events.
	EventDates []time.Time
}

// SalesPhaseSearchInput is the per-artist input to the sales-phase searcher.
// Discovery issues ONE grounded search per artist (not per series) to cut
// grounding cost and to let the model see all of the artist's tours at once.
type SalesPhaseSearchInput struct {
	// ArtistName is the performing artist whose sales phases are searched.
	ArtistName string
	// OfficialSiteURL seeds the URL-context tool so the model reads the real
	// page instead of answering from memory. Always present.
	OfficialSiteURL string
	// Series are the artist's known upcoming series; a discovered phase is
	// attributed back to one of these series_ids (unmappable phases dropped).
	Series []*SalesSeriesRef
}

// SalesPhaseRepository defines the data access interface for [SalesPhase].
type SalesPhaseRepository interface {
	// Upsert converges the candidate onto an existing phase or inserts a new
	// one. A phase with the same series and method whose apply start time falls
	// on the same calendar day in Japan time (Asia/Tokyo) is the same phase: its
	// apply start, apply end and lottery result times are replaced with the
	// candidate's (a zero value clears the stored one), and its id, series,
	// method and discovered time are kept (return UpsertOutcomeUpdated).
	// Otherwise a new row with a fresh id is inserted (return
	// UpsertOutcomeInserted).
	//
	// A candidate with a zero ApplyStartTime is skipped: Upsert returns ("",
	// UpsertOutcomeSkipped, nil) and writes nothing. Upsert is upsert-only: it
	// never deletes existing rows.
	//
	// Returns the affected phase's surrogate ID alongside the outcome:
	//   - On UpsertOutcomeInserted: the newly generated UUID.
	//   - On UpsertOutcomeUpdated: the ID of the row that was updated.
	//   - On UpsertOutcomeSkipped: "".
	//
	// # Possible errors
	//
	//  - FailedPrecondition: If the referenced series does not exist.
	//  - InvalidArgument: If the candidate's SeriesID is empty or the candidate
	//    breaks a method or milestone rule ([SalesPhaseCandidate.Validate]).
	Upsert(ctx context.Context, candidate *SalesPhaseCandidate) (string, UpsertOutcome, error)

	// ListPhasesWithPendingMilestones returns every sales phase that has at
	// least one reminder milestone still pending or recently due. A phase is
	// included when its apply_start_at is no more than lookahead in the future
	// AND the latest of its milestone timestamps (apply_start_at, apply_end_at,
	// lottery_result_at) is no earlier than now minus lookbackMargin.
	//
	// This correctly includes a phase whose apply_start_at is weeks in the
	// past but whose lottery_result_at is imminent — the old apply_start_at-
	// only filter would silently miss that phase's RESULT_DAY stage.
	//
	// # Possible errors
	//
	//  - InvalidArgument: If lookahead is not positive or lookbackMargin is negative.
	ListPhasesWithPendingMilestones(ctx context.Context, lookahead, lookbackMargin time.Duration) ([]*SalesPhase, error)

	// GetBySeries returns every sales phase of the given series, ordered by
	// apply start time, earliest first. A series with no phase, or one that
	// does not exist, returns an empty list.
	//
	// # Possible errors
	//
	//  - InvalidArgument: If seriesID is empty.
	GetBySeries(ctx context.Context, seriesID string) ([]*SalesPhase, error)
}

// SalesPhaseSearcher discovers upcoming ticket-sales phases for an artist
// using an external grounded search, issuing one call per artist.
type SalesPhaseSearcher interface {
	// SearchSalesPhases issues a single grounded search for the artist in the
	// input and returns the sales whose application has not started yet, each
	// attributed to one of the input's series_ids. Every returned candidate
	// passes [SalesPhaseCandidate.Validate]. It does NOT resolve which
	// individual events a phase covers.
	//
	// An empty result with a nil error means the search succeeded and found no
	// sale. Any failure of the search returns an error, so the caller can retry
	// on a later run. No series given returns no phases without searching.
	//
	// # Possible errors
	//
	//  - InvalidArgument: If the service rejects the request.
	//  - Unavailable: If the external search service is unreachable or fails.
	//  - ResourceExhausted: If the service's quota or spend cap is reached.
	//  - DeadlineExceeded: If the call times out.
	//  - Internal: If the response is empty, cut off or not valid JSON.
	SearchSalesPhases(ctx context.Context, in *SalesPhaseSearchInput) ([]*SalesPhaseCandidate, error)
}

// SalesPhaseReminderRepository persists the sent-log for sales-phase reminder
// notifications. It enforces the once-only delivery guarantee keyed by
// (user_id, sales_phase_id, stage).
type SalesPhaseReminderRepository interface {
	// RecordSent records that the given stage reminder was dispatched to the
	// user for the given phase. The operation is idempotent due to the
	// UNIQUE constraint on (user_id, sales_phase_id, stage); a duplicate
	// insert is silently swallowed (not an error).
	//
	// # Possible errors
	//
	//  - Internal: unexpected database failure.
	RecordSent(ctx context.Context, userID, phaseID string, stage ReminderStage) error

	// AlreadySent reports whether the given stage reminder has already been
	// dispatched to the user for the given phase.
	//
	// # Possible errors
	//
	//  - Internal: unexpected database failure.
	AlreadySent(ctx context.Context, userID, phaseID string, stage ReminderStage) (bool, error)

	// ListSentStages returns a map of userID → set of stages already sent for
	// the given phase. Used by the reminder scan to batch the per-phase
	// already-sent check instead of issuing one query per (user, stage) pair.
	//
	// # Possible errors
	//
	//  - Internal: unexpected database failure.
	ListSentStages(ctx context.Context, phaseID string, userIDs []string) (map[string]map[ReminderStage]bool, error)
}
