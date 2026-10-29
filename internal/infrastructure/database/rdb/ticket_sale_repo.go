package rdb

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
)

// TicketSaleRepository implements entity.TicketSaleRepository for PostgreSQL.
// A sale is read in two statements: its ticket_sales row, then the events it
// names in ticket_sale_events.
type TicketSaleRepository struct {
	db *Database
}

// Compile-time interface compliance check.
var _ entity.TicketSaleRepository = (*TicketSaleRepository)(nil)

// rowQuerier is what reading a sale needs from a pool or a transaction.
type rowQuerier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// ticketSaleColumns selects a ticket_sales row (aliased ts).
const ticketSaleColumns = `
	ts.id, ts.series_id, ts.name, ts.method, ts.sale_start_at, ts.sale_end_at,
	ts.verification_requirement, ts.discovered_at
`

const (
	ticketSaleInsertQuery = `
		INSERT INTO ticket_sales (
			id, series_id, name, method, sale_start_at, sale_end_at, verification_requirement
		) VALUES ($1, $2, $3, $4, $5, $6, $7)
	`
	// ticketSaleEventInsertQuery names the event only when it is an event of
	// the sale's Series ($3); another event inserts no row.
	ticketSaleEventInsertQuery = `
		INSERT INTO ticket_sale_events (ticket_sale_id, event_id)
		SELECT $1, e.id FROM events e WHERE e.id = $2 AND e.series_id = $3
	`
	ticketSaleEventExistsQuery = `SELECT EXISTS (SELECT 1 FROM events WHERE id = $1)`
	ticketSaleGetQuery         = `SELECT ` + ticketSaleColumns + ` FROM ticket_sales ts WHERE ts.id = $1`
	// ticketSaleListByEventQuery returns each sale naming the event once,
	// ordered by start time.
	ticketSaleListByEventQuery = `
		SELECT ` + ticketSaleColumns + ` FROM ticket_sales ts
		JOIN ticket_sale_events tse ON tse.ticket_sale_id = ts.id
		WHERE tse.event_id = $1
		ORDER BY ts.sale_start_at, ts.id
	`
	// ticketSaleListDueForDrawQuery returns the Organizer's Lottery sales
	// that ended at or before $1, the instant entity.LotteryDrawDelay before
	// now, and still have an Entered entry. A discovered sale has no entry.
	ticketSaleListDueForDrawQuery = `
		SELECT ` + ticketSaleColumns + ` FROM ticket_sales ts
		WHERE ts.method = 2 AND ts.sale_end_at <= $1 AND ts.discovered_at IS NULL
		  AND EXISTS (SELECT 1 FROM lottery_entries le WHERE le.ticket_sale_id = ts.id AND le.state = 1)
		ORDER BY ts.sale_end_at, ts.id
	`
	ticketSaleListBySeriesQuery = `
		SELECT ` + ticketSaleColumns + ` FROM ticket_sales ts
		WHERE ts.series_id = $1
		ORDER BY ts.sale_start_at, ts.id
	`
	// ticketSaleListWithPendingMilestonesQuery returns the sales that open
	// within the lookahead ($1 seconds) and whose end, the latest milestone,
	// is no further back than the lookback ($2 seconds). An Organizer's sale
	// is returned only while its Series is Published, that is while one of
	// its events is PUBLISHED.
	ticketSaleListWithPendingMilestonesQuery = `
		SELECT ` + ticketSaleColumns + ` FROM ticket_sales ts
		WHERE ts.sale_start_at <= NOW() + make_interval(secs => $1)
		  AND ts.sale_end_at >= NOW() - make_interval(secs => $2)
		  AND (ts.discovered_at IS NOT NULL OR EXISTS (
		        SELECT 1 FROM events e WHERE e.series_id = ts.series_id AND e.publish_state = 'PUBLISHED'))
		ORDER BY ts.sale_start_at, ts.id
	`
	// ticketSaleUpsertDiscoveredQuery stores a discovered sale in one
	// statement. The SELECT from series yields no row when the series does
	// not exist or has an Organizer, so nothing is written and no row is
	// returned. A known discovered sale with the same series, method and start
	// date in Japan time (uq_ticket_sales_discovered_convergence) gets the new
	// name and times and keeps its id, series, method and discovered_at.
	// xmax = 0 is true only for a freshly inserted row.
	ticketSaleUpsertDiscoveredQuery = `
		INSERT INTO ticket_sales (
			id, series_id, name, method, sale_start_at, sale_end_at,
			verification_requirement, discovered_at
		)
		SELECT $1, s.id, $3, $4, $5, $6, 0, NOW()
		FROM series s WHERE s.id = $2 AND s.organizer_id IS NULL
		ON CONFLICT (series_id, method, sale_start_date_jst) WHERE discovered_at IS NOT NULL DO UPDATE
		SET name          = EXCLUDED.name,
		    sale_start_at = EXCLUDED.sale_start_at,
		    sale_end_at   = EXCLUDED.sale_end_at
		RETURNING id, (xmax = 0) AS inserted
	`
	// ticketSaleEventsOfSalesQuery returns the events the sales in $1 name.
	ticketSaleEventsOfSalesQuery = `
		SELECT ticket_sale_id, event_id FROM ticket_sale_events
		WHERE ticket_sale_id = ANY($1::uuid[])
		ORDER BY ticket_sale_id, event_id
	`
	ticketSaleSetWindowQuery = `
		UPDATE ticket_sales SET sale_start_at = $2, sale_end_at = $3 WHERE id = $1
	`
	ticketSaleSetVerificationRequirementQuery = `
		UPDATE ticket_sales SET verification_requirement = $2 WHERE id = $1
	`
)

