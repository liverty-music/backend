package entity

import (
	"context"
	"time"
)

// SalesMethod classifies the ticket acquisition mechanism for a sales phase.
//
// Values MUST exactly match the proto enum liverty_music.entity.v1.SalesMethod
// so the Section-5 RPC adapter can cast Go int16 ↔ proto enum by value.
type SalesMethod int16

const (
	// SalesMethodUnspecified is the zero value and a valid persisted state
	// meaning "method not yet determined". Mirrors SALES_METHOD_UNSPECIFIED = 0.
	SalesMethodUnspecified SalesMethod = 0
	// SalesMethodLottery is a ballot-based allocation: fans apply during the
	// window and a random draw determines winners. Mirrors LOTTERY = 1.
	SalesMethodLottery SalesMethod = 1
	// SalesMethodFirstCome is a sequential on-sale: tickets sell in order of
	// purchase until exhausted. Mirrors FIRST_COME = 2.
	SalesMethodFirstCome SalesMethod = 2
)

// SalesChannel identifies the distribution channel through which a ticket
// sales phase is conducted.
//
// Values MUST exactly match the proto enum liverty_music.entity.v1.SalesChannel
// so the Section-5 RPC adapter can cast Go int16 ↔ proto enum by value.
//
// Concrete play-guide providers (e+, ローチケ, チケットぴあ, CN Playguide, …) are
// NOT distinct channel values — they all map to [SalesChannelPlayguide], and
// their verbatim name is stored in [SalesPhase.ProviderName].
type SalesChannel int16

const (
	// SalesChannelUnspecified is the zero value and a valid persisted state
	// meaning "channel not yet determined". Mirrors SALES_CHANNEL_UNSPECIFIED = 0.
	SalesChannelUnspecified SalesChannel = 0
	// SalesChannelFanClub is a fan-club exclusive presale. Mirrors FAN_CLUB = 1.
	SalesChannelFanClub SalesChannel = 1
	// SalesChannelOfficial is an official-but-non-FC gate: artist/label site or
	// official app. Mirrors OFFICIAL = 2.
	SalesChannelOfficial SalesChannel = 2
	// SalesChannelPlayguide is a third-party play-guide ticketing platform
	// (e+, ぴあ, ローチケ, CN Playguide, …). The concrete provider is stored in
	// SalesPhase.ProviderName. Mirrors PLAYGUIDE = 3.
	SalesChannelPlayguide SalesChannel = 3
	// SalesChannelCreditCard is a credit-card affiliated presale. Mirrors
	// CREDIT_CARD = 4.
	SalesChannelCreditCard SalesChannel = 4
	// SalesChannelMobileCarrier is a mobile-carrier presale (docomo, au,
	// SoftBank). Mirrors MOBILE_CARRIER = 5.
	SalesChannelMobileCarrier SalesChannel = 5
	// SalesChannelGeneral is a public general on-sale with no membership or
	// affiliation requirement. Mirrors GENERAL = 6.
	SalesChannelGeneral SalesChannel = 6
)

// ReminderStage identifies which point in the sales lifecycle a reminder targets.
//
// Stages are defined by the design (Decision 6b) and deliberately omit a
// payment-deadline stage — only lottery winners pay and win/loss gating is
// deferred to a future session. The migration's sales_phase_reminders.stage
// CHECK (1..10) leaves room for future stages.
type ReminderStage int16

const (
	// ReminderStageApplyOpen fires at apply_start_time: "sales are now open."
	ReminderStageApplyOpen ReminderStage = 1
	// ReminderStageApplyClose24H fires 24 hours before apply_end_time:
	// "last day to apply."
	ReminderStageApplyClose24H ReminderStage = 2
	// ReminderStageApplyClose1H fires 1 hour before apply_end_time:
	// "one hour left to apply."
	ReminderStageApplyClose1H ReminderStage = 3
	// ReminderStageResultDay fires at 09:00 in the user's timezone on the
	// calendar day of lottery_result_time: "lottery results are out today."
	ReminderStageResultDay ReminderStage = 4
)

