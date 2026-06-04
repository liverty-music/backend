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
	// findOverlappingPhaseQuery returns the ID of an existing sales_phases row
	// for the given series whose covered event set overlaps with the supplied
	// event IDs AND whose channel is compatible with the candidate's channel.
	//
	// Channel-compatibility rule ($3 = candidate channel smallint):
	//   existing.channel = 0 (UNSPECIFIED)  → matches any candidate channel (reclassification converges)
	//   candidate channel = 0 (UNSPECIFIED) → matches any existing channel (reclassification converges)
	//   both determined and equal           → matches
	//   both determined and different       → NO match (separate row for each channel)
	//
	// This prevents an FC presale (channel=1) and a general on-sale (channel=6)
	// covering the same event from collapsing via last-write-wins.
	//
	// GROUP BY + ORDER BY implement the documented channel-preference: when both
	// an UNSPECIFIED-channel row and a determined-channel row match (because
	// UNSPECIFIED matches any candidate channel), BOOL_OR(sp.channel = $3)
	// is true for the exact match and false for the UNSPECIFIED row, so the
	// exact match sorts first. Without this preference LIMIT 1 on UUID order
	// could pick the UNSPECIFIED row and overwrite it, orphaning the determined row.
	findOverlappingPhaseQuery = `
		SELECT sp.id
		FROM sales_phases sp
		JOIN event_sales_phases esp ON esp.sales_phase_id = sp.id
		WHERE sp.series_id = $1
		  AND esp.event_id = ANY($2::uuid[])
		  AND (sp.channel = 0 OR $3::smallint = 0 OR sp.channel = $3::smallint)
		GROUP BY sp.id
		ORDER BY BOOL_OR(sp.channel = $3::smallint) DESC, sp.id ASC
		LIMIT 1
	`

	// updatePhaseQuery updates the mutable fields of an existing phase row.
	// anchor_event_id is intentionally excluded — it is immutable after insert.
	updatePhaseQuery = `
		UPDATE sales_phases
		SET method              = $2,
		    channel             = $3,
		    provider_name       = $4,
		    sequence            = $5,
		    apply_start_at      = $6,
		    apply_end_at        = $7,
		    lottery_result_at   = $8,
		    payment_deadline_at = $9,
		    url                 = $10
		WHERE id = $1
	`

	// deleteEventSalesPhasesQuery removes all covered-event links for a phase.
	deleteEventSalesPhasesQuery = `
		DELETE FROM event_sales_phases WHERE sales_phase_id = $1
	`

	// insertEventSalesPhasesQuery bulk-inserts the covered event links for a phase.
	insertEventSalesPhasesQuery = `
		INSERT INTO event_sales_phases (sales_phase_id, event_id)
		SELECT $1, unnest($2::uuid[])
		ON CONFLICT DO NOTHING
	`

	// insertPhaseQuery inserts a new sales_phases row and returns the
	// DB-generated discovered_at so the in-memory entity is fully populated.
	insertPhaseQuery = `
		INSERT INTO sales_phases (
			id, series_id, anchor_event_id, method, channel, provider_name,
			sequence, apply_start_at, apply_end_at, lottery_result_at,
			payment_deadline_at, url
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
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
		SELECT id, series_id, anchor_event_id, method, channel, provider_name,
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
		SELECT id, series_id, anchor_event_id, method, channel, provider_name,
		       sequence, apply_start_at, apply_end_at, lottery_result_at,
		       payment_deadline_at, url, discovered_at
		FROM sales_phases
		WHERE series_id = $1
		ORDER BY apply_start_at ASC
	`

	// listCoveredEventIDsQuery returns the covered event IDs for the given
	// phase IDs, keyed by phase ID so the caller can populate each phase.
	listCoveredEventIDsQuery = `
		SELECT sales_phase_id, event_id
		FROM event_sales_phases
		WHERE sales_phase_id = ANY($1::uuid[])
	`

	// existsPhaseQuery confirms a phase row with the given ID exists.
	existsPhaseQuery = `
		SELECT 1 FROM sales_phases WHERE id = $1
	`
)

