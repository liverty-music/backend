package rdb

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
)

// TicketApplicationRepository implements [entity.TicketApplicationRepository] for PostgreSQL.
type TicketApplicationRepository struct {
	db *Database
}

// Compile-time interface compliance check.
var _ entity.TicketApplicationRepository = (*TicketApplicationRepository)(nil)

// NewTicketApplicationRepository creates a new TicketApplicationRepository.
func NewTicketApplicationRepository(db *Database) *TicketApplicationRepository {
	return &TicketApplicationRepository{db: db}
}

const (
	insertTicketApplicationQuery = `
		INSERT INTO ticket_applications (
			id, phase_id, applicant_id, requested_ticket_count,
			applicant_full_name, applicant_phone_number, payment_intent_ref,
			state, draw_sequence
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id, phase_id, applicant_id, requested_ticket_count,
		          applicant_full_name, applicant_phone_number, payment_intent_ref,
		          state, draw_sequence
	`

	// getActiveTicketApplicationByPhaseAndApplicantQuery returns the most-recent active
	// application for the given (phase_id, applicant_id). "Active" means state NOT IN
	// (5=Withdrawn), which matches the partial unique index predicate
	// (state IN (1=Applied, 2=Won, 3=Lost)).
	getActiveTicketApplicationByPhaseAndApplicantQuery = `
		SELECT id, phase_id, applicant_id, requested_ticket_count,
		       applicant_full_name, applicant_phone_number, payment_intent_ref,
		       state, draw_sequence
		FROM ticket_applications
		WHERE phase_id = $1
		  AND applicant_id = $2
		  AND state IN (1, 2, 3)
		ORDER BY id DESC
		LIMIT 1
	`

	getTicketApplicationQuery = `
		SELECT id, phase_id, applicant_id, requested_ticket_count,
		       applicant_full_name, applicant_phone_number, payment_intent_ref,
		       state, draw_sequence
		FROM ticket_applications
		WHERE id = $1
	`

	updateTicketApplicationStateQuery = `
		UPDATE ticket_applications
		SET state = $2
		WHERE id = $1
	`

	// getPhaseStatsQuery aggregates ticket_applications for a phase in a single
	// round-trip. Non-withdrawn rows (state IN (1,2,3)) contribute to all counts.
	// draw_completed is true when any row has a non-null draw_sequence, which the
	// draw job stamps on Won (2) and Lost (3) rows.
	getPhaseStatsQuery = `
		SELECT
			COUNT(*)                                                      AS application_count,
			COALESCE(SUM(requested_ticket_count), 0)                      AS requested_ticket_count,
			COUNT(*) FILTER (WHERE state = 2)                             AS winning_application_count,
			COALESCE(SUM(requested_ticket_count) FILTER (WHERE state = 2), 0) AS won_ticket_count,
			COUNT(*) FILTER (WHERE state = 3)                             AS waitlisted_application_count,
			BOOL_OR(draw_sequence IS NOT NULL)                            AS draw_completed
		FROM ticket_applications
		WHERE phase_id = $1
		  AND state IN (1, 2, 3)
	`

	// listAppliedForPhaseQuery returns all applications in the Applied state (1)
	// for the given phase. Withdrawn (5), Won (2), and Lost (3) rows are excluded
	// because only Applied applications are draw candidates.
	listAppliedForPhaseQuery = `
		SELECT id, phase_id, applicant_id, requested_ticket_count,
		       applicant_full_name, applicant_phone_number, payment_intent_ref,
		       state, draw_sequence
		FROM ticket_applications
		WHERE phase_id = $1
		  AND state = 1
	`

	// persistDrawOutcomeWinnersQuery bulk-updates winner rows: sets state=Won (2)
	// and stamps draw_sequence. Uses unnest to expand the parallel arrays of IDs
	// and sequences in a single UPDATE.
	persistDrawOutcomeWinnersQuery = `
		UPDATE ticket_applications AS ta
		SET state         = 2,
		    draw_sequence = u.seq
		FROM (SELECT unnest($1::uuid[]) AS id, unnest($2::bigint[]) AS seq) AS u
		WHERE ta.id = u.id
	`

	// persistDrawOutcomeLosersQuery bulk-updates loser rows: sets state=Lost (3)
	// and stamps draw_sequence.
	persistDrawOutcomeLosersQuery = `
		UPDATE ticket_applications AS ta
		SET state         = 3,
		    draw_sequence = u.seq
		FROM (SELECT unnest($1::uuid[]) AS id, unnest($2::bigint[]) AS seq) AS u
		WHERE ta.id = u.id
	`

	// stampPhaseDrawnAtQuery sets drawn_at on the lottery_sales_phases row to
	// mark the draw as complete. Called as the final step of PersistDrawOutcome
	// within the same transaction so the idempotency guard (drawn_at IS NULL) and
	// the application state updates are committed atomically.
	stampPhaseDrawnAtQuery = `
		UPDATE lottery_sales_phases
		SET drawn_at = $2
		WHERE id = $1
	`
)

