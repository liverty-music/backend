package migrationtest

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// backfillArtistKnownNamesVersion is
// 20261128142253_backfill_artist_known_names.sql.
const backfillArtistKnownNamesVersion = "20261128142253"

// TestMigration_BackfillArtistKnownNames loads an artist registered without
// known names and one with aliases, applies
// 20261128142253_backfill_artist_known_names.sql and checks that only the
// first is given its name.
func TestMigration_BackfillArtistKnownNames(t *testing.T) {
	ctx := context.Background()
	conn, apply := setupScratchDatabase(t, ctx)
	apply(addArtistKnownNamesVersion)

	empty, aliased := id(940), id(941)
	_, err := conn.Exec(ctx, `INSERT INTO artists (id, name, mbid, listening_mbid) VALUES ($1, 'Saucy　Dog', $2, $2)`, empty, id(942))
	require.NoError(t, err)
	_, err = conn.Exec(ctx, `INSERT INTO artists (id, name, mbid, listening_mbid, known_names) VALUES ($1, 'ヨルシカ', $2, $2, '{ヨルシカ,yorushika}')`, aliased, id(943))
	require.NoError(t, err)

	apply(backfillArtistKnownNamesVersion)

	read := func(artistID string) (known []string) {
		t.Helper()
		require.NoError(t, conn.QueryRow(ctx, `SELECT known_names FROM artists WHERE id = $1`, artistID).Scan(&known))
		return known
	}
	assert.Equal(t, []string{"saucy dog"}, read(empty))
	assert.Equal(t, []string{"ヨルシカ", "yorushika"}, read(aliased), "an artist with known names keeps them")
}
