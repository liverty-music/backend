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

// ScannerRepository implements [entity.ScannerRepository] for PostgreSQL.
//
// Link tokens are looked up only by their SHA-256 (link_token_hash) and the
// digests are compared in constant time; the link token itself is kept only
// while the Scanner is Unused, so the console can show its link, and cleared
// on bind or revoke.
type ScannerRepository struct {
	db *Database
}

// Compile-time interface compliance check.
var _ entity.ScannerRepository = (*ScannerRepository)(nil)

// NewScannerRepository creates a new ScannerRepository.
func NewScannerRepository(db *Database) *ScannerRepository {
	return &ScannerRepository{db: db}
}

const (
	scannerColumns = `id, event_id, number, link_token, link_token_hash, bound_public_key, status, bound_at, revoked_at`

	// scannerLockEventQuery serialises Scanner creation per event: the event
	// row lock (FOR NO KEY UPDATE, which does not block inserts that
	// reference the event) makes choosing the next number and inserting one
	// indivisible step. It also tells a missing event apart.
	scannerLockEventQuery = `SELECT id FROM events WHERE id = $1 FOR NO KEY UPDATE`

	scannerInsertQuery = `
		INSERT INTO scanners (id, event_id, number, link_token, link_token_hash, status)
		SELECT $1, $2::uuid, COALESCE(MAX(number), 0) + 1, $3, $4, 1
		FROM scanners WHERE event_id = $2::uuid
		RETURNING ` + scannerColumns

	scannerGetQuery            = `SELECT ` + scannerColumns + ` FROM scanners WHERE id = $1`
	scannerGetForBindQuery     = `SELECT ` + scannerColumns + ` FROM scanners WHERE id = $1 FOR UPDATE`
	scannerGetByLinkTokenQuery = `SELECT ` + scannerColumns + ` FROM scanners WHERE link_token_hash = $1`
	scannerListQuery           = `SELECT ` + scannerColumns + ` FROM scanners WHERE event_id = $1 ORDER BY number`

	scannerBindQuery = `
		UPDATE scanners
		SET status = 2, bound_public_key = $2, bound_at = $3, link_token = NULL
		WHERE id = $1 AND status = 1
		RETURNING ` + scannerColumns

	scannerRevokeQuery = `
		UPDATE scanners
		SET status = 3, revoked_at = $2, link_token = NULL
		WHERE id = $1 AND status <> 3
	`
)

// Create implements [entity.ScannerRepository].
func (r *ScannerRepository) Create(ctx context.Context, scanner *entity.Scanner) (*entity.Scanner, error) {
	attrs := []slog.Attr{slog.String("event_id", scanner.EventID)}

	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return nil, toAppErr(err, "failed to begin scanner creation", attrs...)
	}
	// Rollback is a no-op after a successful Commit.
	defer func() { _ = tx.Rollback(ctx) }()

	var eventID string
	if err := tx.QueryRow(ctx, scannerLockEventQuery, scanner.EventID).Scan(&eventID); err != nil {
		return nil, toAppErr(err, "event not found for scanner", attrs...)
	}
	created, err := scanScanner(tx.QueryRow(ctx, scannerInsertQuery,
		string(scanner.ID), scanner.EventID, scanner.LinkToken, entity.HashScannerLinkToken(scanner.LinkToken)))
	if err != nil {
		return nil, toAppErr(err, "failed to create scanner", attrs...)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, toAppErr(err, "failed to commit scanner creation", attrs...)
	}
	return created, nil
}

// Get implements [entity.ScannerRepository].
func (r *ScannerRepository) Get(ctx context.Context, id entity.ScannerID) (*entity.Scanner, error) {
	scanner, err := scanScanner(r.db.Pool.QueryRow(ctx, scannerGetQuery, string(id)))
	if err != nil {
		return nil, toAppErr(err, "failed to get scanner", slog.String("scanner_id", string(id)))
	}
	return scanner, nil
}

// GetByLinkToken implements [entity.ScannerRepository]. The link token is
// looked up by its SHA-256, and the stored digest is compared with the given
// one in constant time, so the comparison takes the same time whatever the
// link token is.
func (r *ScannerRepository) GetByLinkToken(ctx context.Context, linkToken string) (*entity.Scanner, error) {
	hash := entity.HashScannerLinkToken(linkToken)
	scanner, storedHash, err := scanScannerWithHash(r.db.Pool.QueryRow(ctx, scannerGetByLinkTokenQuery, hash))
	if err != nil {
		// No attrs: never log a link token or its hash.
		return nil, toAppErr(err, "scanner not found for link token")
	}
	if subtle.ConstantTimeCompare(hash, storedHash) != 1 {
		return nil, apperr.New(codes.NotFound, "scanner not found for link token")
	}
	return scanner, nil
}

