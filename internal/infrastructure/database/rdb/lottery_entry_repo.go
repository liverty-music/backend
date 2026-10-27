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

// LotteryEntryRepository implements [entity.LotteryEntryRepository] for
// PostgreSQL.
type LotteryEntryRepository struct {
	db *Database
}

// Compile-time interface compliance check.
var _ entity.LotteryEntryRepository = (*LotteryEntryRepository)(nil)

// NewLotteryEntryRepository creates a new LotteryEntryRepository.
func NewLotteryEntryRepository(db *Database) *LotteryEntryRepository {
	return &LotteryEntryRepository{db: db}
}

// lotteryEntryColumns selects a lottery_entries row.
const lotteryEntryColumns = `
	id, ticket_type_id, user_id, ticket_count, card_authorization_id, state, draw_position, referral_code
`

// lotteryEntryCreateTimeout bounds every wait of Create's transaction: a lock
// held by a concurrent Create or an event removal, and each statement. The
// draw starts entity.LotteryDrawDelay after the end time, longer than one
// Create transaction can take.
const lotteryEntryCreateTimeout = "10s"

const (
	// lotteryEntryCreateLockQuery serializes every Create for one card
	// authorization: a transaction-scoped advisory lock keyed on its id,
	// released at commit or rollback.
	lotteryEntryCreateLockQuery = `SELECT pg_advisory_xact_lock(hashtextextended('lottery-entry-card-authorization:' || $1, 0))`
	// lotteryEntryInsertQuery inserts an Entered entry only while the ticket
	// type exists and its sale's end time has not passed by the database
	// clock; no row is inserted otherwise. uq_lottery_entries_ticket_type_user
	// rejects a second entry of the user for the ticket type, waiting for a
	// concurrent insert to commit first.
	lotteryEntryInsertQuery = `
		INSERT INTO lottery_entries (id, ticket_type_id, user_id, ticket_count, card_authorization_id, state, referral_code)
		SELECT $1, tt.id, $3, $4, $5, 1, $6
		FROM ticket_types tt
		JOIN ticket_sales ts ON ts.id = tt.ticket_sale_id
		WHERE tt.id = $2 AND clock_timestamp() < ts.sale_end_at
	`
	lotteryEntryGetQuery = `SELECT ` + lotteryEntryColumns + ` FROM lottery_entries WHERE id = $1`
	// lotteryEntryGetByCardAuthorizationIDQuery reads the one entry made from
	// the card authorization (uq_lottery_entries_card_authorization_id).
	lotteryEntryGetByCardAuthorizationIDQuery = `
		SELECT ` + lotteryEntryColumns + ` FROM lottery_entries WHERE card_authorization_id = $1
	`
	// lotteryEntryListByTicketTypeAndUserQuery orders newest first: ids are
	// UUIDv7, so id order is creation order.
	lotteryEntryListByTicketTypeAndUserQuery = `
		SELECT ` + lotteryEntryColumns + ` FROM lottery_entries
		WHERE ticket_type_id = $1 AND user_id = $2
		ORDER BY id DESC
	`
	// lotteryEntryListByUserQuery orders newest first by the UUIDv7 id.
	lotteryEntryListByUserQuery = `
		SELECT ` + lotteryEntryColumns + ` FROM lottery_entries
		WHERE user_id = $1
		ORDER BY id DESC
	`
	lotteryEntryListUndrawnQuery = `
		SELECT ` + lotteryEntryColumns + ` FROM lottery_entries
		WHERE ticket_type_id = $1 AND state = 1
	`
	lotteryEntryStatsQuery = `
		SELECT
			COUNT(*),
			COALESCE(SUM(ticket_count), 0),
			COUNT(*) FILTER (WHERE state = 2),
			COALESCE(SUM(ticket_count) FILTER (WHERE state = 2), 0),
			COUNT(*) FILTER (WHERE state = 3)
		FROM lottery_entries
		WHERE ticket_type_id = $1
	`

	// lotteryEntrySetOutcomeQuery sets the state of every entry in $1 to $3
	// with the matching draw position of $2.
	lotteryEntrySetOutcomeQuery = `
		UPDATE lottery_entries AS le
		SET state = $3, draw_position = u.position
		FROM (SELECT unnest($1::uuid[]) AS id, unnest($2::bigint[]) AS position) AS u
		WHERE le.id = u.id
	`
	// ticketSaleStampDrawQuery marks the sale drawn, the last write of the
	// draw's transaction; chk_ticket_sales_draw_lottery_only rejects a
	// FirstCome sale.
	ticketSaleStampDrawQuery = `UPDATE ticket_sales SET draw_at = $2 WHERE id = $1`
)

