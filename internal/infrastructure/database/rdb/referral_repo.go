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

// ReferralRepository implements entity.ReferralRepository for PostgreSQL.
// Record takes a share lock on the source row before it writes, so it
// serializes with the confirmation of the source (Checkout commit, LotteryEntry
// confirmation), which updates the same row.
type ReferralRepository struct {
	db *Database
}

// Compile-time interface compliance check.
var _ entity.ReferralRepository = (*ReferralRepository)(nil)

// NewReferralRepository creates a new referral repository instance.
func NewReferralRepository(db *Database) *ReferralRepository {
	return &ReferralRepository{db: db}
}

const (
	// referralLockCheckoutQuery locks the checkout row against a concurrent
	// commit and reads its status.
	referralLockCheckoutQuery = `SELECT status FROM checkouts WHERE id = $1 FOR SHARE`
	// referralLockLotteryEntryQuery locks the lottery entry row against a
	// concurrent confirmation and reads its state.
	referralLockLotteryEntryQuery = `SELECT state FROM lottery_entries WHERE id = $1 FOR SHARE`
	// referralUpsertCheckoutQuery creates the checkout's referral or replaces
	// its code, keeping created_at; the same code writes nothing.
	referralUpsertCheckoutQuery = `
		INSERT INTO referrals (id, checkout_id, code, created_at) VALUES ($1, $2, $3, $4)
		ON CONFLICT (checkout_id) WHERE checkout_id IS NOT NULL
		DO UPDATE SET code = EXCLUDED.code WHERE referrals.code <> EXCLUDED.code
	`
	// referralUpsertLotteryEntryQuery is referralUpsertCheckoutQuery for a
	// lottery entry.
	referralUpsertLotteryEntryQuery = `
		INSERT INTO referrals (id, lottery_entry_id, code, created_at) VALUES ($1, $2, $3, $4)
		ON CONFLICT (lottery_entry_id) WHERE lottery_entry_id IS NOT NULL
		DO UPDATE SET code = EXCLUDED.code WHERE referrals.code <> EXCLUDED.code
	`
	// referralListStatsByEventQuery counts the event's Paid orders (status 1)
	// per referral code of their source; a NULL code is the direct group.
	// Tickets are the source's ticket count. COLLATE "C" compares codes
	// character by character.
	referralListStatsByEventQuery = `
		SELECT r.code, count(*)::int, sum(coalesce(c.ticket_count, le.ticket_count))::int, sum(o.amount)::bigint
		FROM orders o
		LEFT JOIN checkouts c        ON c.id = o.checkout_id
		LEFT JOIN lottery_entries le ON le.id = o.lottery_entry_id
		JOIN ticket_types tt         ON tt.id = coalesce(c.ticket_type_id, le.ticket_type_id)
		LEFT JOIN referrals r        ON r.checkout_id = o.checkout_id OR r.lottery_entry_id = o.lottery_entry_id
		WHERE tt.event_id = $1 AND o.status = 1
		GROUP BY r.code
		ORDER BY count(*) DESC, r.code COLLATE "C"
	`
)

// Record stores the source's referral code while the source is open.
func (r *ReferralRepository) Record(ctx context.Context, source entity.ReferralSource, code entity.ReferralCode, at time.Time) error {
	if err := source.Validate(); err != nil {
		return apperr.Wrap(err, codes.InvalidArgument, err.Error())
	}
	if err := code.Validate(); err != nil {
		return apperr.Wrap(err, codes.InvalidArgument, err.Error())
	}
	attrs := []slog.Attr{
		slog.String("checkout_id", string(source.CheckoutID)),
		slog.String("lottery_entry_id", string(source.LotteryEntryID)),
	}

	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return toAppErr(err, "failed to begin referral record", attrs...)
	}
	// Rollback is a no-op after a successful Commit.
	defer func() { _ = tx.Rollback(ctx) }()

	open, err := lockReferralSource(ctx, tx, source)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return apperr.New(codes.FailedPrecondition, "the referral's source does not exist", attrs...)
		}
		return toAppErr(err, "failed to lock referral source", attrs...)
	}
	if !open {
		// A confirmed source keeps its Referral, or stays without one.
		return nil
	}

	query, sourceID := referralUpsertCheckoutQuery, string(source.CheckoutID)
	if source.LotteryEntryID != "" {
		query, sourceID = referralUpsertLotteryEntryQuery, string(source.LotteryEntryID)
	}
	if _, err := tx.Exec(ctx, query, entity.NewID(), sourceID, string(code), at); err != nil {
		return toAppErr(err, "failed to record referral", attrs...)
	}
	if err := tx.Commit(ctx); err != nil {
		return toAppErr(err, "failed to commit referral record", attrs...)
	}
	return nil
}

// lockReferralSource share-locks the source row and reports whether its
// Referral may still change. It returns pgx.ErrNoRows when the source does not
// exist.
func lockReferralSource(ctx context.Context, tx pgx.Tx, source entity.ReferralSource) (bool, error) {
	if source.CheckoutID != "" {
		var status int16
		if err := tx.QueryRow(ctx, referralLockCheckoutQuery, string(source.CheckoutID)).Scan(&status); err != nil {
			return false, err
		}
		return entity.CheckoutStatus(status).AcceptsReferral(), nil
	}
	var state int16
	if err := tx.QueryRow(ctx, referralLockLotteryEntryQuery, string(source.LotteryEntryID)).Scan(&state); err != nil {
		return false, err
	}
	return entity.LotteryEntryState(state).AcceptsReferral(), nil
}

// ListStatsByEvent counts the event's Paid Orders per referral code.
func (r *ReferralRepository) ListStatsByEvent(ctx context.Context, eventID string) ([]entity.ReferralCodeStats, error) {
	rows, err := r.db.Pool.Query(ctx, referralListStatsByEventQuery, eventID)
	if err != nil {
		return nil, toAppErr(err, "failed to list referral stats", slog.String("event_id", eventID))
	}
	defer rows.Close()

	var stats []entity.ReferralCodeStats
	for rows.Next() {
		var (
			code        sql.NullString
			orderCount  int
			ticketCount int
			amount      int64
		)
		if err := rows.Scan(&code, &orderCount, &ticketCount, &amount); err != nil {
			return nil, toAppErr(err, "failed to scan referral stats", slog.String("event_id", eventID))
		}
		s := entity.ReferralCodeStats{OrderCount: orderCount, TicketCount: ticketCount, Amount: amount}
		if code.Valid {
			c := entity.ReferralCode(code.String)
			s.Code = &c
		}
		stats = append(stats, s)
	}
	if err := rows.Err(); err != nil {
		return nil, toAppErr(err, "failed to iterate referral stats", slog.String("event_id", eventID))
	}
	return stats, nil
}
