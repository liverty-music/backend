package rdb_test

import (
	"context"
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	knownNamesMBIDA = "88888888-8888-4888-8888-00000000000a"
	knownNamesMBIDB = "88888888-8888-4888-8888-00000000000b"
	knownNamesMBIDC = "88888888-8888-4888-8888-00000000000c"
)

// createKnownAs registers an artist of the name under mbid with the aliases as
// its other known names, and returns it as registered.
func createKnownAs(t *testing.T, repo *rdb.ArtistRepository, name, mbid string, aliases ...string) *entity.Artist {
	t.Helper()
	artist := entity.NewArtist(name, mbid)
	artist.KnownNames = aliases
	created, err := repo.Create(context.Background(), artist)
	require.NoError(t, err)
	require.Len(t, created, 1)
	return created[0]
}

// idsByKnownNames returns the ids ListByKnownNames finds for the names.
func idsByKnownNames(t *testing.T, repo *rdb.ArtistRepository, names ...string) []string {
	t.Helper()
	artists, err := repo.ListByKnownNames(context.Background(), names)
	require.NoError(t, err)
	ids := make([]string, len(artists))
	for i, a := range artists {
		ids[i] = a.ID
	}
	return ids
}

func TestArtistRepository_ListByKnownNames(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := rdb.NewArtistRepository(db, testOutbox)
	ctx := context.Background()

	// @spec components/entity/artist/list-by-known-names "Found by an alias"
	t.Run("Found by an alias", func(t *testing.T) {
		cleanTables(t, db)
		yorushika := createKnownAs(t, repo, "ヨルシカ", knownNamesMBIDA, "Yorushika")

		got, err := repo.ListByKnownNames(ctx, []string{"Yorushika"})

		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, yorushika, got[0])
		assert.Equal(t, []string{"ヨルシカ", "yorushika"}, got[0].KnownNames)
	})

	// @spec components/entity/artist/list-by-known-names "Two artists share a name"
	t.Run("Two artists share a name", func(t *testing.T) {
		cleanTables(t, db)
		first := createKnownAs(t, repo, "tomoo", knownNamesMBIDA)
		second := createKnownAs(t, repo, "TOMOO", knownNamesMBIDB)
		// Neither the MBID status nor an ended activity hides an artist.
		setMBIDStatus(t, db, first.ID, entity.MBIDStatusAmbiguous)
		require.NoError(t, repo.UpdateActivityEnded(ctx, second.ID, knownNamesMBIDB, true))

		assert.ElementsMatch(t, []string{first.ID, second.ID}, idsByKnownNames(t, repo, "tomoo"))
	})

	// @spec components/entity/artist/list-by-known-names "Several names of one artist"
	t.Run("Several names of one artist", func(t *testing.T) {
		cleanTables(t, db)
		artist := createKnownAs(t, repo, "ヨルシカ", knownNamesMBIDA, "Yorushika")

		assert.Equal(t, []string{artist.ID}, idsByKnownNames(t, repo, "ヨルシカ", "YORUSHIKA"))
	})

	// @spec components/entity/artist/list-by-known-names "No artist"
	t.Run("No artist", func(t *testing.T) {
		cleanTables(t, db)
		createKnownAs(t, repo, "tomoo kosugi", knownNamesMBIDA)

		got, err := repo.ListByKnownNames(ctx, []string{"tomoo"})

		require.NoError(t, err)
		assert.Equal(t, []*entity.Artist{}, got)
	})

	// @spec components/entity/artist/list-by-known-names "Empty input"
	t.Run("Empty input", func(t *testing.T) {
		got, err := repo.ListByKnownNames(ctx, nil)

		require.NoError(t, err)
		assert.Equal(t, []*entity.Artist{}, got)
	})
}

func TestArtistRepository_UpdateKnownNames(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := rdb.NewArtistRepository(db, testOutbox)
	ctx := context.Background()

	// @spec components/entity/artist/update-known-names "Names replaced"
	t.Run("Names replaced", func(t *testing.T) {
		cleanTables(t, db)
		before := createKnownAs(t, repo, "ヨルシカ", knownNamesMBIDA, "Old Alias")
		announced := countAnnouncements(t, db)

		err := repo.UpdateKnownNames(ctx, before.ID, knownNamesMBIDA, []string{"ヨルシカ", "Yorushika"})

		require.NoError(t, err)
		assert.Equal(t, []string{before.ID}, idsByKnownNames(t, repo, "yorushika"))
		assert.Empty(t, idsByKnownNames(t, repo, "old alias"))
		after, err := repo.Get(ctx, before.ID)
		require.NoError(t, err)
		before.KnownNames = []string{"ヨルシカ", "yorushika"}
		assert.Equal(t, before, after, "nothing else changes")
		assert.Equal(t, announced, countAnnouncements(t, db))
	})

	// @spec components/entity/artist/update-known-names "Names of a replaced MBID"
	t.Run("Names of a replaced MBID", func(t *testing.T) {
		cleanTables(t, db)
		artist := createKnownAs(t, repo, "Penthouse", knownNamesMBIDA)
		require.NoError(t, repo.UpdateMBID(ctx, artist.ID, knownNamesMBIDB, "Penthouse", false, []string{"ペントハウス"}))

		err := repo.UpdateKnownNames(ctx, artist.ID, knownNamesMBIDA, []string{"namesake"})

		require.NoError(t, err)
		got, err := repo.Get(ctx, artist.ID)
		require.NoError(t, err)
		assert.Equal(t, []string{"penthouse", "ペントハウス"}, got.KnownNames)
	})

	// @spec components/entity/artist/update-known-names "Unknown artist"
	t.Run("Unknown artist", func(t *testing.T) {
		cleanTables(t, db)

		err := repo.UpdateKnownNames(ctx, entity.NewID(), knownNamesMBIDA, []string{"nobody"})

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})
}

