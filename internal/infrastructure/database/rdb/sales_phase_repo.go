package rdb

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
)

// SalesPhaseRepository implements [entity.SalesPhaseRepository] for PostgreSQL.
type SalesPhaseRepository struct {
	db *Database
}

// Compile-time interface compliance check.
var _ entity.SalesPhaseRepository = (*SalesPhaseRepository)(nil)

// NewSalesPhaseRepository creates a new SalesPhaseRepository instance.
func NewSalesPhaseRepository(db *Database) *SalesPhaseRepository {
	return &SalesPhaseRepository{db: db}
}

const (
	// matchByApplyStartQuery returns the ID of the existing sales_phases row for
	// the given series whose apply_start_at equals the candidate's. This is the
	// convergence key: same series + same application start instant identify the
	// same real sales window, independent of channel/sequence reclassification.
	// apply_start_at is a timestamptz (absolute instant), so the equality is
	// timezone-agnostic and correct for non-JST events.
	matchByApplyStartQuery = `
		SELECT id
		FROM sales_phases
		WHERE series_id = $1 AND apply_start_at = $2
		ORDER BY id ASC
		LIMIT 1
	`

	// updatePhaseQuery updates the descriptive (last-write-wins) fields of an
	// existing phase row. series_id and apply_start_at (the identity) and
	// discovered_at (the first-sight guard) are intentionally not updated.
	updatePhaseQuery = `
		UPDATE sales_phases
		SET method              = $2,
		    channel             = $3,
		    provider_name       = $4,
		    sequence            = $5,
		    apply_end_at        = $6,
		    lottery_result_at   = $7,
		    payment_deadline_at = $8,
		    url                 = $9
		WHERE id = $1
	`

	// insertPhaseQuery inserts a new sales_phases row and returns the
	// DB-generated discovered_at so the in-memory entity is fully populated.
	insertPhaseQuery = `
		INSERT INTO sales_phases (
			id, series_id, method, channel, provider_name,
			sequence, apply_start_at, apply_end_at, lottery_result_at,
			payment_deadline_at, url
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		RETURNING discovered_at
	`

	// listPhasesWithPendingMilestonesQuery returns phases that have at least
	// one reminder milestone that is either currently due or will become due
	// within the lookahead window.
	//
	// A milestone is "pending" when its trigger time is still in the future or
	// very recently past (within the lookback grace margin). The query therefore
	// selects phases where:
	//   GREATEST(apply_start_at,
	//            COALESCE(apply_end_at,   '-infinity'),
	//            COALESCE(lottery_result_at, '-infinity'))
	//     >= NOW() - make_interval(secs => $2)   -- lookback margin (grace period)
	//
	// AND the phase is "active" in the forward window:
	//   apply_start_at <= NOW() + make_interval(secs => $1)
	//
	// The lookback margin ($2, typically a small number like 3600s = 1h) prevents
	// dropping a phase whose last milestone fired slightly before the scan ran.
	// Together the two conditions ensure:
	//   - A phase whose apply_start was weeks ago but whose lottery_result is
	//     tomorrow IS included (GREATEST covers lottery_result_at).
	//   - A phase that is entirely in the future beyond the window is excluded.
	//   - A fully completed phase (all milestones > lookback in the past) is excluded.
	//
	// $1 = lookahead seconds, $2 = lookback margin seconds.
	listPhasesWithPendingMilestonesQuery = `
		SELECT id, series_id, method, channel, provider_name,
		       sequence, apply_start_at, apply_end_at, lottery_result_at,
		       payment_deadline_at, url, discovered_at
		FROM sales_phases
		WHERE apply_start_at <= NOW() + make_interval(secs => $1)
		  AND GREATEST(
		        apply_start_at,
		        COALESCE(apply_end_at,       '-infinity'::timestamptz),
		        COALESCE(lottery_result_at,  '-infinity'::timestamptz)
		      ) >= NOW() - make_interval(secs => $2)
		ORDER BY apply_start_at ASC
	`

	// getBySeriesQuery fetches all phases belonging to a series.
	getBySeriesQuery = `
		SELECT id, series_id, method, channel, provider_name,
		       sequence, apply_start_at, apply_end_at, lottery_result_at,
		       payment_deadline_at, url, discovered_at
		FROM sales_phases
		WHERE series_id = $1
		ORDER BY apply_start_at ASC
	`
)

