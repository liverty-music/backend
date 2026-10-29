package rdb

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
)

// TicketStockRepository implements [entity.TicketStockRepository] for
// PostgreSQL. A stock's sold and held counts are aggregated from its Orders
// on every read (design D5); every write that changes them first takes the
// stock's row lock with [lockTicketStock].
type TicketStockRepository struct {
	db *Database
}

// Compile-time interface compliance check.
var _ entity.TicketStockRepository = (*TicketStockRepository)(nil)

// NewTicketStockRepository creates a new TicketStockRepository.
func NewTicketStockRepository(db *Database) *TicketStockRepository {
	return &TicketStockRepository{db: db}
}

// ticketStockSelect reads stocks (aliased s) with their seat type and, at the
// time given as $1, their sold and held counts. Sold counts the Orders bought
// through a LotteryEntry or a Checkout that are Paid or Refunded, or Pending
// with a Committed Checkout or a Won entry; held counts the Pending Orders
// whose Checkout is Held with its hold not lapsed at $1. Void Orders count in
// neither. A WHERE on s and the GROUP BY follow.
const ticketStockSelect = `
	SELECT s.id, s.event_id, s.seat_type_id, s.quantity,
		st.series_id, st.name, st.price,
		COALESCE(SUM(o.ticket_count) FILTER (WHERE
			(o.status IN (1, 2) AND (c.id IS NOT NULL OR le.id IS NOT NULL))
			OR (o.status = 4 AND (c.status = 2 OR le.state = 2))), 0)::int,
		COALESCE(SUM(o.ticket_count) FILTER (WHERE
			o.status = 4 AND c.status = 1 AND c.hold_expire_at > $1), 0)::int
	FROM ticket_stocks s
	JOIN seat_types st ON st.id = s.seat_type_id
	LEFT JOIN orders o ON o.ticket_stock_id = s.id
	LEFT JOIN checkouts c ON c.order_id = o.id
	LEFT JOIN lottery_entries le ON le.order_id = o.id
`

const ticketStockGroupBy = ` GROUP BY s.id, st.id`

const (
	ticketStockGetQuery         = ticketStockSelect + ` WHERE s.id = $2` + ticketStockGroupBy
	ticketStockListByEventQuery = ticketStockSelect + ` WHERE s.event_id = $2` + ticketStockGroupBy + ` ORDER BY st.price DESC, st.name, s.id`
	// ticketStockInsertQuery stores the stock only for an event of the seat
	// type's Series.
	ticketStockInsertQuery = `
		INSERT INTO ticket_stocks (id, event_id, seat_type_id, quantity)
		SELECT $1, e.id, st.id, $4 FROM events e JOIN seat_types st ON st.series_id = e.series_id
		WHERE e.id = $2 AND st.id = $3
	`
	ticketStockRefsExistQuery = `
		SELECT EXISTS (SELECT 1 FROM events WHERE id = $1), EXISTS (SELECT 1 FROM seat_types WHERE id = $2)
	`
	// ticketStockLockQuery takes the stock's row lock, which every change of
	// its sold or held count takes first (design D5).
	ticketStockLockQuery = `SELECT 1 FROM ticket_stocks WHERE id = $1 FOR UPDATE`
	// ticketStockDeleteQuery runs TicketStock.Delete in the SECURITY DEFINER
	// function of the migration remodel_ticket_sales (design D8).
	ticketStockDeleteQuery = `SELECT delete_ticket_stock($1)`
)

// Create stores the stock of an event of the seat type's Series.
func (r *TicketStockRepository) Create(ctx context.Context, stock *entity.TicketStock) (*entity.TicketStock, error) {
	attrs := []slog.Attr{slog.String("event_id", stock.EventID), slog.String("seat_type_id", string(stock.SeatTypeID))}
	if err := stock.Validate(); err != nil {
		return nil, apperr.Wrap(err, codes.InvalidArgument, err.Error(), attrs...)
	}
	tag, err := r.db.Pool.Exec(ctx, ticketStockInsertQuery, string(stock.ID), stock.EventID, string(stock.SeatTypeID), stock.Quantity)
	if err != nil {
		return nil, toAppErr(err, "failed to create ticket stock", attrs...)
	}
	if tag.RowsAffected() == 0 {
		var eventExists, seatTypeExists bool
		if err := r.db.Pool.QueryRow(ctx, ticketStockRefsExistQuery, stock.EventID, string(stock.SeatTypeID)).Scan(&eventExists, &seatTypeExists); err != nil {
			return nil, toAppErr(err, "failed to read the stock's event and seat type", attrs...)
		}
		if !eventExists || !seatTypeExists {
			return nil, apperr.New(codes.FailedPrecondition, "the event or the seat type does not exist", attrs...)
		}
		return nil, apperr.New(codes.InvalidArgument, "the event is not of the seat type's series", attrs...)
	}
	return r.Get(ctx, stock.ID, time.Now())
}

