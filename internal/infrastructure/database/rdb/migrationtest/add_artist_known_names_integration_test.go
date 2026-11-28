package migrationtest

import (
	"context"
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	// addArtistKnownNamesPrevious is the last migration before
	// 20261128133546_add_artist_known_names.sql.
	addArtistKnownNamesPrevious = "20261128081810"
	// addArtistKnownNamesVersion is
	// 20261128133546_add_artist_known_names.sql.
	addArtistKnownNamesVersion = "20261128133546"
)

// TestMigration_AddArtistKnownNames loads artists, applies
// 20261128133546_add_artist_known_names.sql and checks that each artist is
// known by its name, normalized as entity.NewKnownNames normalizes it.
func TestMigration_AddArtistKnownNames(t *testing.T) {
	ctx := context.Background()
	conn, apply := setupScratchDatabase(t, ctx)
	apply(addArtistKnownNamesPrevious)

	names := []string{"Ｓａｕｃｙ　Dog", "  back \t  number ", "ヨルシカ", "ﾖﾙｼｶ", "Mr.Children"}
	for i, name := range names {
		mbid := id(920 + i)
		_, err := conn.Exec(ctx, `INSERT INTO artists (id, name, mbid, listening_mbid) VALUES ($1, $2, $3, $3)`, id(930+i), name, mbid)
		require.NoError(t, err)
	}

	apply(addArtistKnownNamesVersion)

	for i, name := range names {
		var known []string
		require.NoError(t, conn.QueryRow(ctx, `SELECT known_names FROM artists WHERE id = $1`, id(930+i)).Scan(&known))
		assert.Equal(t, entity.NewKnownNames(name), known, "name %q", name)
	}
	var known []string
	require.NoError(t, conn.QueryRow(ctx, `SELECT known_names FROM artists WHERE id = $1`, id(930)).Scan(&known))
	assert.Equal(t, []string{"saucy dog"}, known)
}
