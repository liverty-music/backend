package rdb

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

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
	// upsertPhaseQuery converges a candidate onto the phase with the same
	// series, method and apply start date in Japan time, or inserts a new row.
	// The match uses the uq_sales_phases_series_method_start_date key over the
	// generated apply_start_date_jst column. On a match the milestones are
	// replaced (NULL clears a stored value) and id, series_id, method and
	// discovered_at are kept. xmax = 0 is true only for a freshly inserted row.
	upsertPhaseQuery = `
		INSERT INTO sales_phases (
			id, series_id, method, apply_start_at, apply_end_at, lottery_result_at
		) VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (series_id, method, apply_start_date_jst) DO UPDATE
		SET apply_start_at    = EXCLUDED.apply_start_at,
		    apply_end_at      = EXCLUDED.apply_end_at,
		    lottery_result_at = EXCLUDED.lottery_result_at
		RETURNING id, (xmax = 0) AS inserted
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
		SELECT id, series_id, method, apply_start_at, apply_end_at,
		       lottery_result_at, discovered_at
		FROM sales_phases
		WHERE apply_start_at <= NOW() + make_interval(secs => $1)
		  AND GREATEST(
		        apply_start_at,
		        COALESCE(apply_end_at,       '-infinity'::timestamptz),
		        COALESCE(lottery_result_at,  '-infinity'::timestamptz)
		      ) >= NOW() - make_interval(secs => $2)
		ORDER BY apply_start_at ASC
	`

	// getBySeriesQuery fetches all phases belonging to a series, earliest
	// apply start first.
	getBySeriesQuery = `
		SELECT id, series_id, method, apply_start_at, apply_end_at,
		       lottery_result_at, discovered_at
		FROM sales_phases
		WHERE series_id = $1
		ORDER BY apply_start_at ASC
	`
)

// Upsert converges the candidate onto the phase with the same series, method
// and apply start date in Japan time, or inserts a new row, in one statement.
// It is a no-op when the candidate has a zero ApplyStartTime, returning
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

	// Persistence guard: a phase without a known start is skipped, not stored.
	if candidate.ApplyStartTime.IsZero() {
		r.db.logger.Info(ctx, "sales_phase_repo: dropping candidate with zero apply_start_time",
			slog.String("series_id", candidate.SeriesID),
		)
		return "", entity.UpsertOutcomeSkipped, nil
	}
	if err := candidate.Validate(); err != nil {
		return "", entity.UpsertOutcomeSkipped, apperr.New(codes.InvalidArgument, err.Error())
	}

	var (
		id       string
		inserted bool
	)
	if err := r.db.Pool.QueryRow(ctx, upsertPhaseQuery,
		newPhaseID(),
		candidate.SeriesID,
		int16(candidate.Method),
		candidate.ApplyStartTime,
		nullableTime(candidate.ApplyEndTime),
		nullableTime(candidate.LotteryResultTime),
	).Scan(&id, &inserted); err != nil {
		return "", entity.UpsertOutcomeSkipped, toAppErr(err, "failed to upsert sales phase",
			slog.String("series_id", candidate.SeriesID),
		)
	}
	if inserted {
		return id, entity.UpsertOutcomeInserted, nil
	}
	return id, entity.UpsertOutcomeUpdated, nil
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

// GetBySeries returns all sales phases for the given series, earliest apply
// start first.
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
			p               entity.SalesPhase
			method          int16
			applyEndAt      sql.NullTime
			lotteryResultAt sql.NullTime
		)
		if err := rows.Scan(
			&p.ID,
			&p.SeriesID,
			&method,
			&p.ApplyStartTime,
			&applyEndAt,
			&lotteryResultAt,
			&p.DiscoveredTime,
		); err != nil {
			return nil, toAppErr(err, "failed to scan sales phase row")
		}
		p.Method = entity.SalesMethod(method)
		if applyEndAt.Valid {
			p.ApplyEndTime = applyEndAt.Time
		}
		if lotteryResultAt.Valid {
			p.LotteryResultTime = lotteryResultAt.Time
		}
		phases = append(phases, &p)
	}
	if err := rows.Err(); err != nil {
		return nil, toAppErr(err, "sales phase row iteration ended with error")
	}
	return phases, nil
}

// nullableTime returns a *time.Time pointer (nil for zero times) so pgx
// maps the value to SQL NULL when no time is known.
func nullableTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

// newPhaseID generates a new UUIDv7 string for a sales_phases primary key,
// matching the UUIDv7 convention used across the system. It routes through the
// central entity ID helper so every table shares one generation site.
func newPhaseID() string {
	return entity.NewID()
}