// NewTicketSaleRepository creates a new ticket sale repository instance.
func NewTicketSaleRepository(db *Database) *TicketSaleRepository {
	return &TicketSaleRepository{db: db}
}

// Create stores the Organizer's sale and the events it names in one
// transaction.
func (r *TicketSaleRepository) Create(ctx context.Context, sale *entity.TicketSale) (*entity.TicketSale, error) {
	if sale == nil {
		return nil, apperr.New(codes.InvalidArgument, "ticket sale must not be nil")
	}
	if err := sale.Validate(); err != nil {
		return nil, apperr.Wrap(err, codes.InvalidArgument, "invalid ticket sale")
	}
	attrs := []slog.Attr{slog.String("ticket_sale_id", string(sale.ID)), slog.String("series_id", sale.SeriesID)}

	if sale.IsDiscovered() {
		return nil, apperr.New(codes.InvalidArgument, "a discovered sale is stored with UpsertDiscovered", attrs...)
	}

	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return nil, toAppErr(err, "failed to begin ticket sale create", attrs...)
	}
	// Rollback is a no-op after a successful Commit.
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, ticketSaleInsertQuery,
		string(sale.ID), sale.SeriesID, sale.Name, int16(sale.Method), sale.SaleStartTime, sale.SaleEndTime,
		int16(sale.VerificationRequirement),
	); err != nil {
		return nil, toAppErr(err, "failed to insert ticket sale", attrs...)
	}
	for _, eventID := range sale.EventIDs {
		eventAttrs := append(attrs, slog.String("event_id", eventID))
		tag, err := tx.Exec(ctx, ticketSaleEventInsertQuery, string(sale.ID), eventID, sale.SeriesID)
		if err != nil {
			return nil, toAppErr(err, "failed to insert the sale's event", eventAttrs...)
		}
		if tag.RowsAffected() == 0 {
			var exists bool
			if err := tx.QueryRow(ctx, ticketSaleEventExistsQuery, eventID).Scan(&exists); err != nil {
				return nil, toAppErr(err, "failed to read the sale's event", eventAttrs...)
			}
			if !exists {
				return nil, apperr.New(codes.FailedPrecondition, "a named event does not exist", eventAttrs...)
			}
			return nil, apperr.New(codes.InvalidArgument, "a named event is not an event of the sale's series", eventAttrs...)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, toAppErr(err, "failed to commit ticket sale create", attrs...)
	}

	r.db.logger.Info(ctx, "ticket sale created",
		slog.String("entityType", "ticket_sale"),
		slog.String("ticket_sale_id", string(sale.ID)),
		slog.Int("event_count", len(sale.EventIDs)),
	)
	return r.Get(ctx, sale.ID)
}

// Get returns the sale with the events it names.
func (r *TicketSaleRepository) Get(ctx context.Context, id entity.TicketSaleID) (*entity.TicketSale, error) {
	sale, err := getTicketSale(ctx, r.db.Pool, id)
	if err != nil {
		return nil, toAppErr(err, "failed to get ticket sale", slog.String("ticket_sale_id", string(id)))
	}
	return sale, nil
}