// Create stores the new Entered entry, or returns the entry that already
// holds its card authorization, in one transaction under the user's shared
// deletion lock and the card authorization's advisory lock.
func (r *LotteryEntryRepository) Create(ctx context.Context, entry *entity.LotteryEntry) (*entity.LotteryEntry, error) {
	attrs := []slog.Attr{
		slog.String("ticket_type_id", string(entry.TicketTypeID)),
		slog.String("user_id", string(entry.UserID)),
		slog.String("card_authorization_id", entry.CardAuthorizationID),
	}
	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return nil, toAppErr(err, "failed to begin lottery entry creation", attrs...)
	}
	// Rollback is a no-op after a successful Commit.
	defer func() { _ = tx.Rollback(ctx) }()

	for _, setting := range []string{"lock_timeout", "statement_timeout"} {
		if _, err := tx.Exec(ctx, "SET LOCAL "+setting+" = '"+lotteryEntryCreateTimeout+"'"); err != nil {
			return nil, toAppErr(err, "failed to bound lottery entry creation", attrs...)
		}
	}
	if err := lockUserForCreate(ctx, tx, entry.UserID); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, lotteryEntryCreateLockQuery, entry.CardAuthorizationID); err != nil {
		return nil, toAppErr(err, "failed to lock the card authorization", attrs...)
	}
	holder, err := scanLotteryEntry(tx.QueryRow(ctx, lotteryEntryGetByCardAuthorizationIDQuery, entry.CardAuthorizationID))
	switch {
	case err == nil:
		// The entry made from the card authorization already exists.
		return holder, nil
	case !errors.Is(err, pgx.ErrNoRows):
		return nil, toAppErr(err, "failed to read the entry of the card authorization", attrs...)
	}

	var code any
	if entry.ReferralCode != nil {
		code = string(*entry.ReferralCode)
	}
	tag, err := tx.Exec(ctx, lotteryEntryInsertQuery,
		string(entry.ID), string(entry.TicketTypeID), string(entry.UserID), entry.TicketCount, entry.CardAuthorizationID, code)
	if err != nil {
		if IsUniqueViolation(err) {
			return nil, apperr.Wrap(err, codes.AlreadyExists, "the user has already entered for the ticket type", attrs...)
		}
		return nil, toAppErr(err, "failed to insert lottery entry", attrs...)
	}
	if tag.RowsAffected() == 0 {
		return nil, apperr.New(codes.FailedPrecondition, "the ticket type does not exist or its sale has ended", attrs...)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, toAppErr(err, "failed to commit lottery entry creation", attrs...)
	}
	r.db.logger.Info(ctx, "lottery entry created",
		slog.String("entityType", "lottery_entry"),
		slog.String("lottery_entry_id", string(entry.ID)),
		slog.String("ticket_type_id", string(entry.TicketTypeID)),
	)
	created := *entry
	created.State = entity.LotteryEntryStateEntered
	created.DrawPosition = nil
	return &created, nil
}

// Get returns the entry by its ID, in any state.
func (r *LotteryEntryRepository) Get(ctx context.Context, id entity.LotteryEntryID) (*entity.LotteryEntry, error) {
	entry, err := scanLotteryEntry(r.db.Pool.QueryRow(ctx, lotteryEntryGetQuery, string(id)))
	if err != nil {
		return nil, toAppErr(err, "failed to get lottery entry", slog.String("lottery_entry_id", string(id)))
	}
	return entry, nil
}

// GetByCardAuthorizationID returns the entry that references the card authorization.
func (r *LotteryEntryRepository) GetByCardAuthorizationID(ctx context.Context, cardAuthorizationID string) (*entity.LotteryEntry, error) {
	entry, err := scanLotteryEntry(r.db.Pool.QueryRow(ctx, lotteryEntryGetByCardAuthorizationIDQuery, cardAuthorizationID))
	if err != nil {
		return nil, toAppErr(err, "failed to get lottery entry by card authorization",
			slog.String("card_authorization_id", cardAuthorizationID))
	}
	return entry, nil
}

// ListByTicketTypeAndUser returns every entry of the user for the ticket
// type, newest first.
func (r *LotteryEntryRepository) ListByTicketTypeAndUser(ctx context.Context, ticketTypeID entity.TicketTypeID, userID entity.UserID) ([]*entity.LotteryEntry, error) {
	entries, err := r.list(ctx, lotteryEntryListByTicketTypeAndUserQuery, string(ticketTypeID), string(userID))
	if err != nil {
		return nil, toAppErr(err, "failed to list lottery entries of the user",
			slog.String("ticket_type_id", string(ticketTypeID)), slog.String("user_id", string(userID)))
	}
	return entries, nil
}

// ListByUser returns every entry of the user, newest first.
func (r *LotteryEntryRepository) ListByUser(ctx context.Context, userID entity.UserID) ([]*entity.LotteryEntry, error) {
	entries, err := r.list(ctx, lotteryEntryListByUserQuery, string(userID))
	if err != nil {
		return nil, toAppErr(err, "failed to list lottery entries of the user", slog.String("user_id", string(userID)))
	}
	return entries, nil
}

// ListUndrawnForTicketType returns every Entered entry of the ticket type.
func (r *LotteryEntryRepository) ListUndrawnForTicketType(ctx context.Context, ticketTypeID entity.TicketTypeID) ([]*entity.LotteryEntry, error) {
	entries, err := r.list(ctx, lotteryEntryListUndrawnQuery, string(ticketTypeID))
	if err != nil {
		return nil, toAppErr(err, "failed to list undrawn lottery entries",
			slog.String("ticket_type_id", string(ticketTypeID)))
	}
	return entries, nil
}

