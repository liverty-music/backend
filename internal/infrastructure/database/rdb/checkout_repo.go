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

// CheckoutRepository implements entity.CheckoutRepository for PostgreSQL.
// Every write that changes a ticket type's sold count, and every start, takes
// the ticket type's row lock first and the checkout row second, so the writes
// of one ticket type never deadlock.
type CheckoutRepository struct {
	db *Database
}

// Compile-time interface compliance check.
var _ entity.CheckoutRepository = (*CheckoutRepository)(nil)

const checkoutColumns = `
	id, ticket_type_id, user_id, ticket_count, amount,
	card_authorization_id, card_authorization_release_at, status, hold_expire_at,
	commit_at, capture_at, resale
`

const (
	checkoutGetQuery                      = `SELECT ` + checkoutColumns + ` FROM checkouts WHERE id = $1`
	checkoutGetByCardAuthorizationIDQuery = `SELECT ` + checkoutColumns + ` FROM checkouts WHERE card_authorization_id = $1`
	// checkoutHeldOfUserQuery locks the user's Held checkout of the ticket type
	// (at most one, by the partial unique index), lapsed or not.
	checkoutHeldOfUserQuery = `
		SELECT ` + checkoutColumns + ` FROM checkouts
		WHERE ticket_type_id = $1 AND user_id = $2 AND status = 1
		FOR UPDATE
	`
	// checkoutUserTicketsQuery counts the tickets that weigh on the user's
	// per-account limit for the ticket type ($1): the user's Issued tickets
	// for the type's event, plus the tickets on the user's Committed checkouts
	// for the type that have no Order yet (charged or being charged, not
	// issued).
	checkoutUserTicketsQuery = `
		SELECT
			(SELECT count(*) FROM tickets t
			 JOIN ticket_types tt ON tt.event_id = t.event_id
			 WHERE tt.id = $1 AND t.user_id = $2 AND t.status = 1)::int
			+
			(SELECT COALESCE(SUM(c.ticket_count), 0) FROM checkouts c
			 WHERE c.ticket_type_id = $1 AND c.user_id = $2 AND c.status = 2
			   AND NOT EXISTS (SELECT 1 FROM orders o WHERE o.checkout_id = c.id))::int
	`
	checkoutEndHeldQuery = `UPDATE checkouts SET status = $2 WHERE id = $1 AND status = 1`
	checkoutInsertQuery  = `
		INSERT INTO checkouts (
			id, ticket_type_id, user_id, ticket_count, amount, status, hold_expire_at, resale
		) VALUES ($1, $2, $3, $4, $5, 1, $6, $7)
	`
	// checkoutFreeListingsQuery locks, oldest first, up to $4 Listed listings
	// of the ticket type ($1) that are free at $3 for the user ($2): not the
	// user's own, and not held by a Checkout that is holding at $3 or
	// Committed, except the user's own holding Checkout ($5), which is
	// replaced. The caller holds the ticket type's row lock, which every hold
	// and withdrawal of its listings takes first.
	checkoutFreeListingsQuery = `
		SELECT rl.id, rl.user_id FROM resale_listings rl
		LEFT JOIN checkouts c ON c.id = rl.checkout_id
		WHERE rl.ticket_type_id = $1 AND rl.status = 1 AND rl.user_id <> $2
		  AND (c.id IS NULL OR c.id = $5
		       OR NOT ((c.status = 1 AND c.hold_expire_at > $3) OR c.status = 2))
		ORDER BY rl.created_at, rl.id
		LIMIT $4
		FOR UPDATE OF rl
	`
	checkoutHoldListingsQuery         = `UPDATE resale_listings SET checkout_id = $2 WHERE id = ANY($1::uuid[])`
	checkoutSetCardAuthorizationQuery = `
		UPDATE checkouts SET card_authorization_id = $2
		WHERE id = $1 AND status = 1 AND (card_authorization_id IS NULL OR card_authorization_id = $2)
	`
	checkoutCommitQuery = `
		UPDATE checkouts SET status = 2, commit_at = $2
		WHERE id = $1 AND status = 1 AND hold_expire_at > $2
	`
	checkoutAddSoldQuery = `UPDATE ticket_types SET sold_count = sold_count + $2 WHERE id = $1`
	// checkoutCommitSoldQuery adds a stock checkout's count ($2) to the sold
	// count and, when that sells the ticket type out for the first time, sets
	// its resale open time to the commit time ($3).
	checkoutCommitSoldQuery = `
		UPDATE ticket_types SET
			sold_count = sold_count + $2,
			resale_open_at = CASE
				WHEN resale_open_at IS NULL AND sold_count + $2 = quantity THEN $3
				ELSE resale_open_at
			END
		WHERE id = $1
	`
	// checkoutLockTicketTypeQuery takes the ticket type's row lock before a
	// checkout's sold count change.
	checkoutLockTicketTypeQuery = `SELECT 1 FROM ticket_types WHERE id = $1 FOR UPDATE`
	checkoutReleaseQuery        = `
		UPDATE checkouts SET status = 4
		WHERE id = $1 AND status = 1 AND hold_expire_at <= $2
	`
	checkoutRevertCommitQuery = `
		UPDATE checkouts SET status = 5
		WHERE id = $1 AND status = 2 AND capture_at IS NULL
	`
	checkoutRecordCaptureQuery = `
		UPDATE checkouts SET capture_at = $2
		WHERE id = $1 AND status = 2 AND capture_at IS NULL
	`
	checkoutRecordCardAuthorizationReleaseQuery = `
		UPDATE checkouts SET card_authorization_release_at = $2
		WHERE id = $1 AND status IN (4, 5) AND card_authorization_id IS NOT NULL AND card_authorization_release_at IS NULL
	`
	checkoutListDueQuery = `
		SELECT ` + checkoutColumns + ` FROM checkouts
		WHERE (status = 1 AND hold_expire_at <= $1)
		   OR (status IN (4, 5) AND card_authorization_id IS NOT NULL AND card_authorization_release_at IS NULL)
		   OR (status = 2 AND commit_at < $2)
		ORDER BY hold_expire_at, id
	`
	// checkoutAdvisoryLockQuery takes a transaction-scoped advisory lock
	// keyed on the checkout id; it is released at commit or rollback, and
	// when the session ends.
	checkoutAdvisoryLockQuery = `SELECT pg_advisory_xact_lock(hashtextextended('checkout:' || $1, 0))`
)