// Upsert persists the candidate as either an update to an existing overlapping
// phase or a new insert. It is a no-op when the candidate fails the persistence
// guard (zero ApplyStartTime or empty CoveredEventIDs), returning
// ("", UpsertOutcomeSkipped, nil).
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

	// Persistence guard: skip unless apply_start_time is known AND at least
	// one covered event is resolved.
	if candidate.ApplyStartTime.IsZero() {
		r.db.logger.Info(ctx, "sales_phase_repo: dropping candidate with zero apply_start_time",
			slog.String("series_id", candidate.SeriesID),
		)
		return "", entity.UpsertOutcomeSkipped, nil
	}
	if len(candidate.CoveredEventIDs) == 0 {
		r.db.logger.Info(ctx, "sales_phase_repo: dropping candidate with no covered events",
			slog.String("series_id", candidate.SeriesID),
		)
		return "", entity.UpsertOutcomeSkipped, nil
	}

	// Attempt to find an existing phase for this series that overlaps on
	// covered event IDs.
	var existingID string
	err := r.db.Pool.QueryRow(ctx, findOverlappingPhaseQuery,
		candidate.SeriesID,
		candidate.CoveredEventIDs,
		int16(candidate.Channel),
	).Scan(&existingID)

	switch {
	case err == nil:
		// Overlapping phase found — update in-place (last-write-wins).
		if err := r.updateExisting(ctx, existingID, candidate); err != nil {
			return "", entity.UpsertOutcomeSkipped, err
		}
		return existingID, entity.UpsertOutcomeUpdated, nil
	case isNoRows(err):
		// No overlap — insert a new phase row.
		newID := newPhaseID()
		if err := r.insertNewWithID(ctx, newID, candidate); err != nil {
			return "", entity.UpsertOutcomeSkipped, err
		}
		return newID, entity.UpsertOutcomeInserted, nil
	default:
		return "", entity.UpsertOutcomeSkipped, toAppErr(err, "failed to query overlapping sales phase",
			slog.String("series_id", candidate.SeriesID),
		)
	}
}

// updateExisting applies last-write-wins logic to an existing phase row and
// atomically replaces its covered-event links.
func (r *SalesPhaseRepository) updateExisting(
	ctx context.Context,
	phaseID string,
	c *entity.SalesPhaseCandidate,
) error {
	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return toAppErr(err, "failed to begin transaction for sales phase update",
			slog.String("phase_id", phaseID),
		)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Update mutable phase fields.
	_, err = tx.Exec(ctx, updatePhaseQuery,
		phaseID,
		int16(c.Method),
		int16(c.Channel),
		nullableString(c.ProviderName),
		c.Sequence,
		c.ApplyStartTime,
		nullableTime(c.ApplyEndTime),
		nullableTime(c.LotteryResultTime),
		nullableTime(c.PaymentDeadlineTime),
		nullableString(c.URL),
	)
	if err != nil {
		return toAppErr(err, "failed to update sales phase",
			slog.String("phase_id", phaseID),
		)
	}

	// Replace covered events atomically within the same transaction.
	if err := replaceEventLinks(ctx, tx, phaseID, c.CoveredEventIDs); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return toAppErr(err, "failed to commit sales phase update", slog.String("phase_id", phaseID))
	}
	return nil
}

// insertNewWithID inserts a fresh sales_phases row with the provided phaseID
// and its covered-event links.
func (r *SalesPhaseRepository) insertNewWithID(ctx context.Context, phaseID string, c *entity.SalesPhaseCandidate) error {
	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return toAppErr(err, "failed to begin transaction for sales phase insert",
			slog.String("series_id", c.SeriesID),
		)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Use QueryRow to consume the RETURNING discovered_at. The value is discarded
	// here because insertNewWithID doesn't return the full entity; the field is
	// populated on subsequent reads via scanPhaseRows.
	var discardDiscoveredAt time.Time
	if err := tx.QueryRow(ctx, insertPhaseQuery,
		phaseID,
		c.SeriesID,
		c.AnchorEventID,
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
		return toAppErr(err, "failed to insert sales phase",
			slog.String("series_id", c.SeriesID),
		)
	}

	if err := replaceEventLinks(ctx, tx, phaseID, c.CoveredEventIDs); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return toAppErr(err, "failed to commit sales phase insert", slog.String("series_id", c.SeriesID))
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

	phases, err := scanPhaseRows(rows)
	if err != nil {
		return nil, err
	}
	if err := r.populateCoveredEvents(ctx, phases); err != nil {
		return nil, err
	}
	return phases, nil
}

