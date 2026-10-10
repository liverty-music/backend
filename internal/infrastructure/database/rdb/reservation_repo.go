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

// ReservationRepository implements entity.ReservationRepository for
// PostgreSQL. Every write that changes a sale's sold count, and every Start,
// takes the sale's row lock first and the reservation row second, so the
// writes of one sale never deadlock.
type ReservationRepository struct {
	db *Database
}

// Compile-time interface compliance check.
var _ entity.ReservationRepository = (*ReservationRepository)(nil)

const reservationColumns = `
	id, ticket_sale_id, user_id, ticket_count, amount, holder_full_name, holder_phone_number,
	authorization_ref, authorization_released_at, status, hold_expire_at,
	committed_at, capture_at
`

const (
	reservationGetQuery                   = `SELECT ` + reservationColumns + ` FROM reservations WHERE id = $1`
	reservationGetByAuthorizationRefQuery = `SELECT ` + reservationColumns + ` FROM reservations WHERE authorization_ref = $1`
	// reservationHeldOfUserQuery locks the user's Held reservation of the sale
	// (at most one, by the partial unique index), lapsed or not.
	reservationHeldOfUserQuery = `
		SELECT ` + reservationColumns + ` FROM reservations
		WHERE ticket_sale_id = $1 AND user_id = $2 AND status = 1
		FOR UPDATE
	`
	reservationUserCommittedQuery = `
		SELECT COALESCE(SUM(ticket_count), 0)::int FROM reservations
		WHERE ticket_sale_id = $1 AND user_id = $2 AND status IN (2, 3)
	`
	reservationEndHeldQuery = `UPDATE reservations SET status = $2 WHERE id = $1 AND status = 1`
	reservationInsertQuery  = `
		INSERT INTO reservations (
			id, ticket_sale_id, user_id, ticket_count, amount, status, hold_expire_at
		) VALUES ($1, $2, $3, $4, $5, 1, $6)
	`
	reservationSetAuthorizationQuery = `
		UPDATE reservations
		SET holder_full_name = $2, holder_phone_number = $3, authorization_ref = $4
		WHERE id = $1 AND status = 1 AND (authorization_ref IS NULL OR authorization_ref = $4)
	`
	reservationCommitQuery = `
		UPDATE reservations SET status = 2, committed_at = $2
		WHERE id = $1 AND status = 1 AND hold_expire_at > $2
	`
	reservationAddSoldQuery = `UPDATE ticket_sales SET sold_count = sold_count + $2 WHERE id = $1`
	reservationReleaseQuery = `
		UPDATE reservations SET status = 4
		WHERE id = $1 AND status = 1 AND hold_expire_at <= $2
	`
	reservationRevertCommitQuery = `
		UPDATE reservations SET status = 5
		WHERE id = $1 AND status = 2 AND capture_at IS NULL
	`
	reservationRecordCaptureQuery = `
		UPDATE reservations SET capture_at = $2
		WHERE id = $1 AND status = 2 AND capture_at IS NULL
	`
	reservationRecordAuthorizationReleaseQuery = `
		UPDATE reservations SET authorization_released_at = $2
		WHERE id = $1 AND status IN (4, 5) AND authorization_ref IS NOT NULL AND authorization_released_at IS NULL
	`
	reservationListDueQuery = `
		SELECT ` + reservationColumns + ` FROM reservations
		WHERE (status = 1 AND hold_expire_at <= $1)
		   OR (status IN (4, 5) AND authorization_ref IS NOT NULL AND authorization_released_at IS NULL)
		   OR (status = 2 AND committed_at < $2)
		ORDER BY hold_expire_at, id
	`
	// reservationAdvisoryLockQuery takes a transaction-scoped advisory lock
	// keyed on the reservation id; it is released at commit or rollback, and
	// when the session ends.
	reservationAdvisoryLockQuery = `SELECT pg_advisory_xact_lock(hashtextextended('reservation:' || $1, 0))`
)

// NewReservationRepository creates a new reservation repository instance.
func NewReservationRepository(db *Database) *ReservationRepository {
	return &ReservationRepository{db: db}
}

