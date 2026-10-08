package rdb

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
)

// ReceptionLinkRepository implements [entity.ReceptionLinkRepository] for
// PostgreSQL.
//
// Tokens are looked up only by their SHA-256 (token_hash) and the digests are
// compared in constant time; the token itself is kept only while the link is
// Unused, so the console can show its URL, and cleared on bind or revoke.
type ReceptionLinkRepository struct {
	db *Database
}

// Compile-time interface compliance check.
var _ entity.ReceptionLinkRepository = (*ReceptionLinkRepository)(nil)

// NewReceptionLinkRepository creates a new ReceptionLinkRepository.
func NewReceptionLinkRepository(db *Database) *ReceptionLinkRepository {
	return &ReceptionLinkRepository{db: db}
}

const (
	receptionLinkColumns = `id, event_id, number, token, token_hash, bound_public_key, status, bound_at, revoked_at`

	// receptionLinkLockEventQuery serialises link creation per event: the
	// event row lock (FOR NO KEY UPDATE, which does not block inserts that
	// reference the event) makes choosing the next number and inserting one
	// indivisible step. It also tells a missing event apart.
	receptionLinkLockEventQuery = `SELECT id FROM events WHERE id = $1 FOR NO KEY UPDATE`

	receptionLinkInsertQuery = `
		INSERT INTO reception_links (id, event_id, number, token, token_hash, status)
		SELECT $1, $2::uuid, COALESCE(MAX(number), 0) + 1, $3, $4, 1
		FROM reception_links WHERE event_id = $2::uuid
		RETURNING ` + receptionLinkColumns

	receptionLinkGetQuery        = `SELECT ` + receptionLinkColumns + ` FROM reception_links WHERE id = $1`
	receptionLinkGetForBindQuery = `SELECT ` + receptionLinkColumns + ` FROM reception_links WHERE id = $1 FOR UPDATE`
	receptionLinkGetByTokenQuery = `SELECT ` + receptionLinkColumns + ` FROM reception_links WHERE token_hash = $1`
	receptionLinkListQuery       = `SELECT ` + receptionLinkColumns + ` FROM reception_links WHERE event_id = $1 ORDER BY number`

	receptionLinkBindQuery = `
		UPDATE reception_links
		SET status = 2, bound_public_key = $2, bound_at = $3, token = NULL
		WHERE id = $1 AND status = 1
		RETURNING ` + receptionLinkColumns

	receptionLinkRevokeQuery = `
		UPDATE reception_links
		SET status = 3, revoked_at = $2, token = NULL
		WHERE id = $1 AND status <> 3
	`
)

// Create implements [entity.ReceptionLinkRepository].
func (r *ReceptionLinkRepository) Create(ctx context.Context, link *entity.ReceptionLink) (*entity.ReceptionLink, error) {
	attrs := []slog.Attr{slog.String("event_id", link.EventID)}

	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return nil, toAppErr(err, "failed to begin reception link creation", attrs...)
	}
	// Rollback is a no-op after a successful Commit.
	defer func() { _ = tx.Rollback(ctx) }()

	var eventID string
	if err := tx.QueryRow(ctx, receptionLinkLockEventQuery, link.EventID).Scan(&eventID); err != nil {
		return nil, toAppErr(err, "event not found for reception link", attrs...)
	}
	created, err := scanReceptionLink(tx.QueryRow(ctx, receptionLinkInsertQuery,
		string(link.ID), link.EventID, link.Token, entity.HashReceptionLinkToken(link.Token)))
	if err != nil {
		return nil, toAppErr(err, "failed to create reception link", attrs...)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, toAppErr(err, "failed to commit reception link creation", attrs...)
	}
	return created, nil
}

// Get implements [entity.ReceptionLinkRepository].
func (r *ReceptionLinkRepository) Get(ctx context.Context, id entity.ReceptionLinkID) (*entity.ReceptionLink, error) {
	link, err := scanReceptionLink(r.db.Pool.QueryRow(ctx, receptionLinkGetQuery, string(id)))
	if err != nil {
		return nil, toAppErr(err, "failed to get reception link", slog.String("reception_link_id", string(id)))
	}
	return link, nil
}

// GetByToken implements [entity.ReceptionLinkRepository]. The token is
// looked up by its SHA-256, and the stored digest is compared with the given
// one in constant time, so the comparison takes the same time whatever the
// token is.
func (r *ReceptionLinkRepository) GetByToken(ctx context.Context, token string) (*entity.ReceptionLink, error) {
	hash := entity.HashReceptionLinkToken(token)
	link, storedHash, err := scanReceptionLinkWithHash(r.db.Pool.QueryRow(ctx, receptionLinkGetByTokenQuery, hash))
	if err != nil {
		// No attrs: never log a token or its hash.
		return nil, toAppErr(err, "reception link not found for token")
	}
	if subtle.ConstantTimeCompare(hash, storedHash) != 1 {
		return nil, apperr.New(codes.NotFound, "reception link not found for token")
	}
	return link, nil
}