// NewCheckoutRepository creates a new checkout repository instance.
func NewCheckoutRepository(db *Database) *CheckoutRepository {
	return &CheckoutRepository{db: db}
}

// GetOrCreateHeld returns the user's holding checkout of the same kind with
// the same count, or creates a new Held one under the ticket type's row lock:
// a stock checkout when resaleDeadline is nil, otherwise a resale checkout
// that holds the oldest free listings.
func (r *CheckoutRepository) GetOrCreateHeld(ctx context.Context, ticketTypeID entity.TicketTypeID, userID entity.UserID, count int, now time.Time, resaleDeadline *time.Time) (*entity.Checkout, error) {
	resale := resaleDeadline != nil
	attrs := []slog.Attr{slog.String("ticket_type_id", string(ticketTypeID)), slog.String("user_id", string(userID)), slog.Bool("resale", resale)}
	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return nil, toAppErr(err, "failed to begin checkout start", attrs...)
	}
	// Rollback is a no-op after a successful Commit.
	defer func() { _ = tx.Rollback(ctx) }()

	_, ticketType, err := lockTicketType(ctx, tx, ticketTypeID, now)
	if err != nil {
		return nil, toAppErr(err, "failed to lock ticket type", attrs...)
	}

	existing, err := scanCheckout(tx.QueryRow(ctx, checkoutHeldOfUserQuery, string(ticketTypeID), string(userID)))
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, toAppErr(err, "failed to read the user's held checkout", attrs...)
	}
	holding := existing != nil && existing.IsHoldingAt(now)
	if holding && existing.Resale == resale && existing.TicketCount == count {
		return existing, nil
	}

	var listings []*entity.ResaleListing
	if resale {
		// The user's own holding checkout counts as free: it is replaced.
		var replaced any
		if holding {
			replaced = string(existing.ID)
		}
		listings, err = r.freeListings(ctx, tx, ticketTypeID, userID, count, now, replaced)
		if err != nil {
			return nil, toAppErr(err, "failed to pick free listings", attrs...)
		}
		if len(listings) < count {
			return nil, apperr.New(codes.ResourceExhausted, "not enough free resale listings", attrs...)
		}
	} else {
		// The user's own holding stock checkout counts as free: it is replaced.
		free := ticketType.Remaining()
		if holding && !existing.Resale {
			free += existing.TicketCount
		}
		if free < count {
			return nil, apperr.New(codes.ResourceExhausted, "not enough tickets remain", attrs...)
		}
	}
	var held int
	if err := tx.QueryRow(ctx, checkoutUserTicketsQuery, string(ticketTypeID), string(userID)).Scan(&held); err != nil {
		return nil, toAppErr(err, "failed to count the user's tickets", attrs...)
	}
	if !ticketType.FitsPerAccountLimit(held, count) {
		return nil, apperr.New(codes.FailedPrecondition, "the per-account limit would be exceeded", attrs...)
	}

	if existing != nil {
		next := entity.CheckoutStatusExpired
		if holding {
			next = entity.CheckoutStatusReleased
		}
		if _, err := tx.Exec(ctx, checkoutEndHeldQuery, string(existing.ID), int16(next)); err != nil {
			return nil, toAppErr(err, "failed to end the replaced checkout", attrs...)
		}
	}

	c := entity.NewCheckout(ticketType, userID, count, now)
	if resale {
		c = entity.NewResaleCheckout(ticketType, userID, count, now, *resaleDeadline)
	}
	if err := c.ValidateResaleListings(listings); err != nil {
		return nil, apperr.Wrap(err, codes.Internal, "picked listings break the resale checkout rule", attrs...)
	}
	if _, err := tx.Exec(ctx, checkoutInsertQuery,
		string(c.ID), string(c.TicketTypeID), string(c.UserID), c.TicketCount, c.Amount,
		c.HoldExpireTime, c.Resale,
	); err != nil {
		return nil, toAppErr(err, "failed to insert checkout", attrs...)
	}
	if resale {
		ids := make([]string, len(listings))
		for i, l := range listings {
			ids[i] = string(l.ID)
		}
		if _, err := tx.Exec(ctx, checkoutHoldListingsQuery, ids, string(c.ID)); err != nil {
			return nil, toAppErr(err, "failed to hold resale listings", attrs...)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, toAppErr(err, "failed to commit checkout start", attrs...)
	}
	return c, nil
}