// GetOrCreateHeld returns the user's holding reservation with the same count,
// or creates a new Held one under the sale's row lock.
func (r *ReservationRepository) GetOrCreateHeld(ctx context.Context, saleID entity.TicketSaleID, userID entity.UserID, count int, now time.Time) (*entity.Reservation, error) {
	attrs := []slog.Attr{slog.String("ticket_sale_id", string(saleID)), slog.String("user_id", string(userID))}
	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return nil, toAppErr(err, "failed to begin reservation start", attrs...)
	}
	// Rollback is a no-op after a successful Commit.
	defer func() { _ = tx.Rollback(ctx) }()

	sale, err := lockTicketSale(ctx, tx, saleID, now)
	if err != nil {
		return nil, toAppErr(err, "failed to lock ticket sale", attrs...)
	}

	existing, err := scanReservation(tx.QueryRow(ctx, reservationHeldOfUserQuery, string(saleID), string(userID)))
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, toAppErr(err, "failed to read the user's held reservation", attrs...)
	}
	holding := existing != nil && existing.IsHoldingAt(now)
	if holding && existing.TicketCount == count {
		return existing, nil
	}

	// The user's own holding reservation counts as free: it is replaced.
	free := sale.Remaining()
	if holding {
		free += existing.TicketCount
	}
	if free < count {
		return nil, apperr.New(codes.ResourceExhausted, "not enough tickets remain", attrs...)
	}
	var committed int
	if err := tx.QueryRow(ctx, reservationUserCommittedQuery, string(saleID), string(userID)).Scan(&committed); err != nil {
		return nil, toAppErr(err, "failed to count the user's tickets", attrs...)
	}
	if committed+count > sale.PerAccountLimit {
		return nil, apperr.New(codes.FailedPrecondition, "the per-account limit would be exceeded", attrs...)
	}

	if existing != nil {
		next := entity.ReservationStatusExpired
		if holding {
			next = entity.ReservationStatusReleased
		}
		if _, err := tx.Exec(ctx, reservationEndHeldQuery, string(existing.ID), int16(next)); err != nil {
			return nil, toAppErr(err, "failed to end the replaced reservation", attrs...)
		}
	}

	res := entity.NewReservation(sale, userID, count, now)
	if _, err := tx.Exec(ctx, reservationInsertQuery,
		string(res.ID), string(res.TicketSaleID), string(res.UserID), res.TicketCount, res.Amount,
		res.HoldExpireTime,
	); err != nil {
		return nil, toAppErr(err, "failed to insert reservation", attrs...)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, toAppErr(err, "failed to commit reservation start", attrs...)
	}
	return res, nil
}

// Get returns the reservation with the given id.
func (r *ReservationRepository) Get(ctx context.Context, id entity.ReservationID) (*entity.Reservation, error) {
	res, err := scanReservation(r.db.Pool.QueryRow(ctx, reservationGetQuery, string(id)))
	if err != nil {
		return nil, toAppErr(err, "failed to get reservation", slog.String("reservation_id", string(id)))
	}
	return res, nil
}

// GetByAuthorizationRef returns the reservation whose card hold has the given
// reference.
func (r *ReservationRepository) GetByAuthorizationRef(ctx context.Context, authorizationRef string) (*entity.Reservation, error) {
	res, err := scanReservation(r.db.Pool.QueryRow(ctx, reservationGetByAuthorizationRefQuery, authorizationRef))
	if err != nil {
		return nil, toAppErr(err, "failed to get reservation by authorization", slog.String("authorization_ref", authorizationRef))
	}
	return res, nil
}

// SetAuthorization stores the holder identity and the card hold reference of
// a Held reservation.
func (r *ReservationRepository) SetAuthorization(ctx context.Context, id entity.ReservationID, identity entity.HolderIdentity, authorizationRef string) error {
	if err := identity.Validate(); err != nil {
		return apperr.Wrap(err, codes.InvalidArgument, "invalid holder identity")
	}
	tag, err := r.db.Pool.Exec(ctx, reservationSetAuthorizationQuery, string(id),
		identity.FullName, identity.PhoneNumber, authorizationRef)
	if err != nil {
		return toAppErr(err, "failed to set reservation authorization", slog.String("reservation_id", string(id)))
	}
	if tag.RowsAffected() > 0 {
		return nil
	}
	if _, err := r.Get(ctx, id); err != nil {
		return err
	}
	return apperr.New(codes.FailedPrecondition, "reservation is not Held or has another card hold",
		slog.String("reservation_id", string(id)))
}