// Get returns the stock with its seat type and its counts at at.
func (r *TicketStockRepository) Get(ctx context.Context, id entity.TicketStockID, at time.Time) (*entity.TicketStock, error) {
	s, err := getTicketStock(ctx, r.db.Pool, id, at)
	if err != nil {
		return nil, toAppErr(err, "failed to get ticket stock", slog.String("ticket_stock_id", string(id)))
	}
	return s, nil
}

// Delete removes the stock with its Void Orders and their Checkouts and
// LotteryEntries through delete_ticket_stock, which refuses with
// restrict_violation (FailedPrecondition) while a live Order names the stock.
func (r *TicketStockRepository) Delete(ctx context.Context, id entity.TicketStockID) error {
	attrs := slog.String("ticket_stock_id", string(id))
	var deleted bool
	if err := r.db.Pool.QueryRow(ctx, ticketStockDeleteQuery, string(id)).Scan(&deleted); err != nil {
		return toAppErr(err, "failed to delete ticket stock", attrs)
	}
	if !deleted {
		return apperr.New(codes.NotFound, "no ticket stock has the id", attrs)
	}
	return nil
}

// ListByEvent returns the event's stocks with their seat types and counts at
// at, highest price first.
func (r *TicketStockRepository) ListByEvent(ctx context.Context, eventID string, at time.Time) ([]*entity.TicketStock, error) {
	stocks, err := listTicketStocks(ctx, r.db.Pool, ticketStockListByEventQuery, at, eventID)
	if err != nil {
		return nil, toAppErr(err, "failed to list ticket stocks", slog.String("event_id", eventID))
	}
	return stocks, nil
}

// lockTicketStock takes the stock's row lock in tx and returns the stock with
// its counts at at, read after the lock is granted, so they include every
// change committed by the transactions that held the lock before.
func lockTicketStock(ctx context.Context, tx pgx.Tx, id entity.TicketStockID, at time.Time) (*entity.TicketStock, error) {
	var one int
	if err := tx.QueryRow(ctx, ticketStockLockQuery, string(id)).Scan(&one); err != nil {
		return nil, err
	}
	return getTicketStock(ctx, tx, id, at)
}

// getTicketStock reads one stock with its counts at at.
func getTicketStock(ctx context.Context, q rowQuerier, id entity.TicketStockID, at time.Time) (*entity.TicketStock, error) {
	stocks, err := listTicketStocks(ctx, q, ticketStockGetQuery, at, string(id))
	if err != nil {
		return nil, err
	}
	if len(stocks) == 0 {
		return nil, pgx.ErrNoRows
	}
	return stocks[0], nil
}

// listTicketStocks runs a ticketStockSelect query with at as $1.
func listTicketStocks(ctx context.Context, q rowQuerier, query string, at time.Time, args ...any) ([]*entity.TicketStock, error) {
	rows, err := q.Query(ctx, query, append([]any{at}, args...)...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (*entity.TicketStock, error) {
		var (
			s              entity.TicketStock
			st             entity.SeatType
			id, seatTypeID string
		)
		if err := row.Scan(&id, &s.EventID, &seatTypeID, &s.Quantity, &st.SeriesID, &st.Name, &st.Price, &s.SoldCount, &s.HeldCount); err != nil {
			return nil, err
		}
		s.ID = entity.TicketStockID(id)
		s.SeatTypeID = entity.SeatTypeID(seatTypeID)
		st.ID = s.SeatTypeID
		s.SeatType = &st
		return &s, nil
	})
}