// freeListings locks and returns, oldest first, up to count listings of the
// ticket type that are free at now for the user, counting those held by the
// user's own holding checkout (replaced, nil when none) as free.
func (r *CheckoutRepository) freeListings(ctx context.Context, tx pgx.Tx, ticketTypeID entity.TicketTypeID, userID entity.UserID, count int, now time.Time, replaced any) ([]*entity.ResaleListing, error) {
	rows, err := tx.Query(ctx, checkoutFreeListingsQuery, string(ticketTypeID), string(userID), now, count, replaced)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (*entity.ResaleListing, error) {
		var id, owner string
		if err := row.Scan(&id, &owner); err != nil {
			return nil, err
		}
		return &entity.ResaleListing{ID: entity.ResaleListingID(id), TicketTypeID: ticketTypeID, UserID: entity.UserID(owner)}, nil
	})
}

// Get returns the checkout with the given id.
func (r *CheckoutRepository) Get(ctx context.Context, id entity.CheckoutID) (*entity.Checkout, error) {
	c, err := scanCheckout(r.db.Pool.QueryRow(ctx, checkoutGetQuery, string(id)))
	if err != nil {
		return nil, toAppErr(err, "failed to get checkout", slog.String("checkout_id", string(id)))
	}
	return c, nil
}

// GetByCardAuthorizationID returns the checkout that references the given card
// authorization.
func (r *CheckoutRepository) GetByCardAuthorizationID(ctx context.Context, cardAuthorizationID string) (*entity.Checkout, error) {
	c, err := scanCheckout(r.db.Pool.QueryRow(ctx, checkoutGetByCardAuthorizationIDQuery, cardAuthorizationID))
	if err != nil {
		return nil, toAppErr(err, "failed to get checkout by authorization", slog.String("card_authorization_id", cardAuthorizationID))
	}
	return c, nil
}

// SetCardAuthorization stores the card authorization ID of a Held checkout; it
// changes no other attribute.
func (r *CheckoutRepository) SetCardAuthorization(ctx context.Context, id entity.CheckoutID, cardAuthorizationID string) error {
	tag, err := r.db.Pool.Exec(ctx, checkoutSetCardAuthorizationQuery, string(id), cardAuthorizationID)
	if err != nil {
		return toAppErr(err, "failed to set checkout authorization", slog.String("checkout_id", string(id)))
	}
	if tag.RowsAffected() > 0 {
		return nil
	}
	if _, err := r.Get(ctx, id); err != nil {
		return err
	}
	return apperr.New(codes.FailedPrecondition, "checkout is not Held or has another card authorization",
		slog.String("checkout_id", string(id)))
}

