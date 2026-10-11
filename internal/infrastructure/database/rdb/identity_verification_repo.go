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

// IdentityVerificationRepository implements entity.IdentityVerificationRepository for
// PostgreSQL.
type IdentityVerificationRepository struct {
	db *Database
}

// NewIdentityVerificationRepository creates a new PostgreSQL-backed
// IdentityVerificationRepository.
func NewIdentityVerificationRepository(db *Database) *IdentityVerificationRepository {
	return &IdentityVerificationRepository{db: db}
}

const (
	insertIdentityVerificationQuery = `
		INSERT INTO identity_verifications (id, user_id, method, provider_user_id, dedupe_strength, verify_at, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, user_id, method, provider_user_id, dedupe_strength, verify_at, status
	`

	// getIdentityVerificationByUserIDQuery returns the most recent row for the user,
	// preferring ACTIVE rows (status=1) over NEEDS_REVERIFICATION (status=2).
	getIdentityVerificationByUserIDQuery = `
		SELECT id, user_id, method, provider_user_id, dedupe_strength, verify_at, status
		FROM identity_verifications
		WHERE user_id = $1
		ORDER BY status ASC, verify_at DESC
		LIMIT 1
	`

	// getIdentityVerificationByProviderUserIDQuery returns the single ACTIVE row
	// for the given provider_user_id, enforced by the partial unique index.
	getIdentityVerificationByProviderUserIDQuery = `
		SELECT id, user_id, method, provider_user_id, dedupe_strength, verify_at, status
		FROM identity_verifications
		WHERE provider_user_id = $1
		  AND status = 1
		LIMIT 1
	`

	updateIdentityVerificationStatusQuery = `
		UPDATE identity_verifications SET status = $2 WHERE id = $1
	`

	deleteIdentityVerificationQuery = `
		DELETE FROM identity_verifications WHERE id = $1
	`
)

// scanIdentityVerification extracts a IdentityVerification from a pgx row.
func scanIdentityVerification(scan func(dest ...any) error) (*entity.IdentityVerification, error) {
	var vi entity.IdentityVerification
	var method, dedupeStrength, status int16
	var verifiedAt time.Time

	if err := scan(
		&vi.ID,
		&vi.UserID,
		&method,
		&vi.ProviderUserID,
		&dedupeStrength,
		&verifiedAt,
		&status,
	); err != nil {
		return nil, err
	}

	vi.Method = entity.VerificationMethod(method)
	vi.DedupeStrength = entity.DedupeStrength(dedupeStrength)
	vi.VerifyTime = verifiedAt.UTC()
	vi.Status = entity.VerificationStatus(status)
	return &vi, nil
}

// Create inserts a new IdentityVerification. The partial unique index on
// (provider_user_id WHERE status=1) enforces the 1-person-1-active-account
// invariant; a violation is surfaced as AlreadyExists by toAppErr.
func (r *IdentityVerificationRepository) Create(ctx context.Context, vi *entity.IdentityVerification) (*entity.IdentityVerification, error) {
	if vi.ID == "" {
		vi.ID = entity.NewID()
	}

	row := r.db.Pool.QueryRow(ctx, insertIdentityVerificationQuery,
		vi.ID,
		vi.UserID,
		int16(vi.Method),
		vi.ProviderUserID,
		int16(vi.DedupeStrength),
		vi.VerifyTime.UTC(),
		int16(vi.Status),
	)
	created, err := scanIdentityVerification(row.Scan)
	if err != nil {
		return nil, toAppErr(err, "failed to insert identity verification",
			slog.String("user_id", vi.UserID))
	}

	r.db.logger.Info(ctx, "identity verification created",
		slog.String("entityType", "identity_verification"),
		slog.String("id", created.ID),
		slog.String("user_id", created.UserID),
	)
	return created, nil
}

// GetByUserID returns the most recent IdentityVerification for the user, preferring
// ACTIVE rows. Returns NotFound when no record exists.
func (r *IdentityVerificationRepository) GetByUserID(ctx context.Context, userID string) (*entity.IdentityVerification, error) {
	row := r.db.Pool.QueryRow(ctx, getIdentityVerificationByUserIDQuery, userID)
	vi, err := scanIdentityVerification(row.Scan)
	if err != nil {
		return nil, toAppErr(err, "failed to get identity verification by user_id",
			slog.String("user_id", userID))
	}
	return vi, nil
}

// GetByProviderUserID returns the ACTIVE IdentityVerification keyed on the Pocket
// Sign person key. Returns NotFound when no active record exists for the given
// provider_user_id.
func (r *IdentityVerificationRepository) GetByProviderUserID(ctx context.Context, providerUserID string) (*entity.IdentityVerification, error) {
	row := r.db.Pool.QueryRow(ctx, getIdentityVerificationByProviderUserIDQuery, providerUserID)
	vi, err := scanIdentityVerification(row.Scan)
	if err != nil {
		if pgxIsNoRows(err) {
			return nil, apperr.New(codes.NotFound, "no active identity verification for provider_user_id")
		}
		return nil, toAppErr(err, "failed to get identity verification by provider_user_id")
	}
	return vi, nil
}

// UpdateStatus changes the status of the record identified by id. Returns
// NotFound when no row matches.
func (r *IdentityVerificationRepository) UpdateStatus(ctx context.Context, id string, status entity.VerificationStatus) error {
	tag, err := r.db.Pool.Exec(ctx, updateIdentityVerificationStatusQuery, id, int16(status))
	if err != nil {
		return toAppErr(err, "failed to update identity verification status", slog.String("id", id))
	}
	if tag.RowsAffected() == 0 {
		return apperr.New(codes.NotFound, "identity verification not found")
	}
	return nil
}

// Delete removes the IdentityVerification record. Returns NotFound when no row
// matches.
func (r *IdentityVerificationRepository) Delete(ctx context.Context, id string) error {
	tag, err := r.db.Pool.Exec(ctx, deleteIdentityVerificationQuery, id)
	if err != nil {
		return toAppErr(err, "failed to delete identity verification", slog.String("id", id))
	}
	if tag.RowsAffected() == 0 {
		return apperr.New(codes.NotFound, "identity verification not found")
	}
	return nil
}

// pgxIsNoRows is a helper to check for pgx.ErrNoRows without importing pgx in
// the caller (toAppErr already handles it for scan errors).
func pgxIsNoRows(err error) bool {
	return err == pgx.ErrNoRows
}