// ListByEvent implements [entity.ReceptionLinkRepository].
func (r *ReceptionLinkRepository) ListByEvent(ctx context.Context, eventID string) ([]*entity.ReceptionLink, error) {
	rows, err := r.db.Pool.Query(ctx, receptionLinkListQuery, eventID)
	if err != nil {
		return nil, toAppErr(err, "failed to list reception links", slog.String("event_id", eventID))
	}
	defer rows.Close()

	links := []*entity.ReceptionLink{}
	for rows.Next() {
		link, err := scanReceptionLink(rows)
		if err != nil {
			return nil, toAppErr(err, "failed to scan reception link", slog.String("event_id", eventID))
		}
		links = append(links, link)
	}
	if err := rows.Err(); err != nil {
		return nil, toAppErr(err, "error iterating reception links", slog.String("event_id", eventID))
	}
	return links, nil
}

// BindDevice implements [entity.ReceptionLinkRepository]. The link row is
// locked FOR UPDATE while its status is checked and the key bound, so of two
// devices opening an Unused link at once exactly one is bound.
func (r *ReceptionLinkRepository) BindDevice(ctx context.Context, id entity.ReceptionLinkID, key entity.PublicKey, boundTime time.Time) (entity.BindOutcome, *entity.ReceptionLink, error) {
	if err := key.Validate(); err != nil {
		return 0, nil, err
	}
	attrs := []slog.Attr{slog.String("reception_link_id", string(id))}

	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return 0, nil, toAppErr(err, "failed to begin reception link bind", attrs...)
	}
	// Rollback is a no-op after a successful Commit.
	defer func() { _ = tx.Rollback(ctx) }()

	link, err := scanReceptionLink(tx.QueryRow(ctx, receptionLinkGetForBindQuery, string(id)))
	if err != nil {
		return 0, nil, toAppErr(err, "failed to get reception link for bind", attrs...)
	}

	var outcome entity.BindOutcome
	switch link.Status {
	case entity.ReceptionLinkStatusUnused:
		link, err = scanReceptionLink(tx.QueryRow(ctx, receptionLinkBindQuery, string(id), []byte(key), boundTime))
		if err != nil {
			return 0, nil, toAppErr(err, "failed to bind reception link", attrs...)
		}
		outcome = entity.BindOutcomeBound
	case entity.ReceptionLinkStatusInUse:
		if link.BoundPublicKey.Equal(key) {
			outcome = entity.BindOutcomeBound
		} else {
			outcome = entity.BindOutcomeOtherDevice
		}
	default:
		outcome = entity.BindOutcomeRevoked
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, nil, toAppErr(err, "failed to commit reception link bind", attrs...)
	}
	return outcome, link, nil
}

// Revoke implements [entity.ReceptionLinkRepository]. An already Revoked
// link keeps its revoked time.
func (r *ReceptionLinkRepository) Revoke(ctx context.Context, id entity.ReceptionLinkID, revokedTime time.Time) (*entity.ReceptionLink, error) {
	if _, err := r.db.Pool.Exec(ctx, receptionLinkRevokeQuery, string(id), revokedTime); err != nil {
		return nil, toAppErr(err, "failed to revoke reception link", slog.String("reception_link_id", string(id)))
	}
	return r.Get(ctx, id)
}

// scanReceptionLink scans one reception_links row selected with
// receptionLinkColumns.
func scanReceptionLink(row pgx.Row) (*entity.ReceptionLink, error) {
	link, _, err := scanReceptionLinkWithHash(row)
	return link, err
}

// scanReceptionLinkWithHash scans one reception_links row and also returns
// its token hash.
func scanReceptionLinkWithHash(row pgx.Row) (*entity.ReceptionLink, []byte, error) {
	var (
		link      entity.ReceptionLink
		id        string
		token     sql.NullString
		tokenHash []byte
		boundKey  []byte
		status    int16
		boundAt   sql.NullTime
		revokedAt sql.NullTime
	)
	if err := row.Scan(&id, &link.EventID, &link.Number, &token, &tokenHash, &boundKey, &status, &boundAt, &revokedAt); err != nil {
		return nil, nil, err
	}
	link.ID = entity.ReceptionLinkID(id)
	if token.Valid {
		link.Token = token.String
	}
	if boundKey != nil {
		link.BoundPublicKey = entity.PublicKey(boundKey)
	}
	link.Status = entity.ReceptionLinkStatus(status)
	if boundAt.Valid {
		t := boundAt.Time
		link.BoundTime = &t
	}
	if revokedAt.Valid {
		t := revokedAt.Time
		link.RevokedTime = &t
	}
	return &link, tokenHash, nil
}
