package migrationtest

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	// verifyArtistMBIDPrevious is the last migration before
	// 20261128081810_verify_artist_mbid.sql.
	verifyArtistMBIDPrevious = "20261128071639"
	// verifyArtistMBIDVersion is 20261128081810_verify_artist_mbid.sql.
	verifyArtistMBIDVersion = "20261128081810"
)

// TestMigration_VerifyArtistMBID loads three artists: Penthouse under the
// MBID it was corrected to by hand, the test artist "Liverty Music Band" and
// Ado. It applies 20261128081810_verify_artist_mbid.sql and checks that every
// artist has its mbid as its listening_mbid, except Penthouse, whose
// listening_mbid is the MBID the listening service returns for it, and is
// Unverified, except the test artist, which is Verified.
func TestMigration_VerifyArtistMBID(t *testing.T) {
	ctx := context.Background()
	conn, apply := setupScratchDatabase(t, ctx)
	apply(verifyArtistMBIDPrevious)

	const (
		penthouseMBID          = "9604bb00-65c5-4b67-a174-c46c063ba794"
		penthouseListeningMBID = "c94eaf81-baae-465d-8197-9951c2ef52cd"
		adoMBID                = "ca195d97-30d9-4870-8a52-7fd3c2e175c3"
		testArtistID           = "01a16d79-1fe5-7825-9fb0-dd6818f202f0"
		testArtistMBID         = "578249c9-c309-4763-addb-f9a4c14e5f1b"
	)
	ado, penthouse := id(910), id(911)
	for _, row := range []struct{ id, name, mbid string }{
		{ado, "Ado", adoMBID},
		{penthouse, "Penthouse", penthouseMBID},
		{testArtistID, "Liverty Music Band", testArtistMBID},
	} {
		_, err := conn.Exec(ctx, `INSERT INTO artists (id, name, mbid) VALUES ($1, $2, $3)`, row.id, row.name, row.mbid)
		require.NoError(t, err)
	}

	apply(verifyArtistMBIDVersion)

	read := func(artistID string) (mbid, listening, status string) {
		t.Helper()
		require.NoError(t, conn.QueryRow(ctx,
			`SELECT mbid, listening_mbid, mbid_status FROM artists WHERE id = $1`, artistID).Scan(&mbid, &listening, &status))
		return mbid, listening, status
	}

	mbid, listening, status := read(ado)
	assert.Equal(t, adoMBID, mbid)
	assert.Equal(t, adoMBID, listening, "an artist's listening_mbid is backfilled from its mbid")
	assert.Equal(t, "unverified", status)

	mbid, listening, status = read(penthouse)
	assert.Equal(t, penthouseMBID, mbid)
	assert.Equal(t, penthouseListeningMBID, listening, "Penthouse keeps the MBID the listening service returns for it")
	assert.Equal(t, "unverified", status)

	mbid, listening, status = read(testArtistID)
	assert.Equal(t, testArtistMBID, mbid)
	assert.Equal(t, testArtistMBID, listening)
	assert.Equal(t, "verified", status, "the test artist the owner confirmed is Verified")

	_, err := conn.Exec(ctx, `INSERT INTO artists (id, name, mbid, listening_mbid) VALUES ($1, 'Other', $2, $3)`,
		id(912), "11111111-1111-4111-8111-111111111111", penthouseListeningMBID)
	assert.Error(t, err, "a listening_mbid belongs to one artist only")

	var otherMBIDsColumns int
	require.NoError(t, conn.QueryRow(ctx,
		`SELECT count(*) FROM information_schema.columns WHERE table_name = 'artists' AND column_name = 'other_mbids'`).Scan(&otherMBIDsColumns))
	assert.Zero(t, otherMBIDsColumns, "other_mbids is dropped")
}