// Commit makes a holding reservation Committed and adds its tickets to the
// sale's sold count, under the sale's row lock.
func (r *ReservationRepository) Commit(ctx context.Context, id entity.ReservationID, now time.Time) (entity.CommitOutcome, error) {
	attrs := slog.String("reservation_id", string(id))
	current, err := r.Get(ctx, id)
	if err != nil {
		return 0, err
	}
	if current.Status == entity.ReservationStatusCommitted || current.Status == entity.ReservationStatusCompleted {
		return entity.CommitOutcomeCommitted, nil
	}
	if current.Status != entity.ReservationStatusHeld {
		return entity.CommitOutcomeNotHeld, nil
	}

	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return 0, toAppErr(err, "failed to begin reservation commit", attrs)
	}
	// Rollback is a no-op after a successful Commit.
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, ticketSaleLockQuery, string(current.TicketSaleID)); err != nil {
		return 0, toAppErr(err, "failed to lock ticket sale", attrs)
	}
	tag, err := tx.Exec(ctx, reservationCommitQuery, string(id), now)
	if err != nil {
		return 0, toAppErr(err, "failed to commit reservation", attrs)
	}
	if tag.RowsAffected() == 0 {
		// Not holding any more: lapsed, released, or committed by a
		// concurrent caller.
		_ = tx.Rollback(ctx)
		after, err := r.Get(ctx, id)
		if err != nil {
			return 0, err
		}
		if after.Status == entity.ReservationStatusCommitted || after.Status == entity.ReservationStatusCompleted {
			return entity.CommitOutcomeCommitted, nil
		}
		return entity.CommitOutcomeNotHeld, nil
	}
	if _, err := tx.Exec(ctx, reservationAddSoldQuery, string(current.TicketSaleID), current.TicketCount); err != nil {
		return 0, toAppErr(err, "failed to add sold tickets", attrs)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, toAppErr(err, "failed to commit reservation commit", attrs)
	}
	return entity.CommitOutcomeCommitted, nil
}

// Release makes a lapsed Held reservation Expired and reports whether it
// changed it.
func (r *ReservationRepository) Release(ctx context.Context, id entity.ReservationID, now time.Time) (bool, error) {
	tag, err := r.db.Pool.Exec(ctx, reservationReleaseQuery, string(id), now)
	if err != nil {
		return false, toAppErr(err, "failed to release reservation", slog.String("reservation_id", string(id)))
	}
	if tag.RowsAffected() > 0 {
		return true, nil
	}
	if _, err := r.Get(ctx, id); err != nil {
		return false, err
	}
	return false, nil
}

// RevertCommit makes a Committed, uncharged reservation Released and takes
// its tickets off the sale's sold count, under the sale's row lock.
func (r *ReservationRepository) RevertCommit(ctx context.Context, id entity.ReservationID) error {
	attrs := slog.String("reservation_id", string(id))
	current, err := r.Get(ctx, id)
	if err != nil {
		return err
	}
	if current.Status != entity.ReservationStatusCommitted {
		return nil
	}
	if current.IsCharged() {
		return apperr.New(codes.FailedPrecondition, "a charged reservation cannot be reverted", attrs)
	}

	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return toAppErr(err, "failed to begin reservation revert", attrs)
	}
	// Rollback is a no-op after a successful Commit.
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, ticketSaleLockQuery, string(current.TicketSaleID)); err != nil {
		return toAppErr(err, "failed to lock ticket sale", attrs)
	}
	tag, err := tx.Exec(ctx, reservationRevertCommitQuery, string(id))
	if err != nil {
		return toAppErr(err, "failed to revert reservation commit", attrs)
	}
	if tag.RowsAffected() == 0 {
		// Changed meanwhile: charged, completed or reverted by another caller.
		_ = tx.Rollback(ctx)
		after, err := r.Get(ctx, id)
		if err != nil {
			return err
		}
		if after.IsCharged() {
			return apperr.New(codes.FailedPrecondition, "a charged reservation cannot be reverted", attrs)
		}
		return nil
	}
	if _, err := tx.Exec(ctx, reservationAddSoldQuery, string(current.TicketSaleID), -current.TicketCount); err != nil {
		return toAppErr(err, "failed to take sold tickets back", attrs)
	}
	if err := tx.Commit(ctx); err != nil {
		return toAppErr(err, "failed to commit reservation revert", attrs)
	}
	return nil
}