// ListByEvent returns every sale naming the event, ordered by start time.
func (r *TicketSaleRepository) ListByEvent(ctx context.Context, eventID string) ([]*entity.TicketSale, error) {
	sales, err := listTicketSales(ctx, r.db.Pool, ticketSaleListByEventQuery, eventID)
	if err != nil {
		return nil, toAppErr(err, "failed to list ticket sales by event", slog.String("event_id", eventID))
	}
	return sales, nil
}

// ListDueForDraw returns every Organizer's Lottery sale that ended at least
// entity.LotteryDrawDelay before now and still has an Entered entry.
func (r *TicketSaleRepository) ListDueForDraw(ctx context.Context, now time.Time) ([]*entity.TicketSale, error) {
	sales, err := listTicketSales(ctx, r.db.Pool, ticketSaleListDueForDrawQuery, now.Add(-entity.LotteryDrawDelay))
	if err != nil {
		return nil, toAppErr(err, "failed to list ticket sales due for draw")
	}
	return sales, nil
}

// UpsertDiscovered stores a discovered sale, converging on the known
// discovered sale with the same series, method and start date in Japan time.
func (r *TicketSaleRepository) UpsertDiscovered(ctx context.Context, found *entity.DiscoveredTicketSale) (entity.TicketSaleID, entity.UpsertOutcome, error) {
	if found == nil {
		return "", entity.UpsertOutcomeSkipped, apperr.New(codes.InvalidArgument, "discovered sale must not be nil")
	}
	if found.SeriesID == "" {
		return "", entity.UpsertOutcomeSkipped, apperr.New(codes.InvalidArgument, "discovered sale must name a series")
	}
	attrs := []slog.Attr{slog.String("series_id", found.SeriesID)}
	// A sale whose start or end the pages do not give is skipped, not stored.
	if found.SaleStartTime.IsZero() || found.SaleEndTime.IsZero() {
		r.db.logger.Info(ctx, "ticket_sale_repo: skipping discovered sale without a start or end time", attrs...)
		return "", entity.UpsertOutcomeSkipped, nil
	}
	if err := found.Validate(); err != nil {
		return "", entity.UpsertOutcomeSkipped, apperr.Wrap(err, codes.InvalidArgument, "invalid discovered sale", attrs...)
	}

	var (
		id       string
		inserted bool
	)
	err := r.db.Pool.QueryRow(ctx, ticketSaleUpsertDiscoveredQuery,
		entity.NewID(),
		found.SeriesID,
		found.Name,
		int16(found.Method),
		found.SaleStartTime,
		found.SaleEndTime,
	).Scan(&id, &inserted)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", entity.UpsertOutcomeSkipped, apperr.New(codes.FailedPrecondition,
			"series does not exist or has an Organizer", attrs...)
	}
	if err != nil {
		return "", entity.UpsertOutcomeSkipped, toAppErr(err, "failed to upsert discovered ticket sale", attrs...)
	}
	if inserted {
		return entity.TicketSaleID(id), entity.UpsertOutcomeInserted, nil
	}
	return entity.TicketSaleID(id), entity.UpsertOutcomeUpdated, nil
}

// ListBySeries returns every sale of the series, earliest start first.
func (r *TicketSaleRepository) ListBySeries(ctx context.Context, seriesID string) ([]*entity.TicketSale, error) {
	if seriesID == "" {
		return nil, apperr.New(codes.InvalidArgument, "series ID must not be empty")
	}
	sales, err := listTicketSales(ctx, r.db.Pool, ticketSaleListBySeriesQuery, seriesID)
	if err != nil {
		return nil, toAppErr(err, "failed to list ticket sales by series", slog.String("series_id", seriesID))
	}
	return sales, nil
}

// ListWithPendingMilestones returns the sales with a reminder milestone
// still pending or just past.
func (r *TicketSaleRepository) ListWithPendingMilestones(ctx context.Context, lookahead, lookback time.Duration) ([]*entity.TicketSale, error) {
	if lookahead <= 0 {
		return nil, apperr.New(codes.InvalidArgument, "lookahead must be positive")
	}
	if lookback < 0 {
		return nil, apperr.New(codes.InvalidArgument, "lookback must not be negative")
	}
	sales, err := listTicketSales(ctx, r.db.Pool, ticketSaleListWithPendingMilestonesQuery,
		lookahead.Seconds(), lookback.Seconds())
	if err != nil {
		return nil, toAppErr(err, "failed to list ticket sales with pending milestones",
			slog.Float64("lookahead_secs", lookahead.Seconds()),
			slog.Float64("lookback_secs", lookback.Seconds()),
		)
	}
	return sales, nil
}