// Commit makes a holding checkout Committed and, for a stock checkout, adds
// its tickets to the ticket type's sold count and opens resale when that
// sells it out, under the ticket type's row lock.
func (r *CheckoutRepository) Commit(ctx context.Context, id entity.CheckoutID, now time.Time) (entity.CommitOutcome, error) {
	attrs := slog.String("checkout_id", string(id))
	current, err := r.Get(ctx, id)
	if err != nil {
		return 0, err
	}
	if current.Status == entity.CheckoutStatusCommitted || current.Status == entity.CheckoutStatusCompleted {
		return entity.CommitOutcomeCommitted, nil
	}
	if current.Status != entity.CheckoutStatusHeld {
		return entity.CommitOutcomeNotHeld, nil
	}

	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return 0, toAppErr(err, "failed to begin checkout commit", attrs)
	}
	// Rollback is a no-op after a successful Commit.
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, checkoutLockTicketTypeQuery, string(current.TicketTypeID)); err != nil {
		return 0, toAppErr(err, "failed to lock ticket type", attrs)
	}
	tag, err := tx.Exec(ctx, checkoutCommitQuery, string(id), now)
	if err != nil {
		return 0, toAppErr(err, "failed to commit checkout", attrs)
	}
	if tag.RowsAffected() == 0 {
		// Not holding any more: lapsed, released, or committed by a
		// concurrent caller.
		_ = tx.Rollback(ctx)
		after, err := r.Get(ctx, id)
		if err != nil {
			return 0, err
		}
		if after.Status == entity.CheckoutStatusCommitted || after.Status == entity.CheckoutStatusCompleted {
			return entity.CommitOutcomeCommitted, nil
		}
		return entity.CommitOutcomeNotHeld, nil
	}
	// A resale checkout's listings stay held by it; the sold count is the
	// stock's only.
	if !current.Resale {
		if _, err := tx.Exec(ctx, checkoutCommitSoldQuery, string(current.TicketTypeID), current.TicketCount, now); err != nil {
			return 0, toAppErr(err, "failed to add sold tickets", attrs)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, toAppErr(err, "failed to commit checkout commit", attrs)
	}
	return entity.CommitOutcomeCommitted, nil
}

// Release makes a lapsed Held checkout Expired and reports whether it
// changed it.
func (r *CheckoutRepository) Release(ctx context.Context, id entity.CheckoutID, now time.Time) (bool, error) {
	tag, err := r.db.Pool.Exec(ctx, checkoutReleaseQuery, string(id), now)
	if err != nil {
		return false, toAppErr(err, "failed to release checkout", slog.String("checkout_id", string(id)))
	}
	if tag.RowsAffected() > 0 {
		return true, nil
	}
	if _, err := r.Get(ctx, id); err != nil {
		return false, err
	}
	return false, nil
}

// RevertCommit makes a Committed, uncharged checkout Released and, for a stock
// checkout, takes its tickets off the ticket type's sold count, under the
// ticket type's row lock.
func (r *CheckoutRepository) RevertCommit(ctx context.Context, id entity.CheckoutID) error {
	attrs := slog.String("checkout_id", string(id))
	current, err := r.Get(ctx, id)
	if err != nil {
		return err
	}
	if current.Status != entity.CheckoutStatusCommitted {
		return nil
	}
	if current.IsCharged() {
		return apperr.New(codes.FailedPrecondition, "a charged checkout cannot be reverted", attrs)
	}

	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return toAppErr(err, "failed to begin checkout revert", attrs)
	}
	// Rollback is a no-op after a successful Commit.
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, checkoutLockTicketTypeQuery, string(current.TicketTypeID)); err != nil {
		return toAppErr(err, "failed to lock ticket type", attrs)
	}
	tag, err := tx.Exec(ctx, checkoutRevertCommitQuery, string(id))
	if err != nil {
		return toAppErr(err, "failed to revert checkout commit", attrs)
	}
	if tag.RowsAffected() == 0 {
		// Changed meanwhile: charged, completed or reverted by another caller.
		_ = tx.Rollback(ctx)
		after, err := r.Get(ctx, id)
		if err != nil {
			return err
		}
		if after.IsCharged() {
			return apperr.New(codes.FailedPrecondition, "a charged checkout cannot be reverted", attrs)
		}
		return nil
	}
	// A resale checkout's listings are free again once it is Released; the
	// resale open time stays as it is.
	if !current.Resale {
		if _, err := tx.Exec(ctx, checkoutAddSoldQuery, string(current.TicketTypeID), -current.TicketCount); err != nil {
			return toAppErr(err, "failed to take sold tickets back", attrs)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return toAppErr(err, "failed to commit checkout revert", attrs)
	}
	return nil
}

