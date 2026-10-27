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
// A sale is read in two statements: its ticket_sales row, then all of its
// ticket_types rows, each with the tickets its Checkouts hold at the read
// time.
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
	ts.verification_requirement, ts.draw_at, ts.lottery_result_at, ts.discovered_at
`

const (
	ticketSaleInsertQuery = `
		INSERT INTO ticket_sales (
			id, series_id, name, method, sale_start_at, sale_end_at, verification_requirement
		) VALUES ($1, $2, $3, $4, $5, $6, $7)
	`
	ticketTypeInsertQuery = `
		INSERT INTO ticket_types (
			id, ticket_sale_id, event_id, price, quantity, per_account_limit, sold_count
		) VALUES ($1, $2, $3, $4, $5, $6, 0)
	`
	ticketSaleGetQuery = `SELECT ` + ticketSaleColumns + ` FROM ticket_sales ts WHERE ts.id = $1`
	// ticketSaleListByEventQuery returns each sale with a ticket type for the
	// event once, ordered by start time.
	ticketSaleListByEventQuery = `
		SELECT ` + ticketSaleColumns + ` FROM ticket_sales ts
		WHERE EXISTS (SELECT 1 FROM ticket_types tt WHERE tt.ticket_sale_id = ts.id AND tt.event_id = $1)
		ORDER BY ts.sale_start_at, ts.id
	`
	// ticketSaleListDueForDrawQuery returns the undrawn Lottery sales that
	// ended at or before $1, the instant entity.LotteryDrawDelay before now.
	// It skips discovered sales: the platform takes no entries for them, and
	// drawing one would break chk_ticket_sales_discovered.
	ticketSaleListDueForDrawQuery = `
		SELECT ` + ticketSaleColumns + ` FROM ticket_sales ts
		WHERE ts.method = 2 AND ts.sale_end_at <= $1 AND ts.draw_at IS NULL
		  AND ts.discovered_at IS NULL
		ORDER BY ts.sale_end_at, ts.id
	`
	ticketSaleListBySeriesQuery = `
		SELECT ` + ticketSaleColumns + ` FROM ticket_sales ts
		WHERE ts.series_id = $1
		ORDER BY ts.sale_start_at, ts.id
	`
	// ticketSaleListWithPendingMilestonesQuery returns the sales that open
	// within the lookahead ($1 seconds) and whose latest milestone is no
	// further back than the lookback ($2 seconds). The milestones are the
	// start, the end and the result time; an Organizer's lottery's result
	// time is its end, already in GREATEST. An Organizer's sale is returned
	// only while its Series is Published, that is while one of its events is
	// PUBLISHED.
	ticketSaleListWithPendingMilestonesQuery = `
		SELECT ` + ticketSaleColumns + ` FROM ticket_sales ts
		WHERE ts.sale_start_at <= NOW() + make_interval(secs => $1)
		  AND GREATEST(
		        ts.sale_start_at,
		        COALESCE(ts.sale_end_at,       '-infinity'::timestamptz),
		        COALESCE(ts.lottery_result_at, '-infinity'::timestamptz)
		      ) >= NOW() - make_interval(secs => $2)
		  AND (ts.discovered_at IS NOT NULL OR EXISTS (
		        SELECT 1 FROM events e WHERE e.series_id = ts.series_id AND e.publish_state = 'PUBLISHED'))
		ORDER BY ts.sale_start_at, ts.id
	`
	// ticketSaleUpsertDiscoveredQuery stores a discovered sale in one
	// statement. The SELECT from series yields no row when the series does
	// not exist or has an Organizer, so nothing is written and no row is
	// returned. A known discovered sale with the same series, method and start
	// date in Japan time (uq_ticket_sales_discovered_convergence) gets the new
	// times (NULL clears a stored one) and keeps its id, series, method and
	// discovered_at. xmax = 0 is true only for a freshly inserted row.
	ticketSaleUpsertDiscoveredQuery = `
		INSERT INTO ticket_sales (
			id, series_id, name, method, sale_start_at, sale_end_at,
			verification_requirement, lottery_result_at, discovered_at
		)
		SELECT $1, s.id, NULL, $3, $4, $5, 0, $6, NOW()
		FROM series s WHERE s.id = $2 AND s.organizer_id IS NULL
		ON CONFLICT (series_id, method, sale_start_date_jst) WHERE discovered_at IS NOT NULL DO UPDATE
		SET sale_start_at     = EXCLUDED.sale_start_at,
		    sale_end_at       = EXCLUDED.sale_end_at,
		    lottery_result_at = EXCLUDED.lottery_result_at
		RETURNING id, (xmax = 0) AS inserted
	`
	ticketSaleOfTicketTypeQuery = `
		SELECT ` + ticketSaleColumns + ` FROM ticket_sales ts
		JOIN ticket_types tt ON tt.ticket_sale_id = ts.id
		WHERE tt.id = $1
	`
	// ticketTypesOfSalesQuery returns the ticket types of the sales in $1,
	// each with the tickets of its stock Checkouts that are Held and holding
	// at $2, its resale open time, and its Listed listings that are free at $2:
	// held by no Checkout, or by one that is neither holding at $2 nor
	// Committed. The resale deadline is checked by the caller.
	ticketTypesOfSalesQuery = `
		SELECT tt.id, tt.ticket_sale_id, tt.event_id, tt.price, tt.quantity,
		       tt.per_account_limit, tt.sold_count,
		       (SELECT COALESCE(SUM(c.ticket_count), 0)::int FROM checkouts c
		        WHERE c.ticket_type_id = tt.id AND c.status = 1 AND c.hold_expire_at > $2
		          AND NOT c.resale),
		       tt.resale_open_at,
		       (SELECT count(*)::int FROM resale_listings rl
		        LEFT JOIN checkouts c ON c.id = rl.checkout_id
		        WHERE rl.ticket_type_id = tt.id AND rl.status = 1
		          AND (c.id IS NULL OR NOT ((c.status = 1 AND c.hold_expire_at > $2) OR c.status = 2)))
		FROM ticket_types tt
		WHERE tt.ticket_sale_id = ANY($1::uuid[])
		ORDER BY tt.id
	`
	ticketSaleSetWindowQuery = `
		UPDATE ticket_sales SET sale_start_at = $2, sale_end_at = $3 WHERE id = $1
	`
	ticketSaleSetVerificationRequirementQuery = `
		UPDATE ticket_sales SET verification_requirement = $2 WHERE id = $1
	`
	// ticketTypeLockQuery takes the ticket type's row lock, which Starts and
	// Commits of its Checkouts also take. The counts are read by a separate,
	// later statement: under READ COMMITTED a statement that waited for the
	// lock still evaluates its subqueries with the snapshot taken before it
	// waited, so reading the counts in the locking statement (or checking
	// them in one conditional UPDATE) would miss the holds committed by the
	// transaction it waited for.
	ticketTypeLockQuery   = `SELECT 1 FROM ticket_types WHERE id = $1 FOR UPDATE`
	ticketTypeUpdateQuery = `
		UPDATE ticket_types SET price = $2, quantity = $3, per_account_limit = $4 WHERE id = $1
	`
)

// NewTicketSaleRepository creates a new ticket sale repository instance.
func NewTicketSaleRepository(db *Database) *TicketSaleRepository {
	return &TicketSaleRepository{db: db}
}

// Create stores the sale without a draw time and all of its ticket types in
// one transaction.
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
	created := *sale
	created.DrawTime = nil
	created.TicketTypes = make([]*entity.TicketType, 0, len(sale.TicketTypes))
	for _, tt := range sale.TicketTypes {
		if _, err := tx.Exec(ctx, ticketTypeInsertQuery,
			string(tt.ID), string(sale.ID), tt.EventID, tt.Price, tt.Quantity, tt.PerAccountLimit,
		); err != nil {
			return nil, toAppErr(err, "failed to insert ticket type",
				append(attrs, slog.String("ticket_type_id", string(tt.ID)), slog.String("event_id", tt.EventID))...)
		}
		stored := *tt
		stored.TicketSaleID = sale.ID
		stored.SoldCount, stored.HeldCount = 0, 0
		created.TicketTypes = append(created.TicketTypes, &stored)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, toAppErr(err, "failed to commit ticket sale create", attrs...)
	}

	r.db.logger.Info(ctx, "ticket sale created",
		slog.String("entityType", "ticket_sale"),
		slog.String("ticket_sale_id", string(created.ID)),
		slog.Int("ticket_type_count", len(created.TicketTypes)),
	)
	return &created, nil
}

// Get returns the sale with all of its ticket types and their held counts at
// the given time.
func (r *TicketSaleRepository) Get(ctx context.Context, id entity.TicketSaleID, at time.Time) (*entity.TicketSale, error) {
	sale, err := getTicketSale(ctx, r.db.Pool, id, at)
	if err != nil {
		return nil, toAppErr(err, "failed to get ticket sale", slog.String("ticket_sale_id", string(id)))
	}
	return sale, nil
}

// ListByEvent returns every sale with a ticket type for the event, ordered by
// start time.
func (r *TicketSaleRepository) ListByEvent(ctx context.Context, eventID string, at time.Time) ([]*entity.TicketSale, error) {
	sales, err := listTicketSales(ctx, r.db.Pool, at, ticketSaleListByEventQuery, eventID)
	if err != nil {
		return nil, toAppErr(err, "failed to list ticket sales by event", slog.String("event_id", eventID))
	}
	return sales, nil
}

// ListDueForDraw returns every Lottery sale that ended at least
// entity.LotteryDrawDelay before now and is not drawn.
func (r *TicketSaleRepository) ListDueForDraw(ctx context.Context, now time.Time) ([]*entity.TicketSale, error) {
	sales, err := listTicketSales(ctx, r.db.Pool, now, ticketSaleListDueForDrawQuery, now.Add(-entity.LotteryDrawDelay))
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
	// A sale whose start the pages do not give is skipped, not stored.
	if found.SaleStartTime.IsZero() {
		r.db.logger.Info(ctx, "ticket_sale_repo: skipping discovered sale without a start time", attrs...)
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
		int16(found.Method),
		found.SaleStartTime,
		nullableTime(found.SaleEndTime),
		nullableTime(found.LotteryResultTime),
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
	sales, err := listTicketSales(ctx, r.db.Pool, time.Now(), ticketSaleListBySeriesQuery, seriesID)
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
	sales, err := listTicketSales(ctx, r.db.Pool, time.Now(), ticketSaleListWithPendingMilestonesQuery,
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
func (r *TicketSaleRepository) SetWindow(ctx context.Context, id entity.TicketSaleID, saleStart, saleEnd time.Time, at time.Time) (*entity.TicketSale, error) {
	attrs := []slog.Attr{slog.String("ticket_sale_id", string(id))}
	current, err := r.Get(ctx, id, at)
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
	return r.Get(ctx, id, at)
}

// SetVerificationRequirement sets the sale's verification requirement.
func (r *TicketSaleRepository) SetVerificationRequirement(ctx context.Context, id entity.TicketSaleID, req entity.VerificationRequirement, at time.Time) (*entity.TicketSale, error) {
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
	return r.Get(ctx, id, at)
}

// GetTicketType returns the sale offering the ticket type, and that ticket
// type.
func (r *TicketSaleRepository) GetTicketType(ctx context.Context, id entity.TicketTypeID, at time.Time) (*entity.TicketSale, *entity.TicketType, error) {
	sale, tt, err := getTicketType(ctx, r.db.Pool, id, at)
	if err != nil {
		return nil, nil, toAppErr(err, "failed to get ticket type", slog.String("ticket_type_id", string(id)))
	}
	return sale, tt, nil
}

// UpdateTicketType stores the ticket type's price, quantity and per-account
// limit. Under the ticket type's row lock it reads the sold and held counts,
// checks the first-come price lock and the sold-plus-held floor, and writes,
// all in one transaction, so a Checkout started meanwhile is counted.
func (r *TicketSaleRepository) UpdateTicketType(ctx context.Context, ticketType *entity.TicketType, at time.Time) (*entity.TicketSale, *entity.TicketType, error) {
	if ticketType == nil {
		return nil, nil, apperr.New(codes.InvalidArgument, "ticket type must not be nil")
	}
	attrs := []slog.Attr{slog.String("ticket_type_id", string(ticketType.ID))}

	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return nil, nil, toAppErr(err, "failed to begin ticket type update", attrs...)
	}
	// Rollback is a no-op after a successful Commit.
	defer func() { _ = tx.Rollback(ctx) }()

	sale, current, err := lockTicketType(ctx, tx, ticketType.ID, at)
	if err != nil {
		return nil, nil, toAppErr(err, "failed to lock ticket type", attrs...)
	}

	updated := *current
	updated.Price = ticketType.Price
	updated.Quantity = ticketType.Quantity
	updated.PerAccountLimit = ticketType.PerAccountLimit
	if err := updated.Validate(); err != nil {
		return nil, nil, apperr.Wrap(err, codes.InvalidArgument, "invalid ticket type", attrs...)
	}
	if sale.Method == entity.TicketSaleMethodFirstCome {
		if err := current.ValidatePriceChange(updated.Price); err != nil {
			return nil, nil, apperr.Wrap(err, codes.FailedPrecondition, "price is fixed", attrs...)
		}
		if updated.Quantity < current.SoldCount+current.HeldCount {
			return nil, nil, apperr.New(codes.FailedPrecondition, "quantity is below the tickets sold and held",
				append(attrs, slog.Int("sold", current.SoldCount), slog.Int("held", current.HeldCount))...)
		}
	}

	if _, err := tx.Exec(ctx, ticketTypeUpdateQuery,
		string(updated.ID), updated.Price, updated.Quantity, updated.PerAccountLimit,
	); err != nil {
		return nil, nil, toAppErr(err, "failed to update ticket type", attrs...)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, nil, toAppErr(err, "failed to commit ticket type update", attrs...)
	}

	*current = updated
	return sale, current, nil
}

// lockTicketType takes the ticket type's row lock in tx and then reads its
// sale with all ticket types and their counts at the given time, in that
// order (see ticketTypeLockQuery). It returns the raw database error, with
// pgx.ErrNoRows when no ticket type has the id; callers wrap it with
// toAppErr.
func lockTicketType(ctx context.Context, tx pgx.Tx, id entity.TicketTypeID, at time.Time) (*entity.TicketSale, *entity.TicketType, error) {
	var one int
	if err := tx.QueryRow(ctx, ticketTypeLockQuery, string(id)).Scan(&one); err != nil {
		return nil, nil, err
	}
	return getTicketType(ctx, tx, id, at)
}

// getTicketSale reads one sale with its ticket types.
func getTicketSale(ctx context.Context, q rowQuerier, id entity.TicketSaleID, at time.Time) (*entity.TicketSale, error) {
	sales, err := listTicketSales(ctx, q, at, ticketSaleGetQuery, string(id))
	if err != nil {
		return nil, err
	}
	if len(sales) == 0 {
		return nil, pgx.ErrNoRows
	}
	return sales[0], nil
}

// getTicketType reads the sale offering the ticket type, with its ticket
// types, and picks the ticket type out of them.
func getTicketType(ctx context.Context, q rowQuerier, id entity.TicketTypeID, at time.Time) (*entity.TicketSale, *entity.TicketType, error) {
	sales, err := listTicketSales(ctx, q, at, ticketSaleOfTicketTypeQuery, string(id))
	if err != nil {
		return nil, nil, err
	}
	if len(sales) == 0 {
		return nil, nil, pgx.ErrNoRows
	}
	tt := sales[0].TicketType(id)
	if tt == nil {
		return nil, nil, pgx.ErrNoRows
	}
	return sales[0], tt, nil
}

// listTicketSales runs a query selecting ticketSaleColumns, then reads the
// ticket types of every returned sale with their held counts at `at`, and
// attaches them. It returns an empty slice when the query returns no sale.
func listTicketSales(ctx context.Context, q rowQuerier, at time.Time, query string, args ...any) ([]*entity.TicketSale, error) {
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
		s.TicketTypes = []*entity.TicketType{}
		byID[s.ID] = s
	}
	typeRows, err := q.Query(ctx, ticketTypesOfSalesQuery, ids, at)
	if err != nil {
		return nil, err
	}
	types, err := pgx.CollectRows(typeRows, func(row pgx.CollectableRow) (*entity.TicketType, error) {
		return scanTicketType(row)
	})
	if err != nil {
		return nil, err
	}
	for _, tt := range types {
		if s, ok := byID[tt.TicketSaleID]; ok {
			s.TicketTypes = append(s.TicketTypes, tt)
		}
	}
	return sales, nil
}

// scanTicketSale maps one row selected with ticketSaleColumns, without its
// ticket types.
func scanTicketSale(row pgx.Row) (*entity.TicketSale, error) {
	var (
		s            entity.TicketSale
		id           string
		name         sql.NullString
		method       int16
		saleEndAt    sql.NullTime
		verifReq     int16
		drawAt       sql.NullTime
		resultAt     sql.NullTime
		discoveredAt sql.NullTime
	)
	if err := row.Scan(
		&id, &s.SeriesID, &name, &method, &s.SaleStartTime, &saleEndAt,
		&verifReq, &drawAt, &resultAt, &discoveredAt,
	); err != nil {
		return nil, err
	}
	if name.Valid {
		s.Name = name.String
	}
	if saleEndAt.Valid {
		s.SaleEndTime = saleEndAt.Time
	}
	if resultAt.Valid {
		resultTime := resultAt.Time
		s.LotteryResultTime = &resultTime
	}
	if discoveredAt.Valid {
		discoveredTime := discoveredAt.Time
		s.DiscoveredTime = &discoveredTime
	}
	s.ID = entity.TicketSaleID(id)
	s.Method = entity.TicketSaleMethod(method)
	s.VerificationRequirement = entity.VerificationRequirement(verifReq)
	if drawAt.Valid {
		drawTime := drawAt.Time
		s.DrawTime = &drawTime
	}
	return &s, nil
}

// scanTicketType maps one row selected by ticketTypesOfSalesQuery.
func scanTicketType(row pgx.Row) (*entity.TicketType, error) {
	var (
		tt           entity.TicketType
		id           string
		saleID       string
		resaleOpenAt sql.NullTime
	)
	if err := row.Scan(
		&id, &saleID, &tt.EventID, &tt.Price, &tt.Quantity,
		&tt.PerAccountLimit, &tt.SoldCount, &tt.HeldCount,
		&resaleOpenAt, &tt.FreeResaleCount,
	); err != nil {
		return nil, err
	}
	tt.ID = entity.TicketTypeID(id)
	tt.TicketSaleID = entity.TicketSaleID(saleID)
	tt.ResaleOpenTime = optionalTime(resaleOpenAt)
	return &tt, nil
}

// nullableTime returns a *time.Time (nil for the zero time) so pgx maps an
// unknown time to SQL NULL.
func nullableTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}