// GetStatsByTicketType counts the ticket type's entries.
func (r *LotteryEntryRepository) GetStatsByTicketType(ctx context.Context, ticketTypeID entity.TicketTypeID) (entity.LotteryEntryStats, error) {
	var s entity.LotteryEntryStats
	if err := r.db.Pool.QueryRow(ctx, lotteryEntryStatsQuery, string(ticketTypeID)).Scan(
		&s.EntryCount, &s.TicketCount, &s.WonEntryCount, &s.WonTicketCount, &s.LostEntryCount,
	); err != nil {
		return entity.LotteryEntryStats{}, toAppErr(err, "failed to get ticket type stats",
			slog.String("ticket_type_id", string(ticketTypeID)))
	}
	return s, nil
}

// PersistDrawOutcome records winners, losers and the sale's draw time in one
// transaction.
func (r *LotteryEntryRepository) PersistDrawOutcome(
	ctx context.Context,
	saleID entity.TicketSaleID,
	winners []entity.DrawWinnerRow,
	losers []entity.DrawLoserRow,
) error {
	if saleID == "" {
		return apperr.New(codes.InvalidArgument, "ticket sale ID must not be empty")
	}
	attrs := []slog.Attr{slog.String("ticket_sale_id", string(saleID))}

	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return toAppErr(err, "failed to begin draw outcome transaction", attrs...)
	}
	// Rollback is a no-op after a successful Commit.
	defer func() { _ = tx.Rollback(ctx) }()

	if len(winners) > 0 {
		ids, positions := make([]string, len(winners)), make([]int64, len(winners))
		for i, w := range winners {
			ids[i], positions[i] = string(w.LotteryEntryID), int64(w.DrawPosition)
		}
		if _, err := tx.Exec(ctx, lotteryEntrySetOutcomeQuery, ids, positions, int16(entity.LotteryEntryStateWon)); err != nil {
			return toAppErr(err, "failed to record the winners", append(attrs, slog.Int("winner_count", len(winners)))...)
		}
	}
	if len(losers) > 0 {
		ids, positions := make([]string, len(losers)), make([]int64, len(losers))
		for i, l := range losers {
			ids[i], positions[i] = string(l.LotteryEntryID), int64(l.DrawPosition)
		}
		if _, err := tx.Exec(ctx, lotteryEntrySetOutcomeQuery, ids, positions, int16(entity.LotteryEntryStateLost)); err != nil {
			return toAppErr(err, "failed to record the losers", append(attrs, slog.Int("loser_count", len(losers)))...)
		}
	}
	// The draw time is the last write, so a crash before it leaves the sale
	// due and the whole outcome is retried on the next sweep.
	tag, err := tx.Exec(ctx, ticketSaleStampDrawQuery, string(saleID), time.Now().UTC())
	if err != nil {
		return toAppErr(err, "failed to set the sale's draw time", attrs...)
	}
	if tag.RowsAffected() == 0 {
		return apperr.New(codes.NotFound, "ticket sale not found", attrs...)
	}
	if err := tx.Commit(ctx); err != nil {
		return toAppErr(err, "failed to commit draw outcome transaction", attrs...)
	}

	r.db.logger.Info(ctx, "draw outcome persisted",
		slog.String("entityType", "ticket_sale"),
		slog.String("ticket_sale_id", string(saleID)),
		slog.Int("winner_count", len(winners)),
		slog.Int("loser_count", len(losers)),
	)
	return nil
}

// list runs a query selecting lotteryEntryColumns and returns its rows, or an
// empty slice when there is none.
func (r *LotteryEntryRepository) list(ctx context.Context, query string, args ...any) ([]*entity.LotteryEntry, error) {
	rows, err := r.db.Pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	entries, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (*entity.LotteryEntry, error) {
		return scanLotteryEntry(row)
	})
	if err != nil {
		return nil, err
	}
	if entries == nil {
		entries = []*entity.LotteryEntry{}
	}
	return entries, nil
}

// scanLotteryEntry maps one row selected with lotteryEntryColumns.
// draw_position is NULL until the draw, and referral_code without a code.
func scanLotteryEntry(row pgx.Row) (*entity.LotteryEntry, error) {
	var (
		e                        entity.LotteryEntry
		id, ticketTypeID, userID string
		state                    int16
		drawPosition             sql.NullInt64
		referralCode             sql.NullString
	)
	if err := row.Scan(&id, &ticketTypeID, &userID, &e.TicketCount, &e.CardAuthorizationID, &state, &drawPosition, &referralCode); err != nil {
		return nil, err
	}
	e.ID = entity.LotteryEntryID(id)
	e.TicketTypeID = entity.TicketTypeID(ticketTypeID)
	e.UserID = entity.UserID(userID)
	e.State = entity.LotteryEntryState(state)
	if drawPosition.Valid {
		position := int(drawPosition.Int64)
		e.DrawPosition = &position
	}
	if referralCode.Valid {
		code := entity.ReferralCode(referralCode.String)
		e.ReferralCode = &code
	}
	return &e, nil
}
