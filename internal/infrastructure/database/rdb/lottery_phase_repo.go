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

// LotteryPhaseRepository implements [entity.LotteryPhaseRepository] for PostgreSQL.
type LotteryPhaseRepository struct {
	db *Database
}

// Compile-time interface compliance check.
var _ entity.LotteryPhaseRepository = (*LotteryPhaseRepository)(nil)

// NewLotteryPhaseRepository creates a new LotteryPhaseRepository.
func NewLotteryPhaseRepository(db *Database) *LotteryPhaseRepository {
	return &LotteryPhaseRepository{db: db}
}

const (
	insertLotteryPhaseQuery = `
		INSERT INTO lottery_sales_phases (
			id, event_id, open_at, close_at,
			ticket_capacity, max_tickets_per_application, ticket_price
		) VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, event_id, open_at, close_at,
		          ticket_capacity, max_tickets_per_application, ticket_price, drawn_at
	`

	getLotteryPhaseQuery = `
		SELECT id, event_id, open_at, close_at,
		       ticket_capacity, max_tickets_per_application, ticket_price, drawn_at
		FROM lottery_sales_phases
		WHERE id = $1
	`

	// listPhasesDueForDrawQuery selects phases whose window has closed (close_at <=
	// $1) and whose draw has not yet run (drawn_at IS NULL). The draw sweeper calls
	// this once per tick and runs the draw for each returned phase.
	listPhasesDueForDrawQuery = `
		SELECT id, event_id, open_at, close_at,
		       ticket_capacity, max_tickets_per_application, ticket_price, drawn_at
		FROM lottery_sales_phases
		WHERE close_at <= $1
		  AND drawn_at IS NULL
	`
)

// Create persists a new LotterySalesPhase and returns the created row.
func (r *LotteryPhaseRepository) Create(ctx context.Context, phase *entity.LotterySalesPhase) (*entity.LotterySalesPhase, error) {
	if phase == nil {
		return nil, apperr.New(codes.InvalidArgument, "phase must not be nil")
	}
	if phase.ID == "" {
		return nil, apperr.New(codes.InvalidArgument, "phase ID must not be empty")
	}

	row := r.db.Pool.QueryRow(ctx, insertLotteryPhaseQuery,
		string(phase.ID),
		phase.EventID,
		phase.OpenTime,
		phase.CloseTime,
		phase.TicketCapacity,
		phase.MaxTicketsPerApplication,
		phase.TicketPrice,
	)

	created, err := scanLotteryPhase(row.Scan)
	if err != nil {
		return nil, toAppErr(err, "failed to insert lottery sales phase",
			slog.String("phase_id", string(phase.ID)),
		)
	}

	r.db.logger.Info(ctx, "lottery sales phase created",
		slog.String("entityType", "lottery_sales_phase"),
		slog.String("phase_id", string(created.ID)),
	)
	return created, nil
}

// Get returns the lottery sales phase by ID.
func (r *LotteryPhaseRepository) Get(ctx context.Context, id entity.LotteryPhaseID) (*entity.LotterySalesPhase, error) {
	if id == "" {
		return nil, apperr.New(codes.InvalidArgument, "lottery phase ID must not be empty")
	}

	row := r.db.Pool.QueryRow(ctx, getLotteryPhaseQuery, string(id))
	phase, err := scanLotteryPhase(row.Scan)
	if err != nil {
		return nil, toAppErr(err, "failed to get lottery sales phase",
			slog.String("phase_id", string(id)),
		)
	}
	return phase, nil
}

// ListPhasesDueForDraw returns all phases whose window has closed and whose
// draw has not yet run (drawn_at IS NULL).
func (r *LotteryPhaseRepository) ListPhasesDueForDraw(ctx context.Context, now time.Time) ([]*entity.LotterySalesPhase, error) {
	rows, err := r.db.Pool.Query(ctx, listPhasesDueForDrawQuery, now)
	if err != nil {
		return nil, toAppErr(err, "failed to list phases due for draw")
	}
	defer rows.Close()

	var phases []*entity.LotterySalesPhase
	for rows.Next() {
		phase, err := scanLotteryPhase(rows.Scan)
		if err != nil {
			return nil, toAppErr(err, "failed to scan lottery phase due for draw")
		}
		phases = append(phases, phase)
	}
	if err := rows.Err(); err != nil {
		return nil, toAppErr(err, "lottery phase due-for-draw iteration error")
	}
	return phases, nil
}

// scanLotteryPhase reads a single lottery_sales_phases row into a LotterySalesPhase entity.
// It accepts a Scan func so it can be used with both pgx.Row and pgx.Rows.
// The drawn_at column is nullable (NULL until the draw runs) and is scanned via sql.NullTime.
func scanLotteryPhase(scan func(dest ...any) error) (*entity.LotterySalesPhase, error) {
	var p entity.LotterySalesPhase
	var rawID string
	var drawnAt sql.NullTime

	if err := scan(
		&rawID,
		&p.EventID,
		&p.OpenTime,
		&p.CloseTime,
		&p.TicketCapacity,
		&p.MaxTicketsPerApplication,
		&p.TicketPrice,
		&drawnAt,
	); err != nil {
		return nil, err
	}
	p.ID = entity.LotteryPhaseID(rawID)
	if drawnAt.Valid {
		p.DrawnTime = drawnAt.Time
	}
	return &p, nil
}