// RecordCapture stores the capture time of a Committed reservation once.
func (r *ReservationRepository) RecordCapture(ctx context.Context, id entity.ReservationID, at time.Time) error {
	tag, err := r.db.Pool.Exec(ctx, reservationRecordCaptureQuery, string(id), at)
	if err != nil {
		return toAppErr(err, "failed to record reservation capture", slog.String("reservation_id", string(id)))
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
	return apperr.New(codes.FailedPrecondition, "reservation is not Committed", slog.String("reservation_id", string(id)))
}

// RecordAuthorizationRelease sets the authorization release time of an
// Expired or Released reservation with a card hold, once.
func (r *ReservationRepository) RecordAuthorizationRelease(ctx context.Context, id entity.ReservationID, at time.Time) error {
	tag, err := r.db.Pool.Exec(ctx, reservationRecordAuthorizationReleaseQuery, string(id), at)
	if err != nil {
		return toAppErr(err, "failed to record authorization release", slog.String("reservation_id", string(id)))
	}
	if tag.RowsAffected() > 0 {
		return nil
	}
	current, err := r.Get(ctx, id)
	if err != nil {
		return err
	}
	ended := current.Status == entity.ReservationStatusExpired || current.Status == entity.ReservationStatusReleased
	if ended && current.AuthorizationReleaseTime != nil {
		return nil
	}
	return apperr.New(codes.FailedPrecondition, "reservation is not an ended checkout with a card hold",
		slog.String("reservation_id", string(id)))
}

// ListDue returns the lapsed holds, the card holds to give back and the
// stalled commits, oldest hold first.
func (r *ReservationRepository) ListDue(ctx context.Context, now time.Time) ([]*entity.Reservation, error) {
	rows, err := r.db.Pool.Query(ctx, reservationListDueQuery, now, now.Add(-entity.ReservationStalledCommitAge))
	if err != nil {
		return nil, toAppErr(err, "failed to list due reservations")
	}
	defer rows.Close()

	due := []*entity.Reservation{}
	for rows.Next() {
		res, err := scanReservation(rows)
		if err != nil {
			return nil, toAppErr(err, "failed to scan due reservation")
		}
		due = append(due, res)
	}
	if err := rows.Err(); err != nil {
		return nil, toAppErr(err, "failed to iterate due reservations")
	}
	return due, nil
}

// Serialize runs fn while holding an advisory lock keyed on the reservation,
// taken in a transaction on its own connection. fn's own writes run on other
// connections, so they never wait for this lock.
func (r *ReservationRepository) Serialize(ctx context.Context, id entity.ReservationID, fn func(ctx context.Context) error) error {
	attrs := slog.String("reservation_id", string(id))
	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return toAppErr(err, "failed to begin reservation lock", attrs)
	}
	// Rollback releases the lock; it is the only way the transaction ends.
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	if _, err := tx.Exec(ctx, reservationAdvisoryLockQuery, string(id)); err != nil {
		return toAppErr(err, "failed to lock reservation", attrs)
	}
	return fn(ctx)
}

// scanReservation maps one row selected with reservationColumns.
func scanReservation(row pgx.Row) (*entity.Reservation, error) {
	var (
		res                                    entity.Reservation
		id, saleID, userID                     string
		holderName, holderPhone, authRef       sql.NullString
		authReleasedAt, committedAt, captureAt sql.NullTime
		status                                 int16
	)
	if err := row.Scan(
		&id, &saleID, &userID, &res.TicketCount, &res.Amount, &holderName, &holderPhone,
		&authRef, &authReleasedAt, &status, &res.HoldExpireTime,
		&committedAt, &captureAt,
	); err != nil {
		return nil, err
	}
	res.ID = entity.ReservationID(id)
	res.TicketSaleID = entity.TicketSaleID(saleID)
	res.UserID = entity.UserID(userID)
	res.Status = entity.ReservationStatus(status)
	// The holder columns are set together (CHECK constraint).
	if holderName.Valid {
		res.HolderIdentity = &entity.HolderIdentity{FullName: holderName.String, PhoneNumber: holderPhone.String}
	}
	if authRef.Valid {
		res.AuthorizationRef = authRef.String
	}
	res.AuthorizationReleaseTime = optionalTime(authReleasedAt)
	res.CommitTime = optionalTime(committedAt)
	res.CaptureTime = optionalTime(captureAt)
	return &res, nil
}

// optionalTime returns a pointer to the time of a valid sql.NullTime, or nil.
func optionalTime(t sql.NullTime) *time.Time {
	if !t.Valid {
		return nil
	}
	v := t.Time
	return &v
}
