package rdb

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/liverty-music/backend/internal/adapter/pb"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/messaging"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
)

// PasskeyRepository implements [entity.PasskeyRepository].
type PasskeyRepository struct {
	db     *Database
	outbox Announcer
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
		RETURNING provider_passkey_id
	`

	deletePasskeyQuery = `
		DELETE FROM passkeys WHERE provider_passkey_id = $1
		RETURNING provider_passkey_id, user_id, aaguid, created_at, name
	`

	listPasskeysByUserQuery = `
		SELECT provider_passkey_id, user_id, aaguid, created_at, name
		FROM passkeys
		WHERE user_id = $1
		ORDER BY created_at DESC, provider_passkey_id
	`
)

// NewPasskeyRepository creates a new passkey repository instance.
func NewPasskeyRepository(db *Database, outbox Announcer) *PasskeyRepository {
	return &PasskeyRepository{db: db, outbox: outbox}
}

// Create stores the passkey, or does nothing when its provider passkey id is
// already stored.
//
// # Announces
//
// USER.passkey_added with the stored Passkey, in the transaction of the
// insert. Nothing when the provider passkey id was already stored.
func (r *PasskeyRepository) Create(ctx context.Context, passkey *entity.Passkey) error {
	if passkey == nil || passkey.ProviderPasskeyID == "" || passkey.UserID == "" || passkey.CreateTime.IsZero() {
		return apperr.New(codes.InvalidArgument, "provider passkey id, user id and create time are required")
	}
	if _, err := uuid.Parse(passkey.AAGUID); err != nil {
		return apperr.Wrap(err, codes.InvalidArgument, "aaguid must be a UUID")
	}
	attrs := []slog.Attr{slog.String("user_id", passkey.UserID), slog.String("provider_passkey_id", passkey.ProviderPasskeyID)}
	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return toAppErr(err, "failed to begin passkey create", attrs...)
	}
	// Rollback is a no-op after a successful Commit.
	defer func() { _ = tx.Rollback(ctx) }()

	var stored string
	err = tx.QueryRow(ctx, insertPasskeyQuery,
		passkey.ProviderPasskeyID, passkey.UserID, passkey.AAGUID, passkey.CreateTime, passkey.Name).Scan(&stored)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		if IsForeignKeyViolation(err) {
			return apperr.Wrap(err, codes.NotFound, "user not found", attrs...)
		}
		return toAppErr(err, "failed to create passkey", attrs...)
	}
	if err := announce(ctx, r.outbox, tx, entity.SubjectUserPasskeyAdded, messaging.Announcement{Payload: pb.FromPasskey(passkey)}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return toAppErr(err, "failed to commit passkey create", attrs...)
	}
	return nil
}

// Delete removes the passkey with the provider passkey id; removing one that
// is not stored succeeds.
//
// # Announces
//
// USER.passkey_removed with the Passkey as it was stored, in the transaction
// of the removal. Nothing when no Passkey had the id.
func (r *PasskeyRepository) Delete(ctx context.Context, providerPasskeyID string) error {
	if providerPasskeyID == "" {
		return apperr.New(codes.InvalidArgument, "provider passkey id cannot be empty")
	}
	attrs := []slog.Attr{slog.String("provider_passkey_id", providerPasskeyID)}
	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return toAppErr(err, "failed to begin passkey delete", attrs...)
	}
	// Rollback is a no-op after a successful Commit.
	defer func() { _ = tx.Rollback(ctx) }()

	var (
		p    entity.Passkey
		name sql.NullString
	)
	err = tx.QueryRow(ctx, deletePasskeyQuery, providerPasskeyID).Scan(&p.ProviderPasskeyID, &p.UserID, &p.AAGUID, &p.CreateTime, &name)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return toAppErr(err, "failed to delete passkey", attrs...)
	}
	if name.Valid {
		p.Name = &name.String
	}
	if err := announce(ctx, r.outbox, tx, entity.SubjectUserPasskeyRemoved, messaging.Announcement{Payload: pb.FromPasskey(&p)}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return toAppErr(err, "failed to commit passkey delete", attrs...)
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
		var (
			p    entity.Passkey
			name sql.NullString
		)
		if err := rows.Scan(&p.ProviderPasskeyID, &p.UserID, &p.AAGUID, &p.CreateTime, &name); err != nil {
			return nil, toAppErr(err, "failed to scan passkey", slog.String("user_id", userID))
		}
		if name.Valid {
			p.Name = &name.String
		}
		passkeys = append(passkeys, &p)
	}
	if err := rows.Err(); err != nil {
		return nil, toAppErr(err, "failed to list passkeys", slog.String("user_id", userID))
	}
	return passkeys, nil
}
