package rdb

import (
	"context"
	"errors"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
)

// SalesPhaseReminderRepository implements [entity.SalesPhaseReminderRepository]
// for PostgreSQL.
type SalesPhaseReminderRepository struct {
	db *Database
}

// Compile-time interface compliance check.
var _ entity.SalesPhaseReminderRepository = (*SalesPhaseReminderRepository)(nil)

// NewSalesPhaseReminderRepository creates a new SalesPhaseReminderRepository.
func NewSalesPhaseReminderRepository(db *Database) *SalesPhaseReminderRepository {
	return &SalesPhaseReminderRepository{db: db}
}

const (
	// recordSentQuery inserts a sent-log row. The ON CONFLICT DO NOTHING
	// clause makes the operation idempotent: a duplicate (user_id, phase_id,
	// stage) triple is silently swallowed rather than returned as an error.
	recordSentQuery = `
		INSERT INTO sales_phase_reminders (id, user_id, sales_phase_id, stage)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT ON CONSTRAINT uq_sales_phase_reminders DO NOTHING
	`

	// alreadySentQuery checks for the existence of a sent-log entry.
	alreadySentQuery = `
		SELECT 1 FROM sales_phase_reminders
		WHERE user_id = $1 AND sales_phase_id = $2 AND stage = $3
		LIMIT 1
	`

	// listSentStagesQuery returns all (user_id, stage) pairs already sent for
	// the given phase among the supplied user IDs. Used by the reminder scan
	// to batch the already-sent check instead of one query per (user, stage).
	listSentStagesQuery = `
		SELECT user_id, stage
		FROM sales_phase_reminders
		WHERE sales_phase_id = $1
		  AND user_id = ANY($2::uuid[])
	`
)

// RecordSent records that the given stage reminder was dispatched to the user
// for the given phase. The operation is idempotent.
func (r *SalesPhaseReminderRepository) RecordSent(
	ctx context.Context,
	userID, phaseID string,
	stage entity.ReminderStage,
) error {
	if userID == "" {
		return apperr.New(codes.InvalidArgument, "userID must not be empty")
	}
	if phaseID == "" {
		return apperr.New(codes.InvalidArgument, "phaseID must not be empty")
	}
	id := newPhaseID() // reuse the same UUIDv7 generator
	if _, err := r.db.Pool.Exec(ctx, recordSentQuery, id, userID, phaseID, int16(stage)); err != nil {
		return toAppErr(err, "failed to record reminder sent",
			slog.String("user_id", userID),
			slog.String("phase_id", phaseID),
			slog.Int("stage", int(stage)),
		)
	}
	return nil
}

// ListSentStages returns a map of userID → set-of-stages already sent for
// the given phase, filtered to the supplied userIDs. A single query replaces
// the per-(user,stage) AlreadySent loop in the reminder scan.
func (r *SalesPhaseReminderRepository) ListSentStages(
	ctx context.Context,
	phaseID string,
	userIDs []string,
) (map[string]map[entity.ReminderStage]bool, error) {
	if phaseID == "" {
		return nil, apperr.New(codes.InvalidArgument, "phaseID must not be empty")
	}
	result := make(map[string]map[entity.ReminderStage]bool)
	if len(userIDs) == 0 {
		return result, nil
	}
	rows, err := r.db.Pool.Query(ctx, listSentStagesQuery, phaseID, userIDs)
	if err != nil {
		return nil, toAppErr(err, "failed to list sent stages",
			slog.String("phase_id", phaseID),
		)
	}
	defer rows.Close()
	for rows.Next() {
		var userID string
		var stage int16
		if err := rows.Scan(&userID, &stage); err != nil {
			return nil, toAppErr(err, "failed to scan sent stage row")
		}
		if result[userID] == nil {
			result[userID] = make(map[entity.ReminderStage]bool)
		}
		result[userID][entity.ReminderStage(stage)] = true
	}
	if err := rows.Err(); err != nil {
		return nil, toAppErr(err, "list sent stages iteration error")
	}
	return result, nil
}

// AlreadySent reports whether the given stage reminder has already been
// dispatched to the user for the given phase.
func (r *SalesPhaseReminderRepository) AlreadySent(
	ctx context.Context,
	userID, phaseID string,
	stage entity.ReminderStage,
) (bool, error) {
	if userID == "" {
		return false, apperr.New(codes.InvalidArgument, "userID must not be empty")
	}
	if phaseID == "" {
		return false, apperr.New(codes.InvalidArgument, "phaseID must not be empty")
	}
	var exists int
	err := r.db.Pool.QueryRow(ctx, alreadySentQuery, userID, phaseID, int16(stage)).Scan(&exists)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, toAppErr(err, "failed to check reminder sent status",
			slog.String("user_id", userID),
			slog.String("phase_id", phaseID),
			slog.Int("stage", int(stage)),
		)
	}
	return true, nil
}
