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

// TicketSaleRepository implements entity.TicketSaleRepository for PostgreSQL.
type TicketSaleRepository struct {
	db *Database
}

// Compile-time interface compliance check.
var _ entity.TicketSaleRepository = (*TicketSaleRepository)(nil)

// ticketSaleColumns selects a ticket_sales row (aliased ts) with its held
// count at $2.
const ticketSaleColumns = `
	ts.id, ts.event_id, ts.method, ts.sale_start_at, ts.sale_end_at, ts.price,
	ts.quantity, ts.per_account_limit, ts.sold_count,
	(SELECT COALESCE(SUM(r.ticket_count), 0)::int FROM reservations r
		WHERE r.ticket_sale_id = ts.id AND r.status = 1 AND r.hold_expire_at > $2)
`

const (
	ticketSaleInsertQuery = `
		INSERT INTO ticket_sales (
			id, event_id, method, sale_start_at, sale_end_at, price,
			quantity, per_account_limit, sold_count
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 0)
	`
	ticketSaleGetQuery        = `SELECT ` + ticketSaleColumns + ` FROM ticket_sales ts WHERE ts.id = $1`
	ticketSaleGetByEventQuery = `SELECT ` + ticketSaleColumns + ` FROM ticket_sales ts WHERE ts.event_id = $1`
	// ticketSaleLockQuery takes the sale's row lock, which Starts and Commits
	// of the sale's reservations also take. The counts are read by a separate,
	// later statement: under READ COMMITTED a statement that waited for the
	// lock still evaluates its subqueries with the snapshot taken before it
	// waited, so reading the counts in the locking statement would miss the
	// holds committed by the transaction it waited for.
	ticketSaleLockQuery   = `SELECT 1 FROM ticket_sales WHERE id = $1 FOR UPDATE`
	ticketSaleUpdateQuery = `
		UPDATE ticket_sales
		SET sale_start_at = $2, sale_end_at = $3, price = $4, quantity = $5, per_account_limit = $6
		WHERE id = $1
	`
)

// NewTicketSaleRepository creates a new ticket sale repository instance.
func NewTicketSaleRepository(db *Database) *TicketSaleRepository {
	return &TicketSaleRepository{db: db}
}

// Create stores a new ticket sale with a sold count of 0.
func (r *TicketSaleRepository) Create(ctx context.Context, sale *entity.TicketSale) (*entity.TicketSale, error) {
	if err := sale.Validate(); err != nil {
		return nil, apperr.Wrap(err, codes.InvalidArgument, "invalid ticket sale")
	}
	if _, err := r.db.Pool.Exec(ctx, ticketSaleInsertQuery,
		string(sale.ID), sale.EventID, int16(sale.Method), sale.SaleStartTime, sale.SaleEndTime,
		sale.Price, sale.Quantity, sale.PerAccountLimit,
	); err != nil {
		return nil, toAppErr(err, "failed to insert ticket sale", slog.String("event_id", sale.EventID))
	}
	created := *sale
	created.SoldCount, created.HeldCount = 0, 0
	return &created, nil
}

// Get returns the ticket sale with its held count at the given time.
func (r *TicketSaleRepository) Get(ctx context.Context, id entity.TicketSaleID, at time.Time) (*entity.TicketSale, error) {
	sale, err := scanTicketSale(r.db.Pool.QueryRow(ctx, ticketSaleGetQuery, string(id), at))
	if err != nil {
		return nil, toAppErr(err, "failed to get ticket sale", slog.String("ticket_sale_id", string(id)))
	}
	return sale, nil
}

// GetByEvent returns the event's ticket sale with its held count at the given
// time.
func (r *TicketSaleRepository) GetByEvent(ctx context.Context, eventID string, at time.Time) (*entity.TicketSale, error) {
	sale, err := scanTicketSale(r.db.Pool.QueryRow(ctx, ticketSaleGetByEventQuery, eventID, at))
	if err != nil {
		return nil, toAppErr(err, "failed to get ticket sale by event", slog.String("event_id", eventID))
	}
	return sale, nil
}

// Update stores the sale's window, price, quantity and per-account limit,
// checking them against the stored sale under its row lock.
func (r *TicketSaleRepository) Update(ctx context.Context, sale *entity.TicketSale, at time.Time) (*entity.TicketSale, error) {
	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return nil, toAppErr(err, "failed to begin ticket sale update")
	}
	// Rollback is a no-op after a successful Commit.
	defer func() { _ = tx.Rollback(ctx) }()

	current, err := lockTicketSale(ctx, tx, sale.ID, at)
	if err != nil {
		return nil, toAppErr(err, "failed to lock ticket sale", slog.String("ticket_sale_id", string(sale.ID)))
	}

	updated := *current
	updated.SaleStartTime = sale.SaleStartTime
	updated.SaleEndTime = sale.SaleEndTime
	updated.Price = sale.Price
	updated.Quantity = sale.Quantity
	updated.PerAccountLimit = sale.PerAccountLimit
	if err := updated.Validate(); err != nil {
		return nil, apperr.Wrap(err, codes.InvalidArgument, "invalid ticket sale")
	}
	if err := current.ValidatePriceChange(sale.Price); err != nil {
		return nil, apperr.Wrap(err, codes.FailedPrecondition, "price is fixed")
	}
	if sale.Quantity < current.SoldCount+current.HeldCount {
		return nil, apperr.New(codes.FailedPrecondition, "quantity is below the tickets sold and held",
			slog.Int("sold", current.SoldCount), slog.Int("held", current.HeldCount))
	}

	if _, err := tx.Exec(ctx, ticketSaleUpdateQuery, string(sale.ID),
		updated.SaleStartTime, updated.SaleEndTime, updated.Price, updated.Quantity, updated.PerAccountLimit,
	); err != nil {
		return nil, toAppErr(err, "failed to update ticket sale", slog.String("ticket_sale_id", string(sale.ID)))
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, toAppErr(err, "failed to commit ticket sale update")
	}
	return &updated, nil
}

// lockTicketSale takes the sale's row lock in tx and then reads the sale with
// its counts at the given time, in that order (see ticketSaleLockQuery).
func lockTicketSale(ctx context.Context, tx pgx.Tx, id entity.TicketSaleID, at time.Time) (*entity.TicketSale, error) {
	var one int
	if err := tx.QueryRow(ctx, ticketSaleLockQuery, string(id)).Scan(&one); err != nil {
		return nil, err
	}
	return scanTicketSale(tx.QueryRow(ctx, ticketSaleGetQuery, string(id), at))
}

// scanTicketSale maps one row selected with ticketSaleColumns.
func scanTicketSale(row pgx.Row) (*entity.TicketSale, error) {
	var (
		s      entity.TicketSale
		id     string
		method int16
	)
	if err := row.Scan(
		&id, &s.EventID, &method, &s.SaleStartTime, &s.SaleEndTime, &s.Price,
		&s.Quantity, &s.PerAccountLimit, &s.SoldCount,
		&s.HeldCount,
	); err != nil {
		return nil, err
	}
	s.ID = entity.TicketSaleID(id)
	s.Method = entity.TicketSaleMethod(method)
	return &s, nil
}