// ListByEvent implements [entity.ScannerRepository].
func (r *ScannerRepository) ListByEvent(ctx context.Context, eventID string) ([]*entity.Scanner, error) {
	rows, err := r.db.Pool.Query(ctx, scannerListQuery, eventID)
	if err != nil {
		return nil, toAppErr(err, "failed to list scanners", slog.String("event_id", eventID))
	}
	defer rows.Close()

	scanners := []*entity.Scanner{}
	for rows.Next() {
		scanner, err := scanScanner(rows)
		if err != nil {
			return nil, toAppErr(err, "failed to scan scanner", slog.String("event_id", eventID))
		}
		scanners = append(scanners, scanner)
	}
	if err := rows.Err(); err != nil {
		return nil, toAppErr(err, "error iterating scanners", slog.String("event_id", eventID))
	}
	return scanners, nil
}

// BindDevice implements [entity.ScannerRepository]. The Scanner row is
// locked FOR UPDATE while its status is checked and the key bound, so of two
// devices opening an Unused Scanner's link at once exactly one is bound.
func (r *ScannerRepository) BindDevice(ctx context.Context, id entity.ScannerID, key entity.PublicKey, boundTime time.Time) (entity.BindOutcome, *entity.Scanner, error) {
	if err := key.Validate(); err != nil {
		return 0, nil, err
	}
	attrs := []slog.Attr{slog.String("scanner_id", string(id))}

	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return 0, nil, toAppErr(err, "failed to begin scanner bind", attrs...)
	}
	// Rollback is a no-op after a successful Commit.
	defer func() { _ = tx.Rollback(ctx) }()

	scanner, err := scanScanner(tx.QueryRow(ctx, scannerGetForBindQuery, string(id)))
	if err != nil {
		return 0, nil, toAppErr(err, "failed to get scanner for bind", attrs...)
	}

	var outcome entity.BindOutcome
	switch scanner.Status {
	case entity.ScannerStatusUnused:
		scanner, err = scanScanner(tx.QueryRow(ctx, scannerBindQuery, string(id), []byte(key), boundTime))
		if err != nil {
			return 0, nil, toAppErr(err, "failed to bind scanner", attrs...)
		}
		outcome = entity.BindOutcomeBound
	case entity.ScannerStatusInUse:
		if scanner.BoundPublicKey.Equal(key) {
			outcome = entity.BindOutcomeBound
		} else {
			outcome = entity.BindOutcomeOtherDevice
		}
	default:
		outcome = entity.BindOutcomeRevoked
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, nil, toAppErr(err, "failed to commit scanner bind", attrs...)
	}
	return outcome, scanner, nil
}

// Revoke implements [entity.ScannerRepository]. An already Revoked Scanner
// keeps its revoked time.
func (r *ScannerRepository) Revoke(ctx context.Context, id entity.ScannerID, revokedTime time.Time) (*entity.Scanner, error) {
	if _, err := r.db.Pool.Exec(ctx, scannerRevokeQuery, string(id), revokedTime); err != nil {
		return nil, toAppErr(err, "failed to revoke scanner", slog.String("scanner_id", string(id)))
	}
	return r.Get(ctx, id)
}

// scanScanner scans one scanners row selected with scannerColumns.
func scanScanner(row pgx.Row) (*entity.Scanner, error) {
	scanner, _, err := scanScannerWithHash(row)
	return scanner, err
}

// scanScannerWithHash scans one scanners row and also returns its link token
// hash.
func scanScannerWithHash(row pgx.Row) (*entity.Scanner, []byte, error) {
	var (
		scanner       entity.Scanner
		id            string
		linkToken     sql.NullString
		linkTokenHash []byte
		boundKey      []byte
		status        int16
		boundAt       sql.NullTime
		revokedAt     sql.NullTime
	)
	if err := row.Scan(&id, &scanner.EventID, &scanner.Number, &linkToken, &linkTokenHash, &boundKey, &status, &boundAt, &revokedAt); err != nil {
		return nil, nil, err
	}
	scanner.ID = entity.ScannerID(id)
	if linkToken.Valid {
		scanner.LinkToken = linkToken.String
	}
	if boundKey != nil {
		scanner.BoundPublicKey = entity.PublicKey(boundKey)
	}
	scanner.Status = entity.ScannerStatus(status)
	if boundAt.Valid {
		t := boundAt.Time
		scanner.BoundTime = &t
	}
	if revokedAt.Valid {
		t := revokedAt.Time
		scanner.RevokedTime = &t
	}
	return &scanner, linkTokenHash, nil
}
