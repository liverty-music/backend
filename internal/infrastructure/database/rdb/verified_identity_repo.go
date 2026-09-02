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

// VerifiedIdentityRepository implements entity.VerifiedIdentityRepository for
// PostgreSQL.
type VerifiedIdentityRepository struct {
	db *Database
}

// NewVerifiedIdentityRepository creates a new PostgreSQL-backed
// VerifiedIdentityRepository.
func NewVerifiedIdentityRepository(db *Database) *VerifiedIdentityRepository {
	return &VerifiedIdentityRepository{db: db}
}

const (
	insertVerifiedIdentityQuery = `
		INSERT INTO verified_identities (id, user_id, method, pocket_sign_user_id, dedupe_strength, verified_at, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, user_id, method, pocket_sign_user_id, dedupe_strength, verified_at, status
	`

	// getVerifiedIdentityByUserIDQuery returns the most recent row for the user,
	// preferring ACTIVE rows (status=1) over NEEDS_REVERIFICATION (status=2).
	getVerifiedIdentityByUserIDQuery = `
		SELECT id, user_id, method, pocket_sign_user_id, dedupe_strength, verified_at, status
		FROM verified_identities
		WHERE user_id = $1
		ORDER BY status ASC, verified_at DESC
		LIMIT 1
	`

	// getVerifiedIdentityByPocketSignUserIDQuery returns the single ACTIVE row
	// for the given pocket_sign_user_id, enforced by the partial unique index.
	getVerifiedIdentityByPocketSignUserIDQuery = `
		SELECT id, user_id, method, pocket_sign_user_id, dedupe_strength, verified_at, status
		FROM verified_identities
		WHERE pocket_sign_user_id = $1
		  AND status = 1
		LIMIT 1
	`

	updateVerifiedIdentityStatusQuery = `
		UPDATE verified_identities SET status = $2 WHERE id = $1
	`

	deleteVerifiedIdentityQuery = `
		DELETE FROM verified_identities WHERE id = $1
	`
)

// scanVerifiedIdentity extracts a VerifiedIdentity from a pgx row.
func scanVerifiedIdentity(scan func(dest ...any) error) (*entity.VerifiedIdentity, error) {
	var vi entity.VerifiedIdentity
	var method, dedupeStrength, status int16
	var verifiedAt time.Time

	if err := scan(
		&vi.ID,
		&vi.UserID,
		&method,
		&vi.PocketSignUserID,
		&dedupeStrength,
		&verifiedAt,
		&status,
	); err != nil {
		return nil, err
	}

	vi.Method = entity.VerificationMethod(method)
	vi.DedupeStrength = entity.DedupeStrength(dedupeStrength)
	vi.VerifiedTime = verifiedAt.UTC()
	vi.Status = entity.VerificationStatus(status)
	return &vi, nil
}

// Create inserts a new VerifiedIdentity. The partial unique index on
// (pocket_sign_user_id WHERE status=1) enforces the 1-person-1-active-account
// invariant; a violation is surfaced as AlreadyExists by toAppErr.
func (r *VerifiedIdentityRepository) Create(ctx context.Context, vi *entity.VerifiedIdentity) (*entity.VerifiedIdentity, error) {
	if vi.ID == "" {
		vi.ID = entity.NewID()
	}

	row := r.db.Pool.QueryRow(ctx, insertVerifiedIdentityQuery,
		vi.ID,
		vi.UserID,
		int16(vi.Method),
		vi.PocketSignUserID,
		int16(vi.DedupeStrength),
		vi.VerifiedTime.UTC(),
		int16(vi.Status),
	)
	created, err := scanVerifiedIdentity(row.Scan)
	if err != nil {
		return nil, toAppErr(err, "failed to insert verified identity",
			slog.String("user_id", vi.UserID))
	}

	r.db.logger.Info(ctx, "verified identity created",
		slog.String("entityType", "verified_identity"),
		slog.String("id", created.ID),
		slog.String("user_id", created.UserID),
	)
	return created, nil
}

// GetByUserID returns the most recent VerifiedIdentity for the user, preferring
// ACTIVE rows. Returns NotFound when no record exists.
func (r *VerifiedIdentityRepository) GetByUserID(ctx context.Context, userID string) (*entity.VerifiedIdentity, error) {
	row := r.db.Pool.QueryRow(ctx, getVerifiedIdentityByUserIDQuery, userID)
	vi, err := scanVerifiedIdentity(row.Scan)
	if err != nil {
		return nil, toAppErr(err, "failed to get verified identity by user_id",
			slog.String("user_id", userID))
	}
	return vi, nil
}

// GetByPocketSignUserID returns the ACTIVE VerifiedIdentity keyed on the Pocket
// Sign person key. Returns NotFound when no active record exists for the given
// pocket_sign_user_id.
func (r *VerifiedIdentityRepository) GetByPocketSignUserID(ctx context.Context, pocketSignUserID string) (*entity.VerifiedIdentity, error) {
	row := r.db.Pool.QueryRow(ctx, getVerifiedIdentityByPocketSignUserIDQuery, pocketSignUserID)
	vi, err := scanVerifiedIdentity(row.Scan)
	if err != nil {
		if pgxIsNoRows(err) {
			return nil, apperr.New(codes.NotFound, "no active verified identity for pocket_sign_user_id")
		}
		return nil, toAppErr(err, "failed to get verified identity by pocket_sign_user_id")
	}
	return vi, nil
}

// UpdateStatus changes the status of the record identified by id. Returns
// NotFound when no row matches.
func (r *VerifiedIdentityRepository) UpdateStatus(ctx context.Context, id string, status entity.VerificationStatus) error {
	tag, err := r.db.Pool.Exec(ctx, updateVerifiedIdentityStatusQuery, id, int16(status))
	if err != nil {
		return toAppErr(err, "failed to update verified identity status", slog.String("id", id))
	}
	if tag.RowsAffected() == 0 {
		return apperr.New(codes.NotFound, "verified identity not found")
	}
	return nil
}

// Delete removes the VerifiedIdentity record. Returns NotFound when no row
// matches.
func (r *VerifiedIdentityRepository) Delete(ctx context.Context, id string) error {
	tag, err := r.db.Pool.Exec(ctx, deleteVerifiedIdentityQuery, id)
	if err != nil {
		return toAppErr(err, "failed to delete verified identity", slog.String("id", id))
	}
	if tag.RowsAffected() == 0 {
		return apperr.New(codes.NotFound, "verified identity not found")
	}
	return nil
}

// pgxIsNoRows is a helper to check for pgx.ErrNoRows without importing pgx in
// the caller (toAppErr already handles it for scan errors).
func pgxIsNoRows(err error) bool {
	return err == pgx.ErrNoRows
}