// GetBySeries returns all sales phases for the given series with covered
// event IDs populated.
func (r *SalesPhaseRepository) GetBySeries(ctx context.Context, seriesID string) ([]*entity.SalesPhase, error) {
	if seriesID == "" {
		return nil, apperr.New(codes.InvalidArgument, "series ID must not be empty")
	}

	rows, err := r.db.Pool.Query(ctx, getBySeriesQuery, seriesID)
	if err != nil {
		return nil, toAppErr(err, "failed to get sales phases by series", slog.String("series_id", seriesID))
	}
	defer rows.Close()

	phases, err := scanPhaseRows(rows)
	if err != nil {
		return nil, err
	}
	if err := r.populateCoveredEvents(ctx, phases); err != nil {
		return nil, err
	}
	return phases, nil
}

// ReplaceCoveredEvents atomically replaces the covered-event links for a phase.
func (r *SalesPhaseRepository) ReplaceCoveredEvents(ctx context.Context, phaseID string, eventIDs []string) error {
	if phaseID == "" {
		return apperr.New(codes.InvalidArgument, "phase ID must not be empty")
	}

	// Confirm the phase exists.
	var exists int
	err := r.db.Pool.QueryRow(ctx, existsPhaseQuery, phaseID).Scan(&exists)
	if err != nil {
		if isNoRows(err) {
			return apperr.New(codes.NotFound, "sales phase not found", slog.String("phase_id", phaseID))
		}
		return toAppErr(err, "failed to look up sales phase", slog.String("phase_id", phaseID))
	}

	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return toAppErr(err, "failed to begin transaction for replace covered events",
			slog.String("phase_id", phaseID),
		)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := replaceEventLinks(ctx, tx, phaseID, eventIDs); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return toAppErr(err, "failed to commit replace covered events", slog.String("phase_id", phaseID))
	}
	return nil
}

// ----- helpers -----

// replaceEventLinks deletes all existing event_sales_phases rows for the
// phase and inserts the new set. Must be called within a transaction.
func replaceEventLinks(ctx context.Context, tx pgx.Tx, phaseID string, eventIDs []string) error {
	if _, err := tx.Exec(ctx, deleteEventSalesPhasesQuery, phaseID); err != nil {
		return toAppErr(err, "failed to delete event_sales_phases",
			slog.String("phase_id", phaseID),
		)
	}
	if len(eventIDs) == 0 {
		return nil
	}
	if _, err := tx.Exec(ctx, insertEventSalesPhasesQuery, phaseID, eventIDs); err != nil {
		return toAppErr(err, "failed to insert event_sales_phases",
			slog.String("phase_id", phaseID),
		)
	}
	return nil
}

// populateCoveredEvents fetches covered event IDs for all phases in a single
// query and populates each phase's CoveredEventIDs field.
func (r *SalesPhaseRepository) populateCoveredEvents(ctx context.Context, phases []*entity.SalesPhase) error {
	if len(phases) == 0 {
		return nil
	}
	ids := make([]string, len(phases))
	byID := make(map[string]*entity.SalesPhase, len(phases))
	for i, p := range phases {
		ids[i] = p.ID
		byID[p.ID] = p
	}

	rows, err := r.db.Pool.Query(ctx, listCoveredEventIDsQuery, ids)
	if err != nil {
		return toAppErr(err, "failed to list covered event IDs for sales phases")
	}
	defer rows.Close()

	for rows.Next() {
		var phaseID, eventID string
		if err := rows.Scan(&phaseID, &eventID); err != nil {
			return toAppErr(err, "failed to scan covered event ID row")
		}
		if p, ok := byID[phaseID]; ok {
			p.CoveredEventIDs = append(p.CoveredEventIDs, eventID)
		}
	}
	if err := rows.Err(); err != nil {
		return toAppErr(err, "covered event ID iteration ended with error")
	}
	return nil
}

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
			&p.AnchorEventID,
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