// Create persists a new TicketApplication and returns the created row.
func (r *TicketApplicationRepository) Create(ctx context.Context, app *entity.TicketApplication) (*entity.TicketApplication, error) {
	if app == nil {
		return nil, apperr.New(codes.InvalidArgument, "ticket application must not be nil")
	}
	if app.ID == "" {
		return nil, apperr.New(codes.InvalidArgument, "ticket application ID must not be empty")
	}

	// draw_sequence is NULL at creation time; it is set only after the draw runs.
	var drawSequence *int64
	if app.DrawSequence != 0 {
		v := app.DrawSequence
		drawSequence = &v
	}

	row := r.db.Pool.QueryRow(ctx, insertTicketApplicationQuery,
		string(app.ID),
		string(app.PhaseID),
		string(app.ApplicantID),
		app.RequestedTicketCount,
		app.Identity.FullName,
		app.Identity.PhoneNumber,
		app.Authorization.PaymentIntentRef,
		int16(app.State),
		drawSequence,
	)

	created, err := scanTicketApplication(row.Scan)
	if err != nil {
		return nil, toAppErr(err, "failed to insert ticket application",
			slog.String("application_id", string(app.ID)),
			slog.String("phase_id", string(app.PhaseID)),
		)
	}

	r.db.logger.Info(ctx, "ticket application created",
		slog.String("entityType", "ticket_application"),
		slog.String("application_id", string(created.ID)),
		slog.String("phase_id", string(created.PhaseID)),
	)
	return created, nil
}

// GetByPhaseAndApplicant returns the most-recent active application for the
// given (phaseID, applicantID) pair. "Active" means state is NOT Withdrawn
// (i.e. state IN (1=Applied, 2=Won, 3=Lost)). Returns NotFound when no active
// application exists.
func (r *TicketApplicationRepository) GetByPhaseAndApplicant(
	ctx context.Context,
	phaseID entity.LotteryPhaseID,
	applicantID entity.UserID,
) (*entity.TicketApplication, error) {
	if phaseID == "" {
		return nil, apperr.New(codes.InvalidArgument, "phase ID must not be empty")
	}
	if applicantID == "" {
		return nil, apperr.New(codes.InvalidArgument, "applicant ID must not be empty")
	}

	row := r.db.Pool.QueryRow(ctx, getActiveTicketApplicationByPhaseAndApplicantQuery,
		string(phaseID),
		string(applicantID),
	)
	app, err := scanTicketApplication(row.Scan)
	if err != nil {
		return nil, toAppErr(err, "failed to get active ticket application by phase and applicant",
			slog.String("phase_id", string(phaseID)),
			slog.String("applicant_id", string(applicantID)),
		)
	}
	return app, nil
}

// Get returns the ticket application by its ID.
func (r *TicketApplicationRepository) Get(ctx context.Context, id entity.TicketApplicationID) (*entity.TicketApplication, error) {
	if id == "" {
		return nil, apperr.New(codes.InvalidArgument, "ticket application ID must not be empty")
	}

	row := r.db.Pool.QueryRow(ctx, getTicketApplicationQuery, string(id))
	app, err := scanTicketApplication(row.Scan)
	if err != nil {
		return nil, toAppErr(err, "failed to get ticket application",
			slog.String("application_id", string(id)),
		)
	}
	return app, nil
}