// RecordCapture stoc the capture time of a Committed checkout once.
func (r *CheckoutRepository) RecordCapture(ctx context.Context, id entity.CheckoutID, at time.Time) error {
	tag, err := r.db.Pool.Exec(ctx, checkoutRecordCaptureQuery, string(id), at)
	if err != nil {
		return toAppErr(err, "failed to record checkout capture", slog.String("checkout_id", string(id)))
	}
	if tag.RowsAffected() > 0 {
		return nil
	}
	current, err := r.Get(ctx, id)
	if err != nil {
		return err
	}
	if current.IsCharged() {
		return nil
	}
	return apperr.New(codes.FailedPrecondition, "checkout is not Committed", slog.String("checkout_id", string(id)))
}

// RecordCardAuthorizationRelease sets the authorization release time of an
// Expired or Released checkout with a card authorization, once.
func (r *CheckoutRepository) RecordCardAuthorizationRelease(ctx context.Context, id entity.CheckoutID, at time.Time) error {
	tag, err := r.db.Pool.Exec(ctx, checkoutRecordCardAuthorizationReleaseQuery, string(id), at)
	if err != nil {
		return toAppErr(err, "failed to record authorization release", slog.String("checkout_id", string(id)))
	}
	if tag.RowsAffected() > 0 {
		return nil
	}
	current, err := r.Get(ctx, id)
	if err != nil {
		return err
	}
	ended := current.Status == entity.CheckoutStatusExpired || current.Status == entity.CheckoutStatusReleased
	if ended && current.CardAuthorizationReleaseTime != nil {
		return nil
	}
	return apperr.New(codes.FailedPrecondition, "checkout is not an ended checkout with a card authorization",
		slog.String("checkout_id", string(id)))
}

// ListDue returns the lapsed holds, the card authorizations to give back and the
// stalled commits, oldest hold first.
func (r *CheckoutRepository) ListDue(ctx context.Context, now time.Time) ([]*entity.Checkout, error) {
	rows, err := r.db.Pool.Query(ctx, checkoutListDueQuery, now, now.Add(-entity.CheckoutStalledCommitAge))
	if err != nil {
		return nil, toAppErr(err, "failed to list due checkouts")
	}
	defer rows.Close()

	due := []*entity.Checkout{}
	for rows.Next() {
		c, err := scanCheckout(rows)
		if err != nil {
			return nil, toAppErr(err, "failed to scan due checkout")
		}
		due = append(due, c)
	}
	if err := rows.Err(); err != nil {
		return nil, toAppErr(err, "failed to iterate due checkouts")
	}
	return due, nil
}

// Serialize runs fn while holding an advisory lock keyed on the checkout,
// taken in a transaction on its own connection. fn's own writes run on other
// connections, so they never wait for this lock.
func (r *CheckoutRepository) Serialize(ctx context.Context, id entity.CheckoutID, fn func(ctx context.Context) error) error {
	attrs := slog.String("checkout_id", string(id))
	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return toAppErr(err, "failed to begin checkout lock", attrs)
	}
	// Rollback releases the lock; it is the only way the transaction ends.
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	if _, err := tx.Exec(ctx, checkoutAdvisoryLockQuery, string(id)); err != nil {
		return toAppErr(err, "failed to lock checkout", attrs)
	}
	return fn(ctx)
}

// scanCheckout maps one row selected with checkoutColumns.
func scanCheckout(row pgx.Row) (*entity.Checkout, error) {
	var (
		c                                      entity.Checkout
		id, ticketTypeID, userID               string
		cardAuthorizationID                    sql.NullString
		authReleasedAt, committedAt, captureAt sql.NullTime
		status                                 int16
	)
	if err := row.Scan(
		&id, &ticketTypeID, &userID, &c.TicketCount, &c.Amount,
		&cardAuthorizationID, &authReleasedAt, &status, &c.HoldExpireTime,
		&committedAt, &captureAt, &c.Resale,
	); err != nil {
		return nil, err
	}
	c.ID = entity.CheckoutID(id)
	c.TicketTypeID = entity.TicketTypeID(ticketTypeID)
	c.UserID = entity.UserID(userID)
	c.Status = entity.CheckoutStatus(status)
	if cardAuthorizationID.Valid {
		c.CardAuthorizationID = cardAuthorizationID.String
	}
	c.CardAuthorizationReleaseTime = optionalTime(authReleasedAt)
	c.CommitTime = optionalTime(committedAt)
	c.CaptureTime = optionalTime(captureAt)
	return &c, nil
}

// optionalTime returns a pointer to the time of a valid sql.NullTime, or nil.
func optionalTime(t sql.NullTime) *time.Time {
	if !t.Valid {
		return nil
	}
	v := t.Time
	return &v
}