// SetWindow stores the sale's new window after checking it against the
// sale's method.
func (r *TicketSaleRepository) SetWindow(ctx context.Context, id entity.TicketSaleID, saleStart, saleEnd time.Time) (*entity.TicketSale, error) {
	attrs := []slog.Attr{slog.String("ticket_sale_id", string(id))}
	current, err := r.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := current.ValidateWindow(saleStart, saleEnd); err != nil {
		return nil, apperr.Wrap(err, codes.InvalidArgument, "invalid sale window", attrs...)
	}
	tag, err := r.db.Pool.Exec(ctx, ticketSaleSetWindowQuery, string(id), saleStart, saleEnd)
	if err != nil {
		return nil, toAppErr(err, "failed to set ticket sale window", attrs...)
	}
	if tag.RowsAffected() == 0 {
		return nil, apperr.New(codes.NotFound, "ticket sale not found", attrs...)
	}
	return r.Get(ctx, id)
}

// SetVerificationRequirement sets the sale's verification requirement.
func (r *TicketSaleRepository) SetVerificationRequirement(ctx context.Context, id entity.TicketSaleID, req entity.VerificationRequirement) (*entity.TicketSale, error) {
	if id == "" {
		return nil, apperr.New(codes.InvalidArgument, "ticket sale ID must not be empty")
	}
	attrs := []slog.Attr{slog.String("ticket_sale_id", string(id))}
	tag, err := r.db.Pool.Exec(ctx, ticketSaleSetVerificationRequirementQuery, string(id), int16(req))
	if err != nil {
		return nil, toAppErr(err, "failed to update ticket sale verification requirement", attrs...)
	}
	if tag.RowsAffected() == 0 {
		return nil, apperr.New(codes.NotFound, "ticket sale not found", attrs...)
	}
	r.db.logger.Info(ctx, "ticket sale verification requirement updated",
		slog.String("ticket_sale_id", string(id)),
		slog.Int("verification_requirement", int(req)),
	)
	return r.Get(ctx, id)
}

// getTicketSale reads one sale with its events.
func getTicketSale(ctx context.Context, q rowQuerier, id entity.TicketSaleID) (*entity.TicketSale, error) {
	sales, err := listTicketSales(ctx, q, ticketSaleGetQuery, string(id))
	if err != nil {
		return nil, err
	}
	if len(sales) == 0 {
		return nil, pgx.ErrNoRows
	}
	return sales[0], nil
}

// listTicketSales runs a query selecting ticketSaleColumns, then reads the
// events every returned sale names and attaches them. It returns an empty
// slice when the query returns no sale.
func listTicketSales(ctx context.Context, q rowQuerier, query string, args ...any) ([]*entity.TicketSale, error) {
	rows, err := q.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	sales, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (*entity.TicketSale, error) {
		return scanTicketSale(row)
	})
	if err != nil {
		return nil, err
	}
	if len(sales) == 0 {
		return []*entity.TicketSale{}, nil
	}

	ids := make([]string, len(sales))
	byID := make(map[entity.TicketSaleID]*entity.TicketSale, len(sales))
	for i, s := range sales {
		ids[i] = string(s.ID)
		byID[s.ID] = s
	}
	eventRows, err := q.Query(ctx, ticketSaleEventsOfSalesQuery, ids)
	if err != nil {
		return nil, err
	}
	var saleID, eventID string
	if _, err := pgx.ForEachRow(eventRows, []any{&saleID, &eventID}, func() error {
		if s, ok := byID[entity.TicketSaleID(saleID)]; ok {
			s.EventIDs = append(s.EventIDs, eventID)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return sales, nil
}

// scanTicketSale maps one row selected with ticketSaleColumns, without its
// events.
func scanTicketSale(row pgx.Row) (*entity.TicketSale, error) {
	var (
		s            entity.TicketSale
		id           string
		method       int16
		verifReq     int16
		discoveredAt sql.NullTime
	)
	if err := row.Scan(
		&id, &s.SeriesID, &s.Name, &method, &s.SaleStartTime, &s.SaleEndTime,
		&verifReq, &discoveredAt,
	); err != nil {
		return nil, err
	}
	s.ID = entity.TicketSaleID(id)
	s.Method = entity.TicketSaleMethod(method)
	s.VerificationRequirement = entity.VerificationRequirement(verifReq)
	s.DiscoveredTime = optionalTime(discoveredAt)
	return &s, nil
}