// UpdateState persists a state change on the ticket application. Returns
// NotFound when no application with the given ID exists.
func (r *TicketApplicationRepository) UpdateState(
	ctx context.Context,
	id entity.TicketApplicationID,
	state entity.TicketApplicationState,
) error {
	if id == "" {
		return apperr.New(codes.InvalidArgument, "ticket application ID must not be empty")
	}

	tag, err := r.db.Pool.Exec(ctx, updateTicketApplicationStateQuery,
		string(id),
		int16(state),
	)
	if err != nil {
		return toAppErr(err, "failed to update ticket application state",
			slog.String("application_id", string(id)),
			slog.Int("state", int(state)),
		)
	}
	if tag.RowsAffected() == 0 {
		return apperr.New(codes.NotFound, "ticket application not found")
	}

	r.db.logger.Info(ctx, "ticket application state updated",
		slog.String("entityType", "ticket_application"),
		slog.String("application_id", string(id)),
		slog.Int("state", int(state)),
	)
	return nil
}

// GetPhaseStats returns aggregate tallies for all non-withdrawn applications
// belonging to the given phase. When no applications exist the returned struct
// has all numeric fields set to zero and DrawCompleted false.
func (r *TicketApplicationRepository) GetPhaseStats(ctx context.Context, phaseID entity.LotteryPhaseID) (entity.LotteryPhaseStatus, error) {
	if phaseID == "" {
		return entity.LotteryPhaseStatus{}, apperr.New(codes.InvalidArgument, "phase ID must not be empty")
	}

	row := r.db.Pool.QueryRow(ctx, getPhaseStatsQuery, string(phaseID))

	var (
		applicationCount           int
		requestedTicketCount       int
		winningApplicationCount    int
		wonTicketCount             int
		waitlistedApplicationCount int
		drawCompleted              sql.NullBool
	)

	if err := row.Scan(
		&applicationCount,
		&requestedTicketCount,
		&winningApplicationCount,
		&wonTicketCount,
		&waitlistedApplicationCount,
		&drawCompleted,
	); err != nil {
		return entity.LotteryPhaseStatus{}, toAppErr(err, "failed to get phase stats",
			slog.String("phase_id", string(phaseID)),
		)
	}

	return entity.LotteryPhaseStatus{
		DrawCompleted:              drawCompleted.Valid && drawCompleted.Bool,
		ApplicationCount:           applicationCount,
		RequestedTicketCount:       requestedTicketCount,
		WinningApplicationCount:    winningApplicationCount,
		WonTicketCount:             wonTicketCount,
		WaitlistedApplicationCount: waitlistedApplicationCount,
	}, nil
}

// ListAppliedForPhase returns all applications in the Applied state (1) for the
// given phase. Withdrawn, Won, and Lost applications are excluded.
func (r *TicketApplicationRepository) ListAppliedForPhase(ctx context.Context, phaseID entity.LotteryPhaseID) ([]*entity.TicketApplication, error) {
	if phaseID == "" {
		return nil, apperr.New(codes.InvalidArgument, "phase ID must not be empty")
	}

	rows, err := r.db.Pool.Query(ctx, listAppliedForPhaseQuery, string(phaseID))
	if err != nil {
		return nil, toAppErr(err, "failed to list applied applications for phase",
			slog.String("phase_id", string(phaseID)),
		)
	}
	defer rows.Close()

	var apps []*entity.TicketApplication
	for rows.Next() {
		app, err := scanTicketApplication(rows.Scan)
		if err != nil {
			return nil, toAppErr(err, "failed to scan ticket application",
				slog.String("phase_id", string(phaseID)),
			)
		}
		apps = append(apps, app)
	}
	if err := rows.Err(); err != nil {
		return nil, toAppErr(err, "applied-for-phase iteration error",
			slog.String("phase_id", string(phaseID)),
		)
	}
	return apps, nil
}