// Upsert converges the candidate onto an existing phase matched on
// (series_id, apply_start_at) or inserts a new row. It is a no-op when the
// candidate fails the persistence guard (zero ApplyStartTime), returning
// ("", UpsertOutcomeSkipped, nil). It is upsert-only: it never deletes rows.
//
// Returns the affected phase ID alongside the outcome:
//   - UpsertOutcomeInserted: the newly generated UUID.
//   - UpsertOutcomeUpdated: the ID of the row that was updated in-place.
//   - UpsertOutcomeSkipped: "".
func (r *SalesPhaseRepository) Upsert(ctx context.Context, candidate *entity.SalesPhaseCandidate) (string, entity.UpsertOutcome, error) {
	if candidate == nil {
		return "", entity.UpsertOutcomeSkipped, nil
	}
	if candidate.SeriesID == "" {
		return "", entity.UpsertOutcomeSkipped, apperr.New(codes.InvalidArgument, "sales phase candidate SeriesID must not be empty")
	}

	// Persistence guard: skip unless apply_start_time is known. A known start is
	// the sole persistence requirement.
	if candidate.ApplyStartTime.IsZero() {
		r.db.logger.Info(ctx, "sales_phase_repo: dropping candidate with zero apply_start_time",
			slog.String("series_id", candidate.SeriesID),
		)
		return "", entity.UpsertOutcomeSkipped, nil
	}

	// Match an existing phase for this series on the application start instant.
	var existingID string
	err := r.db.Pool.QueryRow(ctx, matchByApplyStartQuery,
		candidate.SeriesID,
		candidate.ApplyStartTime,
	).Scan(&existingID)

	switch {
	case err == nil:
		// Same (series_id, apply_start_at) — update descriptive fields in place.
		if err := r.updateExisting(ctx, existingID, candidate); err != nil {
			return "", entity.UpsertOutcomeSkipped, err
		}
		return existingID, entity.UpsertOutcomeUpdated, nil
	case isNoRows(err):
		// No match — insert a new phase row.
		newID := newPhaseID()
		if err := r.insertNewWithID(ctx, newID, candidate); err != nil {
			return "", entity.UpsertOutcomeSkipped, err
		}
		return newID, entity.UpsertOutcomeInserted, nil
	default:
		return "", entity.UpsertOutcomeSkipped, toAppErr(err, "failed to match sales phase by apply_start_at",
			slog.String("series_id", candidate.SeriesID),
		)
	}
}

// updateExisting applies last-write-wins logic to an existing phase row's
// descriptive fields.
func (r *SalesPhaseRepository) updateExisting(
	ctx context.Context,
	phaseID string,
	c *entity.SalesPhaseCandidate,
) error {
	_, err := r.db.Pool.Exec(ctx, updatePhaseQuery,
		phaseID,
		int16(c.Method),
		int16(c.Channel),
		nullableString(c.ProviderName),
		c.Sequence,
		nullableTime(c.ApplyEndTime),
		nullableTime(c.LotteryResultTime),
		nullableTime(c.PaymentDeadlineTime),
		nullableString(c.URL),
	)
	if err != nil {
		return toAppErr(err, "failed to update sales phase", slog.String("phase_id", phaseID))
	}
	return nil
}

// insertNewWithID inserts a fresh sales_phases row with the provided phaseID.
func (r *SalesPhaseRepository) insertNewWithID(ctx context.Context, phaseID string, c *entity.SalesPhaseCandidate) error {
	// Use QueryRow to consume the RETURNING discovered_at. The value is discarded
	// here because insertNewWithID doesn't return the full entity; the field is
	// populated on subsequent reads via scanPhaseRows.
	var discardDiscoveredAt time.Time
	if err := r.db.Pool.QueryRow(ctx, insertPhaseQuery,
		phaseID,
		c.SeriesID,
		int16(c.Method),
		int16(c.Channel),
		nullableString(c.ProviderName),
		c.Sequence,
		c.ApplyStartTime,
		nullableTime(c.ApplyEndTime),
		nullableTime(c.LotteryResultTime),
		nullableTime(c.PaymentDeadlineTime),
		nullableString(c.URL),
	).Scan(&discardDiscoveredAt); err != nil {
		return toAppErr(err, "failed to insert sales phase", slog.String("series_id", c.SeriesID))
	}
	return nil
}

