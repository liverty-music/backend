package rdb

import (
	"context"
	"log/slog"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
)

// OrganizerConnectedAccountRepository implements
// entity.OrganizerConnectedAccountRepository for PostgreSQL.
type OrganizerConnectedAccountRepository struct {
	db *Database
}

// Compile-time interface compliance check.
var _ entity.OrganizerConnectedAccountRepository = (*OrganizerConnectedAccountRepository)(nil)

// NewOrganizerConnectedAccountRepository creates a new repository instance.
func NewOrganizerConnectedAccountRepository(db *Database) *OrganizerConnectedAccountRepository {
	return &OrganizerConnectedAccountRepository{db: db}
}

const (
	// upsertOrganizerConnectedAccountQuery inserts a row or updates account_ref
	// and status when the organizer_id already exists. This allows re-running
	// provisioning after a partial failure without creating duplicate rows.
	upsertOrganizerConnectedAccountQuery = `
		INSERT INTO payout_accounts (organizer_id, provider_account_id, status)
		VALUES ($1, $2, $3)
		ON CONFLICT (organizer_id) DO UPDATE
		  SET provider_account_id      = EXCLUDED.provider_account_id,
		      status           = EXCLUDED.status,
		      status_synced_at = NOW()
	`

	getOrganizerConnectedAccountQuery = `
		SELECT organizer_id, provider_account_id, status
		FROM payout_accounts
		WHERE organizer_id = $1
	`

	updateOrganizerConnectedAccountStatusQuery = `
		UPDATE payout_accounts
		SET status = $2
		WHERE organizer_id = $1
	`
)

// Upsert implements [entity.OrganizerConnectedAccountRepository].
func (r *OrganizerConnectedAccountRepository) Upsert(ctx context.Context, a *entity.OrganizerConnectedAccount) error {
	_, err := r.db.Pool.Exec(ctx, upsertOrganizerConnectedAccountQuery,
		a.OrganizerID,
		a.AccountRef,
		int16(a.Status),
	)
	if err != nil {
		return toAppErr(err, "failed to upsert organizer connected account",
			slog.String("organizer_id", a.OrganizerID))
	}
	return nil
}

// GetByOrganizerID implements [entity.OrganizerConnectedAccountRepository].
func (r *OrganizerConnectedAccountRepository) GetByOrganizerID(ctx context.Context, organizerID string) (*entity.OrganizerConnectedAccount, error) {
	var (
		a      entity.OrganizerConnectedAccount
		status int16
	)
	row := r.db.Pool.QueryRow(ctx, getOrganizerConnectedAccountQuery, organizerID)
	if err := row.Scan(&a.OrganizerID, &a.AccountRef, &status); err != nil {
		return nil, toAppErr(err, "failed to get organizer connected account",
			slog.String("organizer_id", organizerID))
	}
	a.Status = entity.PayoutOnboardingStatus(status)
	return &a, nil
}

// UpdateStatus implements [entity.OrganizerConnectedAccountRepository].
func (r *OrganizerConnectedAccountRepository) UpdateStatus(ctx context.Context, organizerID string, status entity.PayoutOnboardingStatus) error {
	tag, err := r.db.Pool.Exec(ctx, updateOrganizerConnectedAccountStatusQuery,
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
