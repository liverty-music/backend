package rdb

import (
	"context"
	"log/slog"
	"time"

	"github.com/liverty-music/backend/internal/entity"
)

// WalletPublicKeyRepository implements [entity.WalletPublicKeyRepository] for
// PostgreSQL. One row per user; a new key replaces the row in one upsert.
type WalletPublicKeyRepository struct {
	db *Database
}

// Compile-time interface compliance check.
var _ entity.WalletPublicKeyRepository = (*WalletPublicKeyRepository)(nil)

// NewWalletPublicKeyRepository creates a new WalletPublicKeyRepository.
func NewWalletPublicKeyRepository(db *Database) *WalletPublicKeyRepository {
	return &WalletPublicKeyRepository{db: db}
}

const (
	// walletPublicKeyRegisterQuery upserts the user's key in one statement.
	// RETURNING old/new (PostgreSQL 18) reports the row version the upsert
	// actually replaced — the latest committed one, locked by ON CONFLICT — so
	// "replaced" is exact under concurrent registrations. Registering the key
	// the user already has keeps the stored registered_at.
	walletPublicKeyRegisterQuery = `
		INSERT INTO wallet_public_keys AS w (user_id, public_key, registered_at)
		VALUES ($1, $2, $3)
		ON CONFLICT (user_id) DO UPDATE
			SET public_key = EXCLUDED.public_key,
			    registered_at = CASE WHEN w.public_key = EXCLUDED.public_key
			                         THEN w.registered_at ELSE EXCLUDED.registered_at END
		RETURNING new.public_key, new.registered_at,
		          COALESCE(old.public_key <> new.public_key, FALSE)
	`

	walletPublicKeyGetByUserQuery = `
		SELECT public_key, registered_at FROM wallet_public_keys WHERE user_id = $1
	`
)

// Register implements [entity.WalletPublicKeyRepository].
func (r *WalletPublicKeyRepository) Register(ctx context.Context, userID entity.UserID, key entity.PublicKey, registeredTime time.Time) (*entity.WalletPublicKey, bool, error) {
	if err := key.Validate(); err != nil {
		return nil, false, err
	}
	var (
		stored   []byte
		at       time.Time
		replaced bool
	)
	if err := r.db.Pool.QueryRow(ctx, walletPublicKeyRegisterQuery, string(userID), []byte(key), registeredTime).
		Scan(&stored, &at, &replaced); err != nil {
		return nil, false, toAppErr(err, "failed to register wallet public key", slog.String("user_id", string(userID)))
	}
	return &entity.WalletPublicKey{UserID: userID, PublicKey: entity.PublicKey(stored), RegisteredTime: at}, replaced, nil
}

// GetByUser implements [entity.WalletPublicKeyRepository].
func (r *WalletPublicKeyRepository) GetByUser(ctx context.Context, userID entity.UserID) (*entity.WalletPublicKey, error) {
	var (
		stored []byte
		at     time.Time
	)
	if err := r.db.Pool.QueryRow(ctx, walletPublicKeyGetByUserQuery, string(userID)).Scan(&stored, &at); err != nil {
		return nil, toAppErr(err, "failed to get wallet public key", slog.String("user_id", string(userID)))
	}
	return &entity.WalletPublicKey{UserID: userID, PublicKey: entity.PublicKey(stored), RegisteredTime: at}, nil
}