func TestArtistRepository_Create_KnownNames(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := rdb.NewArtistRepository(db, testOutbox)

	// @spec components/entity/artist/create "New artist with aliases"
	t.Run("New artist with aliases", func(t *testing.T) {
		cleanTables(t, db)
		// The name is not among the given known names: Create adds it.
		artist := createKnownAs(t, repo, "ヨルシカ", knownNamesMBIDA, "Yorushika")

		assert.Equal(t, []string{artist.ID}, idsByKnownNames(t, repo, "ヨルシカ"))
		assert.Equal(t, []string{artist.ID}, idsByKnownNames(t, repo, "yorushika"))
	})

	// @spec components/entity/artist/create "New artist without aliases"
	t.Run("New artist without aliases", func(t *testing.T) {
		cleanTables(t, db)
		artist := createKnownAs(t, repo, "Ｓａｕｃｙ　Dog", knownNamesMBIDA)
		other := createKnownAs(t, repo, `Quote " and \ {brace}, comma`, knownNamesMBIDC, "NULL")

		assert.Equal(t, []string{"saucy dog"}, artist.KnownNames)
		assert.Equal(t, []string{`quote " and \ {brace}, comma`, "null"}, other.KnownNames)
		assert.Equal(t, []string{other.ID}, idsByKnownNames(t, repo, `Quote " and \ {brace}, comma`))
	})

	// @spec components/entity/artist/create "Already registered"
	t.Run("Already registered", func(t *testing.T) {
		cleanTables(t, db)
		first := createKnownAs(t, repo, "ヨルシカ", knownNamesMBIDA, "Yorushika")

		again := createKnownAs(t, repo, "ヨルシカ", knownNamesMBIDA, "Other Alias")

		assert.Equal(t, first, again)
		assert.Empty(t, idsByKnownNames(t, repo, "other alias"))
	})
}

func TestArtistRepository_UpdateMBID_KnownNames(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := rdb.NewArtistRepository(db, testOutbox)
	ctx := context.Background()

	// @spec components/entity/artist/update-mbid "Corrected artist"
	t.Run("Corrected artist", func(t *testing.T) {
		cleanTables(t, db)
		artist := createKnownAs(t, repo, "Penthouse", knownNamesMBIDA, "UK Penthouse")

		err := repo.UpdateMBID(ctx, artist.ID, knownNamesMBIDB, "Penthouse", false, []string{"ペントハウス"})

		require.NoError(t, err)
		assert.Equal(t, []string{artist.ID}, idsByKnownNames(t, repo, "ペントハウス"))
		assert.Equal(t, []string{artist.ID}, idsByKnownNames(t, repo, "penthouse"), "the stored name is added")
		assert.Empty(t, idsByKnownNames(t, repo, "uk penthouse"))
	})

	t.Run("own MBID replaces the known names", func(t *testing.T) {
		cleanTables(t, db)
		artist := createKnownAs(t, repo, "ado", knownNamesMBIDA)

		require.NoError(t, repo.UpdateMBID(ctx, artist.ID, knownNamesMBIDA, "Ado", false, []string{"アド"}))

		got, err := repo.Get(ctx, artist.ID)
		require.NoError(t, err)
		assert.Equal(t, []string{"ado", "アド"}, got.KnownNames)
	})

	// @spec components/entity/artist/update-mbid "Refused change"
	t.Run("Refused change", func(t *testing.T) {
		cleanTables(t, db)
		artist := createKnownAs(t, repo, "Penthouse", knownNamesMBIDA, "UK Penthouse")
		createKnownAs(t, repo, "Other", knownNamesMBIDB)

		err := repo.UpdateMBID(ctx, artist.ID, knownNamesMBIDB, "Penthouse", false, []string{"ペントハウス"})

		require.ErrorIs(t, err, apperr.ErrAlreadyExists)
		got, err := repo.Get(ctx, artist.ID)
		require.NoError(t, err)
		assert.Equal(t, artist.KnownNames, got.KnownNames)
	})
}
