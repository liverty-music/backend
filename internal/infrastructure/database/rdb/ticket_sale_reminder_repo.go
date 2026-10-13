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

// TicketSaleReminderRepository implements [entity.TicketSaleReminderRepository]
// for PostgreSQL.
type TicketSaleReminderRepository struct {
	db *Database
}

// Compile-time interface compliance check.
var _ entity.TicketSaleReminderRepository = (*TicketSaleReminderRepository)(nil)

// NewTicketSaleReminderRepository creates a new TicketSaleReminderRepository.
func NewTicketSaleReminderRepository(db *Database) *TicketSaleReminderRepository {
	return &TicketSaleReminderRepository{db: db}
}

const (
	// recordSentQuery inserts a sent-log row. The ON CONFLICT DO NOTHING
	// clause makes the operation idempotent: a duplicate (user_id, ticket_sale_id,
	// stage) triple is silently swallowed rather than returned as an error.
	recordSentQuery = `
		INSERT INTO ticket_sale_reminders (id, user_id, ticket_sale_id, stage)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT ON CONSTRAINT uq_ticket_sale_reminders DO NOTHING
	`

	// alreadySentQuery checks for the existence of a sent-log entry.
	alreadySentQuery = `
		SELECT 1 FROM ticket_sale_reminders
		WHERE user_id = $1 AND ticket_sale_id = $2 AND stage = $3
		LIMIT 1
	`

	// listSentStagesQuery returns all (user_id, stage) pairs already sent for
	// the given ticket sale among the supplied user IDs. Used by the reminder scan
	// to batch the already-sent check instead of one query per (user, stage).
	listSentStagesQuery = `
		SELECT user_id, stage
		FROM ticket_sale_reminders
		WHERE ticket_sale_id = $1
		  AND user_id = ANY($2::uuid[])
	`
)

// RecordSent records that the given stage reminder was dispatched to the user
// for the given ticket sale. The operation is idempotent.
func (r *TicketSaleReminderRepository) RecordSent(
	ctx context.Context,
	userID string,
	saleID entity.TicketSaleID,
	stage entity.ReminderStage,
) error {
	if userID == "" {
		return apperr.New(codes.InvalidArgument, "userID must not be empty")
	}
	if saleID == "" {
		return apperr.New(codes.InvalidArgument, "ticket sale ID must not be empty")
	}
	id := entity.NewID()
	if _, err := r.db.Pool.Exec(ctx, recordSentQuery, id, userID, string(saleID), int16(stage)); err != nil {
		return toAppErr(err, "failed to record reminder sent",
			slog.String("user_id", userID),
			slog.String("ticket_sale_id", string(saleID)),
			slog.Int("stage", int(stage)),
		)
	}
	return nil
}

// ListSentStages returns a map of userID → set-of-stages already sent for
// the given ticket sale, filtered to the supplied userIDs. A single query replaces
// the per-(user,stage) AlreadySent loop in the reminder scan.
func (r *TicketSaleReminderRepository) ListSentStages(
	ctx context.Context,
	saleID entity.TicketSaleID,
	userIDs []string,
) (map[string]map[entity.ReminderStage]bool, error) {
	if saleID == "" {
		return nil, apperr.New(codes.InvalidArgument, "ticket sale ID must not be empty")
	}
	result := make(map[string]map[entity.ReminderStage]bool)
	if len(userIDs) == 0 {
		return result, nil
	}
	rows, err := r.db.Pool.Query(ctx, listSentStagesQuery, string(saleID), userIDs)
	if err != nil {
		return nil, toAppErr(err, "failed to list sent stages",
			slog.String("ticket_sale_id", string(saleID)),
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
// dispatched to the user for the given ticket sale.
func (r *TicketSaleReminderRepository) AlreadySent(
	ctx context.Context,
	userID string,
	saleID entity.TicketSaleID,
	stage entity.ReminderStage,
) (bool, error) {
	if userID == "" {
		return false, apperr.New(codes.InvalidArgument, "userID must not be empty")
	}
	if saleID == "" {
		return false, apperr.New(codes.InvalidArgument, "ticket sale ID must not be empty")
	}
	var exists int
	err := r.db.Pool.QueryRow(ctx, alreadySentQuery, userID, string(saleID), int16(stage)).Scan(&exists)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, toAppErr(err, "failed to check reminder sent status",
			slog.String("user_id", userID),
			slog.String("ticket_sale_id", string(saleID)),
			slog.Int("stage", int(stage)),
		)
	}
	return true, nil
}
