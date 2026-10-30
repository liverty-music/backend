package rdb

import (
	"context"
	"log/slog"

	"github.com/google/uuid"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
)

// PasskeyRepository implements [entity.PasskeyRepository].
type PasskeyRepository struct {
	db *Database
}

// Compile-time interface compliance check.
var _ entity.PasskeyRepository = (*PasskeyRepository)(nil)

const (
	// insertPasskeyQuery stores a passkey once: a repeated provider passkey id
	// keeps the stored row unchanged.
	insertPasskeyQuery = `
		INSERT INTO passkeys (provider_passkey_id, user_id, aaguid, created_at, name)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (provider_passkey_id) DO NOTHING
	`

	deletePasskeyQuery = `
		DELETE FROM passkeys WHERE provider_passkey_id = $1
	`

	listPasskeysByUserQuery = `
		SELECT provider_passkey_id, user_id, aaguid, created_at, name
		FROM passkeys
		WHERE user_id = $1
		ORDER BY created_at DESC, provider_passkey_id
	`
)

// NewPasskeyRepository creates a new passkey repository instance.
func NewPasskeyRepository(db *Database) *PasskeyRepository {
	return &PasskeyRepository{db: db}
}

// Create stores the passkey, or does nothing when its provider passkey id is
// already stored.
func (r *PasskeyRepository) Create(ctx context.Context, passkey *entity.Passkey) error {
	if passkey == nil || passkey.ProviderPasskeyID == "" || passkey.UserID == "" || passkey.CreateTime.IsZero() {
		return apperr.New(codes.InvalidArgument, "provider passkey id, user id and create time are required")
	}
	if _, err := uuid.Parse(passkey.AAGUID); err != nil {
		return apperr.Wrap(err, codes.InvalidArgument, "aaguid must be a UUID")
	}
	attrs := []slog.Attr{slog.String("user_id", passkey.UserID), slog.String("provider_passkey_id", passkey.ProviderPasskeyID)}
	_, err := r.db.Pool.Exec(ctx, insertPasskeyQuery,
		passkey.ProviderPasskeyID, passkey.UserID, passkey.AAGUID, passkey.CreateTime, passkey.Name)
	if err != nil {
		if IsForeignKeyViolation(err) {
			return apperr.Wrap(err, codes.NotFound, "user not found", attrs...)
		}
		return toAppErr(err, "failed to create passkey", attrs...)
	}
	return nil
}

// Delete removes the passkey with the provider passkey id; removing one that
// is not stored succeeds.
func (r *PasskeyRepository) Delete(ctx context.Context, providerPasskeyID string) error {
	if providerPasskeyID == "" {
		return apperr.New(codes.InvalidArgument, "provider passkey id cannot be empty")
	}
	if _, err := r.db.Pool.Exec(ctx, deletePasskeyQuery, providerPasskeyID); err != nil {
		return toAppErr(err, "failed to delete passkey", slog.String("provider_passkey_id", providerPasskeyID))
	}
	return nil
}

// ListByUser returns the user's passkeys, newest first.
func (r *PasskeyRepository) ListByUser(ctx context.Context, userID string) ([]*entity.Passkey, error) {
	if userID == "" {
		return nil, apperr.New(codes.InvalidArgument, "user id cannot be empty")
	}
	rows, err := r.db.Pool.Query(ctx, listPasskeysByUserQuery, userID)
	if err != nil {
		return nil, toAppErr(err, "failed to list passkeys", slog.String("user_id", userID))
	}
	defer rows.Close()

	passkeys := []*entity.Passkey{}
	for rows.Next() {
		var p entity.Passkey
		if err := rows.Scan(&p.ProviderPasskeyID, &p.UserID, &p.AAGUID, &p.CreateTime, &p.Name); err != nil {
			return nil, toAppErr(err, "failed to scan passkey", slog.String("user_id", userID))
		}
		passkeys = append(passkeys, &p)
	}
	if err := rows.Err(); err != nil {
		return nil, toAppErr(err, "failed to list passkeys", slog.String("user_id", userID))
	}
	return passkeys, nil
}
