package migrationtest

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// renameReceptionLinksPrevious is the last migration before
// 20261012220000_rename_reception_links_to_scanners.sql.
const renameReceptionLinksPrevious = "20261012100000"

// TestMigration_RenameReceptionLinksToScanners loads an Unused and an InUse
// reception link and a rejected scan in the old shape, applies
// 20261012220000_rename_reception_links_to_scanners.sql and reads them back as
// Scanners, so an issued link keeps its link token and a bound device keeps
// its key across the rename.
func TestMigration_RenameReceptionLinksToScanners(t *testing.T) {
	ctx := context.Background()
	conn, apply := setupScratchDatabase(t, ctx)
	apply(renameReceptionLinksPrevious)

	var (
		venueID   = id(1)
		seriesID  = id(2)
		eventID   = id(3)
		unusedID  = id(70)
		inUseID   = id(71)
		scanID    = id(72)
		linkToken = "tok_0123456789abcdefghijklmn"
	)
	at := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)

	mustExec := func(sql string, args ...any) {
		t.Helper()
		_, err := conn.Exec(ctx, sql, args...)
		require.NoError(t, err, sql)
	}
	mustExec(`INSERT INTO venues (id, name) VALUES ($1, 'Zepp Haneda')`, venueID)
	mustExec(`INSERT INTO series (id, title, type) VALUES ($1, 'Tour 2026', 'TOUR')`, seriesID)
	mustExec(`INSERT INTO events (id, series_id, venue_id, local_event_date) VALUES ($1, $2, $3, '2026-12-01')`, eventID, seriesID, venueID)
	mustExec(`INSERT INTO reception_links (id, event_id, number, token, token_hash, status)
	      VALUES ($1, $2, 1, $3::text, sha256(convert_to($3::text, 'UTF8')), 1)`, unusedID, eventID, linkToken)
	mustExec(`INSERT INTO reception_links (id, event_id, number, token_hash, bound_public_key, status, bound_at)
	      VALUES ($1, $2, 2, decode(repeat('cd', 32), 'hex'), decode('04' || repeat('ab', 64), 'hex'), 2, $3)`, inUseID, eventID, at)
	mustExec(`INSERT INTO rejected_scans (id, event_id, reception_link_id, reason, scanned_at) VALUES ($1, $2, $3, 1, $4)`,
		scanID, eventID, inUseID, at)

	apply("")

	t.Run("an Unused scanner keeps its link token", func(t *testing.T) {
		var gotID string
		require.NoError(t, conn.QueryRow(ctx,
			`SELECT id FROM scanners WHERE link_token = $1::text AND link_token_hash = sha256(convert_to($1::text, 'UTF8')) AND status = 1`, linkToken).Scan(&gotID))
		assert.Equal(t, unusedID, gotID)
	})

	t.Run("an InUse scanner keeps its bound device", func(t *testing.T) {
		var (
			number   int
			keyBytes int
			boundAt  time.Time
		)
		require.NoError(t, conn.QueryRow(ctx,
			`SELECT number, octet_length(bound_public_key), bound_at FROM scanners WHERE id = $1 AND status = 2`, inUseID).Scan(&number, &keyBytes, &boundAt))
		assert.Equal(t, 2, number)
		assert.Equal(t, 65, keyBytes)
		assert.True(t, boundAt.Equal(at))
	})

	t.Run("a rejected scan names its scanner", func(t *testing.T) {
		var scannerID string
		require.NoError(t, conn.QueryRow(ctx, `SELECT scanner_id FROM rejected_scans WHERE id = $1`, scanID).Scan(&scannerID))
		assert.Equal(t, inUseID, scannerID)
	})

	t.Run("constraints carry the new names", func(t *testing.T) {
		_, err := conn.Exec(ctx, `UPDATE scanners SET status = 2 WHERE id = $1`, unusedID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "chk_scanners_in_use_bound")

		_, err = conn.Exec(ctx, `UPDATE scanners SET link_token = 'again' WHERE id = $1`, inUseID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "chk_scanners_link_token_only_unused")

		_, err = conn.Exec(ctx, `INSERT INTO rejected_scans (id, event_id, scanner_id, reason, scanned_at) VALUES ($1, $2, $3, 1, $4)`,
			id(73), eventID, id(99), at)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "rejected_scans_scanner_id_fkey")
	})
}