// PersistDrawOutcome atomically sets winner/loser states and draw_sequences,
// then stamps drawn_at on the phase — all within a single transaction.
func (r *TicketApplicationRepository) PersistDrawOutcome(
	ctx context.Context,
	phaseID entity.LotteryPhaseID,
	winners []entity.DrawWinnerRow,
	losers []entity.DrawLoserRow,
) error {
	if phaseID == "" {
		return apperr.New(codes.InvalidArgument, "phase ID must not be empty")
	}

	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return toAppErr(err, "failed to begin draw outcome transaction",
			slog.String("phase_id", string(phaseID)),
		)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Bulk-update winners when the slice is non-empty.
	if len(winners) > 0 {
		winnerIDs := make([]string, len(winners))
		winnerSeqs := make([]int64, len(winners))
		for i, w := range winners {
			winnerIDs[i] = string(w.ApplicationID)
			winnerSeqs[i] = int64(w.DrawSequence)
		}
		if _, err := tx.Exec(ctx, persistDrawOutcomeWinnersQuery, winnerIDs, winnerSeqs); err != nil {
			return toAppErr(err, "failed to update winner states",
				slog.String("phase_id", string(phaseID)),
				slog.Int("winner_count", len(winners)),
			)
		}
	}

	// Bulk-update losers when the slice is non-empty.
	if len(losers) > 0 {
		loserIDs := make([]string, len(losers))
		loserSeqs := make([]int64, len(losers))
		for i, l := range losers {
			loserIDs[i] = string(l.ApplicationID)
			loserSeqs[i] = int64(l.DrawSequence)
		}
		if _, err := tx.Exec(ctx, persistDrawOutcomeLosersQuery, loserIDs, loserSeqs); err != nil {
			return toAppErr(err, "failed to update loser states",
				slog.String("phase_id", string(phaseID)),
				slog.Int("loser_count", len(losers)),
			)
		}
	}

	// Stamp drawn_at on the phase to mark the draw complete and close the
	// idempotency window. This is the last write in the transaction so a crash
	// between winner/loser updates and this stamp causes a full retry on the
	// next sweep tick (the application state updates are rolled back with the tx).
	drawnAt := time.Now().UTC()
	if _, err := tx.Exec(ctx, stampPhaseDrawnAtQuery, string(phaseID), drawnAt); err != nil {
		return toAppErr(err, "failed to stamp drawn_at on lottery phase",
			slog.String("phase_id", string(phaseID)),
		)
	}

	if err := tx.Commit(ctx); err != nil {
		return toAppErr(err, "failed to commit draw outcome transaction",
			slog.String("phase_id", string(phaseID)),
		)
	}

	r.db.logger.Info(ctx, "draw outcome persisted",
		slog.String("entityType", "lottery_sales_phase"),
		slog.String("phase_id", string(phaseID)),
		slog.Int("winner_count", len(winners)),
		slog.Int("loser_count", len(losers)),
	)
	return nil
}

// scanTicketApplication reads a single ticket_applications row into a TicketApplication entity.
// draw_sequence is nullable (set only after the draw runs) and is scanned via sql.NullInt64.
func scanTicketApplication(scan func(dest ...any) error) (*entity.TicketApplication, error) {
	var app entity.TicketApplication
	var rawID, rawPhaseID, rawApplicantID string
	var state int16
	var drawSeq sql.NullInt64

	if err := scan(
		&rawID,
		&rawPhaseID,
		&rawApplicantID,
		&app.RequestedTicketCount,
		&app.Identity.FullName,
		&app.Identity.PhoneNumber,
		&app.Authorization.PaymentIntentRef,
		&state,
		&drawSeq,
	); err != nil {
		return nil, err
	}

	app.ID = entity.TicketApplicationID(rawID)
	app.PhaseID = entity.LotteryPhaseID(rawPhaseID)
	app.ApplicantID = entity.UserID(rawApplicantID)
	app.State = entity.TicketApplicationState(state)
	if drawSeq.Valid {
		app.DrawSequence = drawSeq.Int64
	}
	return &app, nil
}
