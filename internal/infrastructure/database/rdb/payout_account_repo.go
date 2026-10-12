package rdb

import (
	"context"
	"log/slog"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
)

// PayoutAccountRepository implements
// entity.PayoutAccountRepository for PostgreSQL.
type PayoutAccountRepository struct {
	db *Database
}

// NewPayoutAccountRepository creates a new repository instance.
func NewPayoutAccountRepository(db *Database) *PayoutAccountRepository {
	return &PayoutAccountRepository{db: db}
}

const (
	// upsertPayoutAccountQuery inserts a row or updates provider_account_id
	// and status when the organizer_id already exists. This allows re-running
	// provisioning after a partial failure without creating duplicate rows.
	upsertPayoutAccountQuery = `
		INSERT INTO payout_accounts (organizer_id, provider_account_id, status)
		VALUES ($1, $2, $3)
		ON CONFLICT (organizer_id) DO UPDATE
		  SET provider_account_id      = EXCLUDED.provider_account_id,
		      status           = EXCLUDED.status,
		      status_synced_at = NOW()
	`

	getPayoutAccountQuery = `
		SELECT organizer_id, provider_account_id, status
		FROM payout_accounts
		WHERE organizer_id = $1
	`

	updatePayoutAccountStatusQuery = `
		UPDATE payout_accounts
		SET status = $2
		WHERE organizer_id = $1
	`
)

// Upsert implements [entity.PayoutAccountRepository].
func (r *PayoutAccountRepository) Upsert(ctx context.Context, a *entity.PayoutAccount) error {
	_, err := r.db.Pool.Exec(ctx, upsertPayoutAccountQuery,
		a.OrganizerID,
		a.ProviderAccountID,
		int16(a.Status),
	)
	if err != nil {
		return toAppErr(err, "failed to upsert organizer connected account",
			slog.String("organizer_id", a.OrganizerID))
	}
	return nil
}

// GetByOrganizerID implements [entity.PayoutAccountRepository].
func (r *PayoutAccountRepository) GetByOrganizerID(ctx context.Context, organizerID string) (*entity.PayoutAccount, error) {
	var (
		a      entity.PayoutAccount
		status int16
	)
	row := r.db.Pool.QueryRow(ctx, getPayoutAccountQuery, organizerID)
	if err := row.Scan(&a.OrganizerID, &a.ProviderAccountID, &status); err != nil {
		return nil, toAppErr(err, "failed to get organizer connected account",
			slog.String("organizer_id", organizerID))
	}
	a.Status = entity.PayoutOnboardingStatus(status)
	return &a, nil
}

// UpdateStatus implements [entity.PayoutAccountRepository].
func (r *PayoutAccountRepository) UpdateStatus(ctx context.Context, organizerID string, status entity.PayoutOnboardingStatus) error {
	tag, err := r.db.Pool.Exec(ctx, updatePayoutAccountStatusQuery,
		organizerID, int16(status))
	if err != nil {
		return toAppErr(err, "failed to update organizer connected account status",
			slog.String("organizer_id", organizerID))
	}
	if tag.RowsAffected() == 0 {
		return apperr.New(codes.NotFound, "organizer connected account not found")
	}
	return nil
}
