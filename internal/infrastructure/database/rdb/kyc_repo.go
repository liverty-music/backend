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

// KycRepository implements entity.KycRepository for
// PostgreSQL.
type KycRepository struct {
	db *Database
}

// NewKycRepository creates a new PostgreSQL-backed
// KycRepository.
func NewKycRepository(db *Database) *KycRepository {
	return &KycRepository{db: db}
}

const (
	insertKycQuery = `
		INSERT INTO identity_verifications (id, user_id, method, provider_user_id, dedupe_strength, verify_at, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, user_id, method, provider_user_id, dedupe_strength, verify_at, status
	`

	// getKycByUserIDQuery returns the most recent row for the user,
	// preferring ACTIVE rows (status=1) over NEEDS_REVERIFICATION (status=2).
	getKycByUserIDQuery = `
		SELECT id, user_id, method, provider_user_id, dedupe_strength, verify_at, status
		FROM identity_verifications
		WHERE user_id = $1
		ORDER BY status ASC, verify_at DESC
		LIMIT 1
	`

	// getKycByPocketSignUserIDQuery returns the single ACTIVE row
	// for the given pocket_sign_user_id, enforced by the partial unique index.
	getKycByPocketSignUserIDQuery = `
		SELECT id, user_id, method, provider_user_id, dedupe_strength, verify_at, status
		FROM identity_verifications
		WHERE provider_user_id = $1
		  AND status = 1
		LIMIT 1
	`

	updateKycStatusQuery = `
		UPDATE identity_verifications SET status = $2 WHERE id = $1
	`

	deleteKycQuery = `
		DELETE FROM identity_verifications WHERE id = $1
	`
)

// scanKyc extracts a Kyc from a pgx row.
func scanKyc(scan func(dest ...any) error) (*entity.Kyc, error) {
	var vi entity.Kyc
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

// Create inserts a new Kyc. The partial unique index on
// (pocket_sign_user_id WHERE status=1) enforces the 1-person-1-active-account
// invariant; a violation is surfaced as AlreadyExists by toAppErr.
func (r *KycRepository) Create(ctx context.Context, vi *entity.Kyc) (*entity.Kyc, error) {
	if vi.ID == "" {
		vi.ID = entity.NewID()
	}

	row := r.db.Pool.QueryRow(ctx, insertKycQuery,
		vi.ID,
		vi.UserID,
		int16(vi.Method),
		vi.PocketSignUserID,
		int16(vi.DedupeStrength),
		vi.VerifiedTime.UTC(),
		int16(vi.Status),
	)
	created, err := scanKyc(row.Scan)
	if err != nil {
		return nil, toAppErr(err, "failed to insert Kyc",
			slog.String("user_id", vi.UserID))
	}

	r.db.logger.Info(ctx, "Kyc created",
		slog.String("entityType", "kyc"),
		slog.String("id", created.ID),
		slog.String("user_id", created.UserID),
	)
	return created, nil
}

// GetByUserID returns the most recent Kyc for the user, preferring
// ACTIVE rows. Returns NotFound when no record exists.
func (r *KycRepository) GetByUserID(ctx context.Context, userID string) (*entity.Kyc, error) {
	row := r.db.Pool.QueryRow(ctx, getKycByUserIDQuery, userID)
	vi, err := scanKyc(row.Scan)
	if err != nil {
		return nil, toAppErr(err, "failed to get Kyc by user_id",
			slog.String("user_id", userID))
	}
	return vi, nil
}

// GetByPocketSignUserID returns the ACTIVE Kyc keyed on the Pocket
// Sign person key. Returns NotFound when no active record exists for the given
// pocket_sign_user_id.
func (r *KycRepository) GetByPocketSignUserID(ctx context.Context, pocketSignUserID string) (*entity.Kyc, error) {
	row := r.db.Pool.QueryRow(ctx, getKycByPocketSignUserIDQuery, pocketSignUserID)
	vi, err := scanKyc(row.Scan)
	if err != nil {
		if pgxIsNoRows(err) {
			return nil, apperr.New(codes.NotFound, "no active Kyc for pocket_sign_user_id")
		}
		return nil, toAppErr(err, "failed to get Kyc by pocket_sign_user_id")
	}
	return vi, nil
}

// UpdateStatus changes the status of the record identified by id. Returns
// NotFound when no row matches.
func (r *KycRepository) UpdateStatus(ctx context.Context, id string, status entity.VerificationStatus) error {
	tag, err := r.db.Pool.Exec(ctx, updateKycStatusQuery, id, int16(status))
	if err != nil {
		return toAppErr(err, "failed to update Kyc status", slog.String("id", id))
	}
	if tag.RowsAffected() == 0 {
		return apperr.New(codes.NotFound, "Kyc not found")
	}
	return nil
}

// Delete removes the Kyc record. Returns NotFound when no row
// matches.
func (r *KycRepository) Delete(ctx context.Context, id string) error {
	tag, err := r.db.Pool.Exec(ctx, deleteKycQuery, id)
	if err != nil {
		return toAppErr(err, "failed to delete Kyc", slog.String("id", id))
	}
	if tag.RowsAffected() == 0 {
		return apperr.New(codes.NotFound, "Kyc not found")
	}
	return nil
}

// pgxIsNoRows is a helper to check for pgx.ErrNoRows without importing pgx in
// the caller (toAppErr already handles it for scan errors).
func pgxIsNoRows(err error) bool {
	return err == pgx.ErrNoRows
}