// SalesPhase represents a single ticket-sales window for a series. A phase
// belongs to a series and applies to it as a whole — there is no per-event
// coverage subset.
//
// The application layer converges re-discovered phases onto existing rows by
// matching on (series_id, apply_start_time); the surrogate ID is the only
// uniqueness key at the database layer and the stable handle reminders
// reference.
type SalesPhase struct {
	// ID is the unique identifier for this phase (UUIDv7).
	ID string
	// SeriesID is the parent series that owns this sales phase.
	SeriesID string
	// Method is the ticket acquisition mechanism (lottery vs. FCFS).
	Method SalesMethod
	// Channel is the distribution channel (FC, general, partner platform, etc.).
	Channel SalesChannel
	// ProviderName is the verbatim provider name from the source (e.g. "e+", "ローチケ").
	// Empty when the provider is indeterminate.
	ProviderName string
	// Sequence is the 0-based ordinal within the same channel when a channel runs
	// multiple rounds. It does not uniquely identify a phase on its own.
	Sequence int
	// ApplyStartTime is the start of the application or on-sale window. Required.
	ApplyStartTime time.Time
	// ApplyEndTime is the end of the application window or close of the on-sale.
	// Zero value means unknown.
	ApplyEndTime time.Time
	// LotteryResultTime is when lottery results are announced.
	// Zero value means unknown or N/A (FCFS phases).
	LotteryResultTime time.Time
	// PaymentDeadlineTime is the payment deadline after a successful lottery.
	// Zero value means unknown or N/A (FCFS phases).
	PaymentDeadlineTime time.Time
	// URL is the direct link to the sales page for this phase. Empty when unknown.
	URL string
	// DiscoveredTime is the timestamp when this phase row was first inserted. It is
	// set by the database DEFAULT and never overwritten on update. The reminder
	// scan uses it as the first-sight guard: stages whose natural trigger is
	// before DiscoveredTime are not fired (the phase was discovered after that
	// milestone had already passed).
	DiscoveredTime time.Time
}

// SalesPhaseCandidate carries the data for a single phase extracted by the
// Gemini searcher before it is matched against the database.
type SalesPhaseCandidate struct {
	// SeriesID is the series this candidate belongs to.
	SeriesID string
	// Method, Channel, ProviderName, Sequence carry the structured sales data.
	Method       SalesMethod
	Channel      SalesChannel
	ProviderName string
	Sequence     int
	// ApplyStartTime is the only mandatory timestamp. A candidate is dropped
	// if this is zero.
	ApplyStartTime      time.Time
	ApplyEndTime        time.Time
	LotteryResultTime   time.Time
	PaymentDeadlineTime time.Time
	// URL is the direct link to the sales page, if known.
	URL string
	// SourceURL is the page the searcher extracted this data from.
	SourceURL string
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
	// UpsertOutcomeUpdated means an existing row (matched on (series_id,
	// apply_start_time)) was updated in place.
	UpsertOutcomeUpdated UpsertOutcome = 2
)

// SalesSeriesCandidate pairs a series with its candidate events for the
// sales-phase discovery job. The job builds one of these per series and
// passes it to the searcher.
type SalesSeriesCandidate struct {
	// SeriesID is the stable identifier of the series.
	SeriesID string
	// SeriesTitle is the display title used to ground the Gemini search.
	SeriesTitle string
	// ArtistName is a representative performing artist for this series, used
	// in the search prompt.
	ArtistName string
}

// SalesPhaseRepository defines the data access interface for [SalesPhase].
type SalesPhaseRepository interface {
	// Upsert converges the candidate onto an existing phase or inserts a new
	// one. Matching is on (series_id, apply_start_time): when a row with the
	// same series and the same apply_start_at exists, its descriptive fields
	// (method, channel, provider_name, sequence, the other timestamps, url) are
	// updated in place last-write-wins (return UpsertOutcomeUpdated); otherwise
	// a new row with a fresh id is inserted (return UpsertOutcomeInserted).
	// channel, sequence, and the other fields never participate in identity, so
	// a reclassification across runs updates rather than forks a duplicate.
	//
	// The caller must ensure candidate.ApplyStartTime is non-zero; callers that
	// do not should drop the candidate before calling Upsert. Upsert itself
	// enforces the guard and returns ("", UpsertOutcomeSkipped, nil) when it
	// fails. Upsert is upsert-only: it never deletes existing rows.
	//
	// Returns the affected phase's surrogate ID alongside the outcome:
	//   - On UpsertOutcomeInserted: the newly generated UUID.
	//   - On UpsertOutcomeUpdated: the ID of the row that was updated.
	//   - On UpsertOutcomeSkipped: "".
	//
	// # Possible errors
	//
	//  - FailedPrecondition: If the referenced series does not exist.
	//  - InvalidArgument: If the candidate's SeriesID is empty.
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

	// GetBySeries returns all sales phases for the given series.
	//
	// # Possible errors
	//
	//  - InvalidArgument: If seriesID is empty.
	GetBySeries(ctx context.Context, seriesID string) ([]*SalesPhase, error)
}

// SalesPhaseSearcher discovers upcoming ticket-sales phases for an artist's
// series using an external grounded search.
type SalesPhaseSearcher interface {
	// SearchSalesPhases returns all series-level sales-phase candidates found
	// for the given artist name and series title. It does NOT resolve which
	// individual events a phase covers — each candidate is a series-level record.
	//
	// An empty result with a nil error means the searcher found no phases (normal
	// outcome). Only infrastructure failures return a non-nil error.
	//
	// # Possible errors
	//
	//  - Unavailable: If the external search service is down.
	//  - Internal: Unexpected failure during extraction or coercion.
	SearchSalesPhases(
		ctx context.Context,
		artistName string,
		seriesTitle string,
		seriesID string,
	) ([]*SalesPhaseCandidate, error)
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
