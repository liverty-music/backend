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
	id, ticket_type_id, user_id, ticket_count, card_authorization_id, state, draw_position
`

// getOrCreateDraftAttempts bounds the read-insert loop of GetOrCreateDraft. A
// lost insert race re-reads the winner, so two passes settle every race
// between two callers; the third covers a winner that was itself replaced.
const getOrCreateDraftAttempts = 3

const (
	// lotteryEntryActiveOfUserQuery locks the user's active entry for the
	// ticket type (at most one, by uq_lottery_entries_active).
	lotteryEntryActiveOfUserQuery = `
		SELECT ` + lotteryEntryColumns + ` FROM lottery_entries
		WHERE ticket_type_id = $1 AND user_id = $2 AND state <> 4
		FOR UPDATE
	`
	// lotteryEntryInsertDraftQuery inserts a Draft unless an active entry of
	// the user for the ticket type exists; a concurrent insert of one makes
	// it wait for that transaction and then insert nothing.
	lotteryEntryInsertDraftQuery = `
		INSERT INTO lottery_entries (id, ticket_type_id, user_id, ticket_count, state)
		VALUES ($1, $2, $3, $4, 5)
		ON CONFLICT (ticket_type_id, user_id) WHERE state <> 4 DO NOTHING
	`
	lotteryEntryWithdrawDraftQuery = `UPDATE lottery_entries SET state = 4 WHERE id = $1 AND state = 5`
	// lotteryEntrySetCardAuthorizationQuery records the card authorization on
	// a Draft that has none or already has the same one (a retry).
	lotteryEntrySetCardAuthorizationQuery = `
		UPDATE lottery_entries SET card_authorization_id = $2
		WHERE id = $1 AND state = 5 AND (card_authorization_id IS NULL OR card_authorization_id = $2)
	`
	lotteryEntryGetQuery = `SELECT ` + lotteryEntryColumns + ` FROM lottery_entries WHERE id = $1`
	// lotteryEntryGetByCardAuthorizationIDQuery takes the newest entry with the
	// reference; one hold is recorded on one entry only.
	lotteryEntryGetByCardAuthorizationIDQuery = `
		SELECT ` + lotteryEntryColumns + ` FROM lottery_entries
		WHERE card_authorization_id = $1
		ORDER BY id DESC
		LIMIT 1
	`
	// lotteryEntryListByTicketTypeAndUserQuery orders newest first: ids are
	// UUIDv7, so id order is creation order.
	lotteryEntryListByTicketTypeAndUserQuery = `
		SELECT ` + lotteryEntryColumns + ` FROM lottery_entries
		WHERE ticket_type_id = $1 AND user_id = $2
		ORDER BY id DESC
	`
	lotteryEntryListUndrawnQuery = `
		SELECT ` + lotteryEntryColumns + ` FROM lottery_entries
		WHERE ticket_type_id = $1 AND state IN (1, 5)
	`
	lotteryEntryStatsQuery = `
		SELECT
			COUNT(*),
			COALESCE(SUM(ticket_count), 0),
			COUNT(*) FILTER (WHERE state = 2),
			COALESCE(SUM(ticket_count) FILTER (WHERE state = 2), 0),
			COUNT(*) FILTER (WHERE state = 3)
		FROM lottery_entries
		WHERE ticket_type_id = $1 AND state IN (1, 2, 3)
	`
	lotteryEntryUpdateStateQuery = `UPDATE lottery_entries SET state = $2 WHERE id = $1`

	// lotteryEntrySetOutcomeQuery sets the state of every entry in $1 to $3
	// with the matching draw position of $2.
	lotteryEntrySetOutcomeQuery = `
		UPDATE lottery_entries AS le
		SET state = $3, draw_position = u.position
		FROM (SELECT unnest($1::uuid[]) AS id, unnest($2::bigint[]) AS position) AS u
		WHERE le.id = u.id
	`
	// lotteryEntryWithdrawDraftsQuery withdraws the unconfirmed Drafts of the
	// draw; an entry confirmed meanwhile is not a Draft and is left alone.
	lotteryEntryWithdrawDraftsQuery = `
		UPDATE lottery_entries SET state = 4 WHERE id = ANY($1::uuid[]) AND state = 5
	`
	// ticketSaleStampDrawnQuery marks the sale drawn, the last write of the
	// draw's transaction; chk_ticket_sales_drawn_lottery_only rejects a
	// FirstCome sale.
	ticketSaleStampDrawnQuery = `UPDATE ticket_sales SET draw_at = $2 WHERE id = $1`
)

// GetOrCreateDraft returns the user's Draft with the same count, replaces a
// Draft with another count, or creates a Draft, relying on
// uq_lottery_entries_active so concurrent calls leave one active entry.
func (r *LotteryEntryRepository) GetOrCreateDraft(ctx context.Context, ticketTypeID entity.TicketTypeID, userID entity.UserID, count int) (*entity.LotteryEntry, error) {
	attrs := []slog.Attr{slog.String("ticket_type_id", string(ticketTypeID)), slog.String("user_id", string(userID))}
	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return nil, toAppErr(err, "failed to begin lottery entry start", attrs...)
	}
	// Rollback is a no-op after a successful Commit.
	defer func() { _ = tx.Rollback(ctx) }()

	// Under READ COMMITTED every statement reads the rows committed before it
	// started, so a pass after a lost insert sees the winner's entry.
	for range getOrCreateDraftAttempts {
		existing, err := scanLotteryEntry(tx.QueryRow(ctx, lotteryEntryActiveOfUserQuery, string(ticketTypeID), string(userID)))
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, toAppErr(err, "failed to read the user's active lottery entry", attrs...)
		}
		if existing != nil {
			if existing.State != entity.LotteryEntryStateDraft {
				return nil, apperr.New(codes.AlreadyExists, "the user has already entered for the ticket type",
					append(attrs, slog.String("lottery_entry_id", string(existing.ID)))...)
			}
			if existing.TicketCount == count {
				if err := tx.Commit(ctx); err != nil {
					return nil, toAppErr(err, "failed to commit lottery entry start", attrs...)
				}
				return existing, nil
			}
			if _, err := tx.Exec(ctx, lotteryEntryWithdrawDraftQuery, string(existing.ID)); err != nil {
				return nil, toAppErr(err, "failed to withdraw the replaced draft", attrs...)
			}
		}

		entry := entity.NewLotteryEntry(ticketTypeID, userID, count)
		tag, err := tx.Exec(ctx, lotteryEntryInsertDraftQuery,
			string(entry.ID), string(entry.TicketTypeID), string(entry.UserID), entry.TicketCount)
		if err != nil {
			return nil, toAppErr(err, "failed to insert lottery entry draft", attrs...)
		}
		if tag.RowsAffected() == 0 {
			// A concurrent call created the active entry first: read it.
			continue
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, toAppErr(err, "failed to commit lottery entry start", attrs...)
		}
		r.db.logger.Info(ctx, "lottery entry draft created",
			slog.String("entityType", "lottery_entry"),
			slog.String("lottery_entry_id", string(entry.ID)),
			slog.String("ticket_type_id", string(ticketTypeID)),
		)
		return entry, nil
	}
	return nil, apperr.New(codes.Aborted, "concurrent lottery entry starts did not settle", attrs...)
}

// SetCardAuthorization records the card hold on a Draft.
func (r *LotteryEntryRepository) SetCardAuthorization(ctx context.Context, id entity.LotteryEntryID, cardAuthorizationID string) error {
	attrs := []slog.Attr{slog.String("lottery_entry_id", string(id))}
	tag, err := r.db.Pool.Exec(ctx, lotteryEntrySetCardAuthorizationQuery, string(id), cardAuthorizationID)
	if err != nil {
		return toAppErr(err, "failed to set lottery entry authorization", attrs...)
	}
	if tag.RowsAffected() > 0 {
		return nil
	}
	if _, err := r.Get(ctx, id); err != nil {
		return err
	}
	return apperr.New(codes.FailedPrecondition, "lottery entry is not a draft or has another card authorization", attrs...)
}

// Get returns the entry by its ID, in any state.
func (r *LotteryEntryRepository) Get(ctx context.Context, id entity.LotteryEntryID) (*entity.LotteryEntry, error) {
	entry, err := scanLotteryEntry(r.db.Pool.QueryRow(ctx, lotteryEntryGetQuery, string(id)))
	if err != nil {
		return nil, toAppErr(err, "failed to get lottery entry", slog.String("lottery_entry_id", string(id)))
	}
	return entry, nil
}

// GetByCardAuthorizationID returns the entry whose card hold has the reference.
func (r *LotteryEntryRepository) GetByCardAuthorizationID(ctx context.Context, cardAuthorizationID string) (*entity.LotteryEntry, error) {
	entry, err := scanLotteryEntry(r.db.Pool.QueryRow(ctx, lotteryEntryGetByCardAuthorizationIDQuery, cardAuthorizationID))
	if err != nil {
		return nil, toAppErr(err, "failed to get lottery entry by payment intent",
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

// ListUndrawnForTicketType returns every Draft and Entered entry of the
// ticket type.
func (r *LotteryEntryRepository) ListUndrawnForTicketType(ctx context.Context, ticketTypeID entity.TicketTypeID) ([]*entity.LotteryEntry, error) {
	entries, err := r.list(ctx, lotteryEntryListUndrawnQuery, string(ticketTypeID))
	if err != nil {
		return nil, toAppErr(err, "failed to list undrawn lottery entries",
			slog.String("ticket_type_id", string(ticketTypeID)))
	}
	return entries, nil
}

// GetStatsByTicketType tallies the ticket type's Entered, Won and Lost entries.
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

// PersistDrawOutcome records winners, losers, withdrawn Drafts and the sale's
// drawn time in one transaction.
func (r *LotteryEntryRepository) PersistDrawOutcome(
	ctx context.Context,
	saleID entity.TicketSaleID,
	winners []entity.DrawWinnerRow,
	losers []entity.DrawLoserRow,
	withdrawnDrafts []entity.LotteryEntryID,
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
	if len(withdrawnDrafts) > 0 {
		ids := make([]string, len(withdrawnDrafts))
		for i, id := range withdrawnDrafts {
			ids[i] = string(id)
		}
		if _, err := tx.Exec(ctx, lotteryEntryWithdrawDraftsQuery, ids); err != nil {
			return toAppErr(err, "failed to withdraw the unconfirmed drafts", append(attrs, slog.Int("draft_count", len(ids)))...)
		}
	}

	// The drawn time is the last write, so a crash before it leaves the sale
	// due and the whole outcome is retried on the next sweep.
	tag, err := tx.Exec(ctx, ticketSaleStampDrawnQuery, string(saleID), time.Now().UTC())
	if err != nil {
		return toAppErr(err, "failed to set the sale's drawn time", attrs...)
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
		slog.Int("withdrawn_draft_count", len(withdrawnDrafts)),
	)
	return nil
}

// UpdateState sets the entry's state.
func (r *LotteryEntryRepository) UpdateState(ctx context.Context, id entity.LotteryEntryID, state entity.LotteryEntryState) error {
	attrs := []slog.Attr{slog.String("lottery_entry_id", string(id)), slog.Int("state", int(state))}
	tag, err := r.db.Pool.Exec(ctx, lotteryEntryUpdateStateQuery, string(id), int16(state))
	if err != nil {
		return toAppErr(err, "failed to update lottery entry state", attrs...)
	}
	if tag.RowsAffected() == 0 {
		return apperr.New(codes.NotFound, "lottery entry not found", attrs...)
	}
	r.db.logger.Info(ctx, "lottery entry state updated",
		slog.String("entityType", "lottery_entry"),
		slog.String("lottery_entry_id", string(id)),
		slog.Int("state", int(state)),
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
// card_authorization_id is NULL on a Draft without a card authorization, and draw_position is
// NULL until the draw.
func scanLotteryEntry(row pgx.Row) (*entity.LotteryEntry, error) {
	var (
		e                        entity.LotteryEntry
		id, ticketTypeID, userID string
		cardAuthorizationID      sql.NullString
		state                    int16
		drawPosition             sql.NullInt64
	)
	if err := row.Scan(&id, &ticketTypeID, &userID, &e.TicketCount, &cardAuthorizationID, &state, &drawPosition); err != nil {
		return nil, err
	}
	e.ID = entity.LotteryEntryID(id)
	e.TicketTypeID = entity.TicketTypeID(ticketTypeID)
	e.UserID = entity.UserID(userID)
	e.State = entity.LotteryEntryState(state)
	if cardAuthorizationID.Valid {
		e.CardAuthorizationID = cardAuthorizationID.String
	}
	if drawPosition.Valid {
		position := int(drawPosition.Int64)
		e.DrawPosition = &position
	}
	return &e, nil
}