// ListPhasesWithPendingMilestones returns every sales phase that has at least
// one reminder milestone still pending or recently due. A phase is included
// when:
//
//   - Its apply_start_at is no more than lookahead seconds in the future
//     (the phase has started or is about to start), AND
//   - The GREATEST of its three milestone timestamps
//     (apply_start_at, apply_end_at, lottery_result_at) is no earlier than
//     now minus lookbackMargin seconds (at least one milestone is still
//     relevant).
//
// lookahead (seconds) is typically the reminder-scan horizon (e.g. 7 days).
// lookbackMargin (seconds) is a grace period that prevents dropping a phase
// whose last milestone fired just before this scan ran (e.g. 3 600 s = 1 h).
//
// This correctly includes a phase whose apply_start_at is weeks in the past
// but whose lottery_result_at is tomorrow — the old apply_start_at-only
// filter would silently miss that phase's RESULT_DAY stage.
func (r *SalesPhaseRepository) ListPhasesWithPendingMilestones(ctx context.Context, lookahead, lookbackMargin time.Duration) ([]*entity.SalesPhase, error) {
	if lookahead <= 0 {
		return nil, apperr.New(codes.InvalidArgument, "sales phase lookahead must be positive")
	}
	if lookbackMargin < 0 {
		return nil, apperr.New(codes.InvalidArgument, "sales phase lookback margin must be non-negative")
	}

	rows, err := r.db.Pool.Query(ctx, listPhasesWithPendingMilestonesQuery,
		lookahead.Seconds(),
		lookbackMargin.Seconds(),
	)
	if err != nil {
		return nil, toAppErr(err, "failed to list phases with pending milestones",
			slog.Float64("lookahead_secs", lookahead.Seconds()),
			slog.Float64("lookback_margin_secs", lookbackMargin.Seconds()),
		)
	}
	defer rows.Close()

	return scanPhaseRows(rows)
}

// GetBySeries returns all sales phases for the given series.
func (r *SalesPhaseRepository) GetBySeries(ctx context.Context, seriesID string) ([]*entity.SalesPhase, error) {
	if seriesID == "" {
		return nil, apperr.New(codes.InvalidArgument, "series ID must not be empty")
	}

	rows, err := r.db.Pool.Query(ctx, getBySeriesQuery, seriesID)
	if err != nil {
		return nil, toAppErr(err, "failed to get sales phases by series", slog.String("series_id", seriesID))
	}
	defer rows.Close()

	return scanPhaseRows(rows)
}

// ----- helpers -----

// scanPhaseRows scans all rows returned by a sales_phases SELECT query into
// a slice of SalesPhase entities.
func scanPhaseRows(rows pgx.Rows) ([]*entity.SalesPhase, error) {
	var phases []*entity.SalesPhase
	for rows.Next() {
		var (
			p                 entity.SalesPhase
			method, channel   int16
			providerName      *string
			applyEndAt        *time.Time
			lotteryResultAt   *time.Time
			paymentDeadlineAt *time.Time
			rawURL            *string
		)
		if err := rows.Scan(
			&p.ID,
			&p.SeriesID,
			&method,
			&channel,
			&providerName,
			&p.Sequence,
			&p.ApplyStartTime,
			&applyEndAt,
			&lotteryResultAt,
			&paymentDeadlineAt,
			&rawURL,
			&p.DiscoveredTime,
		); err != nil {
			return nil, toAppErr(err, "failed to scan sales phase row")
		}
		p.Method = entity.SalesMethod(method)
		p.Channel = entity.SalesChannel(channel)
		if providerName != nil {
			p.ProviderName = *providerName
		}
		if applyEndAt != nil {
			p.ApplyEndTime = *applyEndAt
		}
		if lotteryResultAt != nil {
			p.LotteryResultTime = *lotteryResultAt
		}
		if paymentDeadlineAt != nil {
			p.PaymentDeadlineTime = *paymentDeadlineAt
		}
		if rawURL != nil {
			p.URL = *rawURL
		}
		phases = append(phases, &p)
	}
	if err := rows.Err(); err != nil {
		return nil, toAppErr(err, "sales phase row iteration ended with error")
	}
	return phases, nil
}

// nullableString returns a *string pointer (nil for empty strings) so pgx
// maps the value to SQL NULL rather than an empty string.
func nullableString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// nullableTime returns a *time.Time pointer (nil for zero times) so pgx
// maps the value to SQL NULL when no time is known.
func nullableTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

// isNoRows reports whether err is the pgx "no rows" sentinel.
func isNoRows(err error) bool {
	return err == pgx.ErrNoRows
}

// newPhaseID generates a new UUIDv7 string for a sales_phases primary key,
// matching the UUIDv7 convention used across the system.
func newPhaseID() string {
	return uuid.Must(uuid.NewV7()).String()
}
